package transformation_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"

	"ocm.software/open-component-model/bindings/go/blob"
	"ocm.software/open-component-model/bindings/go/blob/filesystem"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"ocm.software/open-component-model/bindings/go/helm/localize"
	"ocm.software/open-component-model/bindings/go/helm/transformation"
	ociaccess "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	"ocm.software/open-component-model/bindings/go/repository"
	"ocm.software/open-component-model/bindings/go/runtime"
)

// fakeResourceRepository captures the upload without touching a registry.
type fakeResourceRepository struct {
	repository.ResourceRepository
	uploadedResource *descriptor.Resource
	uploadedBlob     blob.ReadOnlyBlob
	creds            runtime.Typed
}

func (f *fakeResourceRepository) UploadResource(_ context.Context, res *descriptor.Resource, content blob.ReadOnlyBlob, creds runtime.Typed) (*descriptor.Resource, error) {
	f.uploadedResource = res
	f.uploadedBlob = content
	f.creds = creds
	return res.DeepCopy(), nil
}

// sourceChart builds a minimal podinfo-style chart .tgz with a single image block.
func sourceChart(t *testing.T, dir string) blob.ReadOnlyBlob {
	t.Helper()
	values := []byte("image:\n  repository: stefanprodan/podinfo\n  tag: 6.11.1\n")
	c := &chartv2.Chart{
		Metadata: &chartv2.Metadata{
			APIVersion: chartv2.APIVersionV2,
			Name:       "podinfo",
			Version:    "6.11.1",
			Type:       "application",
		},
		Raw: []*chartcommon.File{{Name: chartutil.ValuesfileName, Data: values}},
		Values: map[string]any{
			"image": map[string]any{"repository": "stefanprodan/podinfo", "tag": "6.11.1"},
		},
	}
	path, err := chartutil.Save(c, dir)
	require.NoError(t, err)
	b, err := filesystem.GetBlobFromOSPath(path)
	require.NoError(t, err)
	return b
}

func TestGenerateAndUploadWrapper(t *testing.T) {
	dir := t.TempDir()
	repo := &fakeResourceRepository{}

	uploaded, err := transformation.GenerateAndUploadWrapper(t.Context(), repo, nil, transformation.WrapperRequest{
		Chart: sourceChart(t, dir),
		Images: []localize.ImageMapping{
			{
				Source:           "ghcr.io/stefanprodan/podinfo:6.11.1",
				TargetRepository: "target.registry/library/podinfo",
				Digest:           "sha256:abc123",
			},
		},
		TargetRepository: "target.registry/charts",
		Annotations:      map[string]string{"software.ocm/component": "podinfo:6.11.1"},
		TempDir:          dir,
	})
	require.NoError(t, err)
	require.NotNil(t, uploaded)

	require.NotNil(t, repo.uploadedResource)
	assert.Equal(t, "podinfo-localized", repo.uploadedResource.Name)
	assert.Equal(t, "6.11.1-localized.1", repo.uploadedResource.Version)

	access, ok := repo.uploadedResource.Access.(*ociaccess.OCIImage)
	require.True(t, ok)
	assert.Equal(t, "target.registry/charts/podinfo-localized:6.11.1-localized.1", access.ImageReference)

	require.NotNil(t, repo.uploadedBlob)
}
