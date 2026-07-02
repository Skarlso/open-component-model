package transformation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"

	"helm.sh/helm/v4/pkg/chart/v2/loader"

	"ocm.software/open-component-model/bindings/go/blob"
	"ocm.software/open-component-model/bindings/go/blob/filesystem"
	"ocm.software/open-component-model/bindings/go/credentials"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"ocm.software/open-component-model/bindings/go/helm/localize"
	"ocm.software/open-component-model/bindings/go/helm/transformation/spec/v1alpha1"
	"ocm.software/open-component-model/bindings/go/oci/looseref"
	ocischeme "ocm.software/open-component-model/bindings/go/oci/spec/access"
	ociaccess "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	"ocm.software/open-component-model/bindings/go/repository"
	"ocm.software/open-component-model/bindings/go/runtime"
)

// WrapperRequest contains details for creating and pushing the Wrapper OCI Artifact.
type WrapperRequest struct {
	// Chart is the original chart .tgz.
	Chart blob.ReadOnlyBlob
	// Images contains the mappings for the original > target locations.
	Images []localize.ImageMapping
	// ChartReference is the OCI reference the original chart was uploaded to.
	// For example `"target.registry/charts/podinfo:6.11.1"`. The wrapper is
	// uploaded next to it.
	ChartReference string
	// Annotations contains component id and repo spec.
	Annotations map[string]string
	// TempDir holds intermediate files.
	TempDir string
}

