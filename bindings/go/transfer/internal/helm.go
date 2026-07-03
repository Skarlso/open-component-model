package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	v2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	helmv1 "ocm.software/open-component-model/bindings/go/helm/spec/access/v1"
	helmv1alpha1 "ocm.software/open-component-model/bindings/go/helm/transformation/spec/v1alpha1"
	ociv1 "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	"ocm.software/open-component-model/bindings/go/oci/spec/repository/v1/oci"
	"ocm.software/open-component-model/bindings/go/runtime"
	transformv1alpha1 "ocm.software/open-component-model/bindings/go/transform/spec/v1alpha1"
	"ocm.software/open-component-model/bindings/go/transform/spec/v1alpha1/meta"
)

const (
	// AnnotationComponent carries the component identity ("name:version") on the
	// wrapper OCI manifest for provenance.
	AnnotationComponent = "software.ocm/component"
	// AnnotationRepository carries the serialized source repository specification
	// on the wrapper OCI manifest for provenance.
	AnnotationRepository = "software.ocm/repository"
)

func processHelm(resource v2.Resource, id string, val *discoveryValue, tgd *transformv1alpha1.TransformationGraphDefinition, toSpec runtime.Typed, resourceTransformIDs map[int]string, i int, uploadAsOCIArtifact bool) error {
	resourceIdentity := resource.ToIdentity()
	resourceID := identityToTransformationID(resourceIdentity)
	getResourceID := fmt.Sprintf("%sGet%s", id, resourceID)
	convertResourceID := fmt.Sprintf("%sConvert%s", id, resourceID)
	addResourceID := fmt.Sprintf("%sAdd%s", id, resourceID)

	unstructured, err := runtime.UnstructuredFromMixedData(map[string]any{
		"resource": resource,
	})
	if err != nil {
		return fmt.Errorf("cannot create unstructured spec for GetHelmChartV1alpha1 transformation: %w", err)
	}

	// Create GetHelmChart transformation
	getChartTransform := transformv1alpha1.GenericTransformation{
		TransformationMeta: meta.TransformationMeta{
			Type: helmv1alpha1.GetHelmChartV1alpha1,
			ID:   getResourceID,
		},
		Spec: unstructured,
	}
	tgd.Transformations = append(tgd.Transformations, getChartTransform)

	// convert chart to oci artifact transformation
	convertToOCITransform := transformv1alpha1.GenericTransformation{
		TransformationMeta: meta.TransformationMeta{
			Type: helmv1alpha1.ConvertHelmToOCIV1alpha1,
			ID:   convertResourceID,
		},
		Spec: &runtime.Unstructured{Data: map[string]any{
			"resource":  fmt.Sprintf("${%s.output.resource}", getResourceID),
			"chartFile": fmt.Sprintf("${%s.output.chartFile}", getResourceID),
			"provFile":  fmt.Sprintf("${%s.output.?provFile}", getResourceID),
		}},
	}
	tgd.Transformations = append(tgd.Transformations, convertToOCITransform)

	// Create upload transformations
	var addResourceTransform transformv1alpha1.GenericTransformation
	if uploadAsOCIArtifact {
		if addResourceTransform, err = ociUploadAsArtifact(toSpec, addResourceID, convertResourceID, imageReferenceFromAccess(convertResourceID)); err != nil {
			return fmt.Errorf("failed to create oci upload transformation: %w", err)
		}
	} else {
		if addResourceTransform, err = ociUploadAsLocalResource(toSpec, val.Descriptor.Component.Name, val.Descriptor.Component.Version, addResourceID, convertResourceID, imageReferenceFromAccess(convertResourceID)); err != nil {
			return fmt.Errorf("failed to create oci upload as local resource transformation: %w", err)
		}
	}

	tgd.Transformations = append(tgd.Transformations, addResourceTransform)

	// Track this resource's transformation
	resourceTransformIDs[i] = addResourceID

	return nil
}

