package transformation

import (
	"context"
	"errors"
	"fmt"

	"helm.sh/helm/v4/pkg/chart/v2/loader"

	"ocm.software/open-component-model/bindings/go/blob"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"ocm.software/open-component-model/bindings/go/helm/localize"
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
	// TargetRepository is the OCI repository directory the wrapper and the
	// original chart live in. For example `"target.registry/charts"`.
	// TODO: Arguably could be named better, I couldn't yet think of anything.
	TargetRepository string
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

	wrapper, err := localize.CreateHelmWrapper(localize.WrapperMeta{
		ChartName:    chart.Name(),
		ChartVersion: chart.Metadata.Version,
		Dependency:   "oci://" + req.TargetRepository,
	}, localize.Scan(chart.Values, req.Images))
	if err != nil {
		return nil, fmt.Errorf("creating wrapper: %w", err)
	}

	pkg, err := localize.Package(ctx, wrapper, req.TempDir, req.Annotations)
	if err != nil {
		return nil, fmt.Errorf("packaging wrapper: %w", err)
	}

	targetRef := fmt.Sprintf("%s/%s:%s", req.TargetRepository, wrapper.Metadata.Name, wrapper.Metadata.Version)

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
