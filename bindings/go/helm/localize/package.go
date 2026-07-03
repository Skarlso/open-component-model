package localize

import (
	"context"
	"fmt"

	ociImageSpecV1 "github.com/opencontainers/image-spec/specs-go/v1"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"

	"ocm.software/open-component-model/bindings/go/blob/filesystem"
	"ocm.software/open-component-model/bindings/go/helm/internal"
	"ocm.software/open-component-model/bindings/go/helm/internal/oci"
)

// PackagedWrapper is a wrapper chart serialized as an OCI image layout.
type PackagedWrapper struct {
	// Layout is the tar+gzip holding the wrapper chart.
	Layout *filesystem.Blob
	// Descriptor is the wrapper's OCI manifest.
	Descriptor *ociImageSpecV1.Descriptor
	// ChartArchive is the packaged Helm chart .tgz.
	ChartArchive *filesystem.Blob
}

// Package serializes a wrapper chart into a Helm .tgz and then into an OCI image
// layout. tmpDir is passed in so the location remains configurable by .ocmconfig.
// We vendor the chart here to ensure that install works since you can't resolve
// or vendor at install time. This is okay here from a security standpoint because
// later when sign this artifact it will sign not the reference but the actual chart
// bytes so we'll always know that the wrapper is correct.
// TODO: Annotations are used for backtracking for the component version for now.
// TODO: Not going to deal with frigging Chart.lock file rn. :D
func Package(ctx context.Context, wrapper *Wrapper, originalChart []byte, tmpDir string, annotations map[string]string) (*PackagedWrapper, error) {
	if len(originalChart) == 0 {
		return nil, fmt.Errorf("original chart archive is required to vendor the wrapper dependency")
	}
	if len(wrapper.Metadata.Dependencies) == 0 {
		return nil, fmt.Errorf("wrapper metadata carries no dependency to vendor")
	}
	dep := wrapper.Metadata.Dependencies[0]

	chart := &chartv2.Chart{
		Metadata: wrapper.Metadata,
		Raw: []*chartcommon.File{
			{
				Name: chartutil.ValuesfileName,
				Data: wrapper.ValuesYAML,
			},
		},
		Files: []*chartcommon.File{
			{
				Name: fmt.Sprintf("charts/%s-%s.tgz", dep.Name, dep.Version),
				Data: originalChart,
			},
		},
		Values: wrapper.Values,
	}

	// largely lifted from input/blob.go
	archivePath, err := chartutil.Save(chart, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("save wrapper chart: %w", err)
	}

	archive, err := filesystem.GetBlobFromOSPath(archivePath)
	if err != nil {
		return nil, fmt.Errorf("read wrapper chart archive: %w", err)
	}

	result, err := oci.CopyChartToOCILayout(ctx, &internal.ChartData{
		Name:      wrapper.Metadata.Name,
		Version:   wrapper.Metadata.Version,
		ChartBlob: archive,
	}, tmpDir, annotations)
	if err != nil {
		return nil, fmt.Errorf("convert wrapper chart to OCI layout: %w", err)
	}

	return &PackagedWrapper{
		Layout:       result.Blob,
		Descriptor:   result.Desc,
		ChartArchive: archive,
	}, nil
}
