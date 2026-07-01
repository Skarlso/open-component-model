package localize_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/chart/v2/loader"

	"ocm.software/open-component-model/bindings/go/helm/localize"
)

func TestPackage(t *testing.T) {
	wrapper, err := localize.CreateHelmWrapper(localize.WrapperMeta{
		ChartName:    "podinfo",
		ChartVersion: "6.11.1",
		Dependency:   "oci://target.registry/charts",
	}, []localize.Localization{
		{Path: []string{"image"}, Repository: "target.registry/library/podinfo", Digest: "sha256:abc123"},
	})
	require.NoError(t, err)

	annotations := map[string]string{
		"software.ocm/component":  "github.com/acme/podinfo:1.2.3",
		"software.ocm/repository": "oci://source.registry",
	}

	pkg, err := localize.Package(context.Background(), wrapper, t.TempDir(), annotations)
	require.NoError(t, err)
	require.NotNil(t, pkg.Layout)
	require.NotNil(t, pkg.Descriptor)
	require.NotNil(t, pkg.ChartArchive)

	// Provenance annotations must ride on the OCI manifest.
	assert.Equal(t, "github.com/acme/podinfo:1.2.3", pkg.Descriptor.Annotations["software.ocm/component"])
	assert.Equal(t, "oci://source.registry", pkg.Descriptor.Annotations["software.ocm/repository"])

	rc, err := pkg.ChartArchive.ReadCloser()
	require.NoError(t, err)
	defer rc.Close()

	loaded, err := loader.LoadArchive(rc)
	require.NoError(t, err)

	assert.Equal(t, "podinfo-localized", loaded.Name())
	assert.Equal(t, "6.11.1-localized.1", loaded.Metadata.Version)

	require.Len(t, loaded.Metadata.Dependencies, 1)
	assert.Equal(t, "podinfo", loaded.Metadata.Dependencies[0].Name)
	assert.Equal(t, "oci://target.registry/charts", loaded.Metadata.Dependencies[0].Repository)

	podinfo, ok := loaded.Values["podinfo"].(map[string]any)
	require.True(t, ok)
	image, ok := podinfo["image"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "target.registry/library/podinfo", image["repository"])
	assert.Equal(t, "sha256:abc123", image["digest"])
}
