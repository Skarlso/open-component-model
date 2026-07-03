package localize_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"

	"ocm.software/open-component-model/bindings/go/helm/localize"
)

// TODO: The vendoring made this test explode. I'm sorry. Maybe find a better way to create the chart.
func originalChartArchive(t *testing.T) []byte {
	t.Helper()
	c := &chartv2.Chart{
		Metadata: &chartv2.Metadata{
			APIVersion: chartv2.APIVersionV2,
			Name:       "podinfo",
			Version:    "6.11.1",
			Type:       "application",
		},
		Raw: []*chartcommon.File{{
			Name: chartutil.ValuesfileName,
			Data: []byte("image:\n  repository: stefanprodan/podinfo\n  tag: 6.11.1\n"),
		}},
		Values: map[string]any{
			"image": map[string]any{"repository": "stefanprodan/podinfo", "tag": "6.11.1"},
		},
	}
	path, err := chartutil.Save(c, t.TempDir())
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func testWrapper(t *testing.T) *localize.Wrapper {
	t.Helper()
	wrapper, err := localize.CreateHelmWrapper(localize.WrapperMeta{
		ChartName:    "podinfo",
		ChartVersion: "6.11.1",
		Dependency:   "oci://target.registry/charts",
	}, []localize.Localization{
		{Path: []string{"image"}, Repository: "target.registry/library/podinfo", Digest: "sha256:abc123"},
	})
	require.NoError(t, err)
	return wrapper
}

func TestPackage(t *testing.T) {
	wrapper := testWrapper(t)
	original := originalChartArchive(t)

	annotations := map[string]string{
		"software.ocm/component":  "github.com/acme/podinfo:1.2.3",
		"software.ocm/repository": "oci://source.registry",
	}

	pkg, err := localize.Package(context.Background(), wrapper, original, t.TempDir(), annotations)
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

	// The vendored dependency must be parsed as a subchart so helm's
	// install-time dependency check passes without helm dependency build.
	require.Len(t, loaded.Dependencies(), 1)
	assert.Equal(t, "podinfo", loaded.Dependencies()[0].Name())
	assert.Equal(t, "6.11.1", loaded.Dependencies()[0].Metadata.Version)

	podinfo, ok := loaded.Values["podinfo"].(map[string]any)
	require.True(t, ok)
	image, ok := podinfo["image"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "target.registry/library/podinfo", image["repository"])
	assert.Equal(t, "sha256:abc123", image["digest"])

	// The vendored archive must be byte-exact: the signed wrapper carries the
	// same bytes that were transferred, not a re-serialization.
	vendored := tarEntry(t, pkg.ChartArchive, "podinfo-localized/charts/podinfo-6.11.1.tgz")
	assert.Equal(t, original, vendored)
}

func TestPackageRequiresOriginalChart(t *testing.T) {
	wrapper := testWrapper(t)

	_, err := localize.Package(context.Background(), wrapper, nil, t.TempDir(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "original chart archive is required")
}

// tarEntry reads a single entry from a gzipped tar blob.
func tarEntry(t *testing.T, archive interface {
	ReadCloser() (io.ReadCloser, error)
}, name string,
) []byte {
	t.Helper()
	rc, err := archive.ReadCloser()
	require.NoError(t, err)
	defer rc.Close()

	gz, err := gzip.NewReader(rc)
	require.NoError(t, err)
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if strings.TrimPrefix(hdr.Name, "./") == name {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			return data
		}
	}
	t.Fatalf("entry %q not found in archive", name)
	return nil
}