// processLocalizeWrappers appends a GenerateHelmWrapper transformation for every
// Helm chart resource in the component. Returns cel expression so the cleanup
// node wait before removing the wrapper. Learned that the hard way. :D
func processLocalizeWrappers(
	ctx context.Context,
	v2desc *v2.Descriptor,
	id string,
	val *discoveryValue,
	tgd *transformv1alpha1.TransformationGraphDefinition,
	toSpec runtime.Typed,
	resourceTransformIDs map[int]string,
	resourceAccesses map[int]runtime.Typed,
	signWrapper bool,
) ([]string, error) {
	if _, isOCITarget := toSpec.(*oci.Repository); !isOCITarget {
		slog.DebugContext(ctx, "skipping helm chart localization: target repository is not an OCI registry",
			"component", val.Descriptor.Component.Name, "version", val.Descriptor.Component.Version)
		return nil, nil
	}

	annotations, err := createWrapperAnnotations(val)
	if err != nil {
		return nil, err
	}

	imagePairs, err := collectImagePairs(v2desc, resourceTransformIDs, resourceAccesses)
	if err != nil {
		return nil, err
	}

	var fileExpressions []string
	for i, resource := range v2desc.Component.Resources {
		if _, isHelm := resourceAccesses[i].(*helmv1.Helm); !isHelm {
			continue
		}
		addResourceID, ok := resourceTransformIDs[i]
		if !ok {
			continue
		}

		resourceID := identityToTransformationID(resource.ToIdentity())
		getResourceID := fmt.Sprintf("%sGet%s", id, resourceID)
		wrapResourceID := fmt.Sprintf("%sWrap%s", id, resourceID)

		wrapTransform := transformv1alpha1.GenericTransformation{
			TransformationMeta: meta.TransformationMeta{
				Type: helmv1alpha1.GenerateHelmWrapperV1alpha1,
				ID:   wrapResourceID,
			},
			Spec: &runtime.Unstructured{Data: map[string]any{
				"chartFile":     fmt.Sprintf("${%s.output.chartFile}", getResourceID),
				"chartResource": fmt.Sprintf("${%s.output.resource}", addResourceID),
				"images":        imagePairs,
				"annotations":   annotations,
			}},
		}
		tgd.Transformations = append(tgd.Transformations, wrapTransform)

		if signWrapper {
			signTransform := transformv1alpha1.GenericTransformation{
				TransformationMeta: meta.TransformationMeta{
					Type: SignOCIArtifactVersionedType,
					ID:   fmt.Sprintf("%sSign%s", id, resourceID),
				},
				Spec: &runtime.Unstructured{Data: map[string]any{
					"resource": fmt.Sprintf("${%s.output.resource}", wrapResourceID),
				}},
			}
			tgd.Transformations = append(tgd.Transformations, signTransform)
		}

		fileExpressions = append(fileExpressions, fmt.Sprintf("${%s.spec.chartFile}", wrapResourceID))
	}
	return fileExpressions, nil
}

// collectImagePairs pairs every OCI image resource of the component with its
// Add/Transfer transformation output, so the wrapper transformer can map the
// original image reference to its transferred location.
func collectImagePairs(v2desc *v2.Descriptor, resourceTransformIDs map[int]string, resourceAccesses map[int]runtime.Typed) ([]any, error) {
	var pairs []any
	for i, resource := range v2desc.Component.Resources {
		if _, isOCIImage := resourceAccesses[i].(*ociv1.OCIImage); !isOCIImage {
			continue
		}
		transformID, ok := resourceTransformIDs[i]
		if !ok {
			continue
		}

		source, err := resourceToMap(resource)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, map[string]any{
			"source": source,
			"target": fmt.Sprintf("${%s.output.resource}", transformID),
		})
	}
	return pairs, nil
}

// createWrapperAnnotations builds the provenance annotations linking the
// wrapper back to the component version and its source repository.
func createWrapperAnnotations(val *discoveryValue) (map[string]any, error) {
	annotations := map[string]any{
		AnnotationComponent: fmt.Sprintf("%s:%s", val.Descriptor.Component.Name, val.Descriptor.Component.Version),
	}
	if val.SourceRepository != nil {
		raw, err := json.Marshal(val.SourceRepository)
		if err != nil {
			return nil, fmt.Errorf("cannot marshal source repository spec for wrapper provenance: %w", err)
		}
		annotations[AnnotationRepository] = string(raw)
	}
	return annotations, nil
}

// resourceToMap converts a v2 resource into a plain map for inlining into an
// unstructured transformation spec.
func resourceToMap(resource v2.Resource) (map[string]any, error) {
	raw, err := json.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal resource: %w", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("cannot unmarshal resource into map: %w", err)
	}
	return m, nil
}
