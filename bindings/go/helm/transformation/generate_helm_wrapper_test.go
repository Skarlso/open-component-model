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
	filesystemaccess "ocm.software/open-component-model/bindings/go/blob/filesystem/spec/access"
	filev1alpha1 "ocm.software/open-component-model/bindings/go/blob/filesystem/spec/access/v1alpha1"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	descv2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	"ocm.software/open-component-model/bindings/go/helm/localize"
	"ocm.software/open-component-model/bindings/go/helm/transformation"
	"ocm.software/open-component-model/bindings/go/helm/transformation/spec/v1alpha1"
	ocischeme "ocm.software/open-component-model/bindings/go/oci/spec/access"
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
func sourceChart(t *testing.T, dir string) (blob.ReadOnlyBlob, string) {
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
	return b, path
}

func TestGenerateAndUploadWrapper(t *testing.T) {
	dir := t.TempDir()
	repo := &fakeResourceRepository{}

	chart, _ := sourceChart(t, dir)
	uploaded, err := transformation.GenerateAndUploadWrapper(t.Context(), repo, nil, transformation.WrapperRequest{
		Chart: chart,
		Images: []localize.ImageMapping{
			{
				Source:           "ghcr.io/stefanprodan/podinfo:6.11.1",
				TargetRepository: "target.registry/library/podinfo",
				Digest:           "sha256:abc123",
			},
		},
		ChartReference: "target.registry/charts/podinfo:6.11.1",
		Annotations:    map[string]string{"software.ocm/component": "podinfo:6.11.1"},
		TempDir:        dir,
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

func TestGenerateAndUploadWrapperChartNameMismatch(t *testing.T) {
	dir := t.TempDir()
	chart, _ := sourceChart(t, dir)

	_, err := transformation.GenerateAndUploadWrapper(t.Context(), &fakeResourceRepository{}, nil, transformation.WrapperRequest{
		Chart:          chart,
		ChartReference: "target.registry/charts/app-podinfo:6.11.1",
		TempDir:        dir,
	})
	require.ErrorContains(t, err, `does not end in chart name "podinfo"`)
}

func newWrapperTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	descv2.MustAddToScheme(scheme)
	filesystemaccess.MustAddToScheme(scheme)
	scheme.MustRegisterScheme(ocischeme.Scheme)
	scheme.MustRegisterWithAlias(&v1alpha1.GenerateHelmWrapper{}, v1alpha1.GenerateHelmWrapperV1alpha1)
	return scheme
}

func rawOCIImage(t *testing.T, ref string) *runtime.Raw {
	t.Helper()
	img := &ociaccess.OCIImage{
		Type:           runtime.Type{Name: ociaccess.LegacyType, Version: ociaccess.LegacyTypeVersion},
		ImageReference: ref,
	}
	raw := &runtime.Raw{}
	require.NoError(t, ocischeme.Scheme.Convert(img, raw))
	return raw
}

func imageResource(t *testing.T, name, version, ref string) *descv2.Resource {
	t.Helper()
	return &descv2.Resource{
		ElementMeta: descv2.ElementMeta{ObjectMeta: descv2.ObjectMeta{Name: name, Version: version}},
		Type:        "ociImage",
		Access:      rawOCIImage(t, ref),
	}
}

func TestGenerateHelmWrapper_Transform(t *testing.T) {
	dir := t.TempDir()
	repo := &fakeResourceRepository{}
	transformer := &transformation.GenerateHelmWrapper{
		Scheme:     newWrapperTestScheme(),
		Repository: repo,
	}

	_, chartPath := sourceChart(t, dir)

	targetDigest := "sha256:abababababababababababababababababababababababababababababababab"
	step := &v1alpha1.GenerateHelmWrapper{
		Type: v1alpha1.GenerateHelmWrapperV1alpha1,
		ID:   "wrapPodinfo",
		Spec: &v1alpha1.GenerateHelmWrapperSpec{
			ChartFile: filev1alpha1.File{URI: "file://" + chartPath},
			ChartResource: &descv2.Resource{
				ElementMeta: descv2.ElementMeta{ObjectMeta: descv2.ObjectMeta{Name: "podinfo", Version: "6.11.1"}},
				Type:        "helmChart",
				Access:      rawOCIImage(t, "target.registry/charts/podinfo:6.11.1"),
			},
			Images: []v1alpha1.ImagePair{
				{
					Source: imageResource(t, "image", "6.11.1", "ghcr.io/stefanprodan/podinfo:6.11.1"),
					Target: imageResource(t, "image", "6.11.1", "target.registry/library/podinfo@"+targetDigest),
				},
				{
					Source: &descv2.Resource{
						ElementMeta: descv2.ElementMeta{ObjectMeta: descv2.ObjectMeta{Name: "blob", Version: "1.0.0"}},
						Type:        "blob",
						Access: &runtime.Raw{
							Type: runtime.NewVersionedType("localBlob", "v1"),
							Data: []byte(`{"type":"localBlob/v1"}`),
						},
					},
					Target: imageResource(t, "blob", "1.0.0", "target.registry/library/blob:1.0.0"),
				},
			},
			Annotations: map[string]string{"software.ocm/component": "podinfo:6.11.1"},
			OutputPath:  dir,
		},
	}

	out, err := transformer.Transform(t.Context(), step)
	require.NoError(t, err)

	result, ok := out.(*v1alpha1.GenerateHelmWrapper)
	require.True(t, ok)
	require.NotNil(t, result.Output)
	require.NotNil(t, result.Output.Resource)
	assert.Equal(t, "podinfo-localized", result.Output.Resource.Name)
	assert.Equal(t, "6.11.1-localized.1", result.Output.Resource.Version)

	var outAccess ociaccess.OCIImage
	require.NoError(t, ocischeme.Scheme.Convert(result.Output.Resource.Access, &outAccess))
	assert.Equal(t, "target.registry/charts/podinfo-localized:6.11.1-localized.1", outAccess.ImageReference)

	require.NotNil(t, repo.uploadedResource)
	require.NotNil(t, repo.uploadedBlob)
}