// GenerateAndUploadWrapper loads the original chart, generates its localized
// wrapper, packages it as an OCI artifact and pushes it to
// <target-repository>/<chart>-localized:<version>. It returns the uploaded
// resource with its access.
func GenerateAndUploadWrapper(ctx context.Context, repo repository.ResourceRepository, creds runtime.Typed, req WrapperRequest) (_ *descriptor.Resource, err error) {
	rc, err := req.Chart.ReadCloser()
	if err != nil {
		return nil, fmt.Errorf("reading chart archive: %w", err)
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()

	chart, err := loader.LoadArchive(rc)
	if err != nil {
		return nil, fmt.Errorf("loading chart archive: %w", err)
	}

	targetRepository, err := getTargetChartRepository(req.ChartReference, chart.Name())
	if err != nil {
		return nil, fmt.Errorf("deriving wrapper repository from chart reference %q: %w", req.ChartReference, err)
	}

	wrapper, err := localize.CreateHelmWrapper(localize.WrapperMeta{
		ChartName:    chart.Name(),
		ChartVersion: chart.Metadata.Version,
		Dependency:   "oci://" + targetRepository,
	}, localize.Scan(chart.Values, req.Images))
	if err != nil {
		return nil, fmt.Errorf("creating wrapper: %w", err)
	}

	pkg, err := localize.Package(ctx, wrapper, req.TempDir, req.Annotations)
	if err != nil {
		return nil, fmt.Errorf("packaging wrapper: %w", err)
	}

	targetRef := fmt.Sprintf("%s/%s:%s", targetRepository, wrapper.Metadata.Name, wrapper.Metadata.Version)

	res := &descriptor.Resource{}
	res.Name = wrapper.Metadata.Name
	res.Version = wrapper.Metadata.Version
	res.Type = "helmChart"
	res.Access = &ociaccess.OCIImage{
		Type:           runtime.Type{Name: ociaccess.LegacyType, Version: ociaccess.LegacyTypeVersion},
		ImageReference: targetRef,
	}

	uploaded, err := repo.UploadResource(ctx, res, pkg.Layout, creds)
	if err != nil {
		return nil, fmt.Errorf("uploading wrapper: %w", err)
	}
	return uploaded, nil
}

// GenerateHelmWrapper is a transformer that generates a localized wrapper chart
// for a transferred Helm chart and uploads it next to the chart in the target
// OCI repository.
type GenerateHelmWrapper struct {
	Scheme             *runtime.Scheme
	Repository         repository.ResourceRepository
	CredentialProvider credentials.Resolver
}

func (t *GenerateHelmWrapper) Transform(ctx context.Context, step runtime.Typed) (runtime.Typed, error) {
	// TODO: much of this is stolen from convert_helm_chart_to_oci without shame. :D
	var transformation v1alpha1.GenerateHelmWrapper
	if err := t.Scheme.Convert(step, &transformation); err != nil {
		return nil, fmt.Errorf("failed converting generic transformation to generate helm wrapper transformation: %w", err)
	}
	if transformation.Spec == nil {
		return nil, fmt.Errorf("spec is required for generate helm wrapper transformation")
	}
	if transformation.Spec.ChartFile.URI == "" {
		return nil, fmt.Errorf("spec.chartFile.uri is required for generate helm wrapper transformation")
	}
	if transformation.Spec.ChartResource == nil || transformation.Spec.ChartResource.Access == nil {
		return nil, fmt.Errorf("spec.chartResource and spec.chartResource.access are required for generate helm wrapper transformation")
	}

	chartBlob, err := fileBlobFromURI(transformation.Spec.ChartFile.URI)
	if err != nil {
		return nil, fmt.Errorf("failed reading chart file spec: %w", err)
	}

	chartImage, err := ociImageFromAccess(transformation.Spec.ChartResource.Access)
	if err != nil {
		return nil, fmt.Errorf("chart resource access must be an OCI image: %w", err)
	}

	images := make([]localize.ImageMapping, 0, len(transformation.Spec.Images))
	for _, pair := range transformation.Spec.Images {
		mapping, ok, err := imageMappingFromPair(ctx, pair)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		images = append(images, mapping)
	}

	chartResource := descriptor.ConvertFromV2Resource(transformation.Spec.ChartResource)
	creds, err := resolveCredentials(ctx, t.Repository, t.CredentialProvider, chartResource)
	if err != nil {
		return nil, err
	}

	tempDir := transformation.Spec.OutputPath
	if tempDir == "" {
		if tempDir, err = os.MkdirTemp("", "helm-wrapper-*"); err != nil {
			return nil, fmt.Errorf("failed creating temporary directory for wrapper packaging: %w", err)
		}
		defer func() {
			if rerr := os.RemoveAll(tempDir); rerr != nil {
				slog.WarnContext(ctx, "Failed cleaning up wrapper temp directory", "dir", tempDir, "error", rerr)
			}
		}()
	}

	uploaded, err := GenerateAndUploadWrapper(ctx, t.Repository, creds, WrapperRequest{
		Chart:          chartBlob,
		Images:         images,
		ChartReference: chartImage.ImageReference,
		Annotations:    transformation.Spec.Annotations,
		TempDir:        tempDir,
	})
	if err != nil {
		return nil, err
	}

	v2Resource, err := descriptor.ConvertToV2Resource(t.Scheme, uploaded)
	if err != nil {
		return nil, fmt.Errorf("failed converting uploaded wrapper resource to v2 format: %w", err)
	}
	transformation.Output = &v1alpha1.GenerateHelmWrapperOutput{Resource: v2Resource}

	slog.InfoContext(ctx, "Uploaded localized wrapper chart", "resource", uploaded.Name, "version", uploaded.Version)

	return &transformation, nil
}

// imageMappingFromPair converts an image resource pair into an ImageMapping. Ignores localBlobs.
func imageMappingFromPair(ctx context.Context, pair v1alpha1.ImagePair) (localize.ImageMapping, bool, error) {
	if pair.Source == nil || pair.Source.Access == nil || pair.Target == nil || pair.Target.Access == nil {
		slog.WarnContext(ctx, "no source or target access, skipping")
		return localize.ImageMapping{}, false, nil
	}

	source, err := ociImageFromAccess(pair.Source.Access)
	if err != nil {
		slog.WarnContext(ctx, "skipping non-OCI source access", "resource", pair.Source.Name, "error", err)
		return localize.ImageMapping{}, false, nil
	}
	target, err := ociImageFromAccess(pair.Target.Access)
	if err != nil {
		slog.WarnContext(ctx, "skipping non-OCI target access", "resource", pair.Target.Name, "error", err)
		return localize.ImageMapping{}, false, nil
	}

	mapping, err := localize.NewImageMapping(source, target)
	if err != nil {
		return localize.ImageMapping{}, false, fmt.Errorf("failed creating image mapping for resource %q: %w", pair.Target.Name, err)
	}
	return mapping, true, nil
}

// ociImageFromAccess decodes an access specification into an OCIImage.
// TODO: This will skip local blobs for now.
func ociImageFromAccess(access runtime.Typed) (*ociaccess.OCIImage, error) {
	t := access.GetType()
	obj, err := ocischeme.Scheme.NewObject(t)
	if err != nil {
		return nil, fmt.Errorf("error creating new object for type %s: %w", t, err)
	}
	if err := ocischeme.Scheme.Convert(access, obj); err != nil {
		return nil, fmt.Errorf("error converting access to object of type %s: %w", t, err)
	}
	img, ok := obj.(*ociaccess.OCIImage)
	if !ok {
		return nil, fmt.Errorf("access type %s is not an OCI image", t)
	}
	return img, nil
}

// getTargetChartRepository gets the OCI repository directory the wrapper is
// uploaded to.
func getTargetChartRepository(imageReference, chartName string) (string, error) {
	ref, err := looseref.ParseReference(imageReference)
	if err != nil {
		return "", fmt.Errorf("parse chart reference: %w", err)
	}

	if base := path.Base(ref.Repository); base != chartName {
		return "", fmt.Errorf("chart reference repository %q does not end in chart name %q", ref.Repository, chartName)
	}

	dir := path.Dir(ref.Repository)
	if dir == "." {
		dir = ""
	}
	target := path.Join(ref.Registry, dir)
	if target == "" {
		return "", fmt.Errorf("chart reference has no repository directory")
	}
	return target, nil
}

// fileBlobFromURI opens the file referenced by a file access spec URI as a blob.
func fileBlobFromURI(uri string) (blob.ReadOnlyBlob, error) {
	filePath, err := filesystem.FilePathFromURI(uri)
	if err != nil {
		return nil, err
	}
	return filesystem.GetBlobFromOSPath(filePath)
}
