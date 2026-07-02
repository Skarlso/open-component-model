package localize_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"ocm.software/open-component-model/bindings/go/helm/localize"
)

func loadValues(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &values))
	return values
}

func TestScan(t *testing.T) {
	tests := []struct {
		name     string
		values   string
		mappings []localize.ImageMapping
		want     []localize.Localization
	}{
		{
			name: "matches top-level and nested image blocks",
			values: `
image:
  registry: ghcr.io
  repository: app/main
  tag: 1.0.0
sidecar:
  image:
    repository: app/side
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:aaa"},
				{Source: "ghcr.io/app/side:2.0.0", TargetRepository: "target.io/app/side", Digest: "sha256:bbb"},
			},
			want: []localize.Localization{
				{Path: []string{"image"}, Repository: "target.io/app/main", Digest: "sha256:aaa"},
				{Path: []string{"sidecar", "image"}, Repository: "target.io/app/side", Digest: "sha256:bbb"},
			},
		},
		{
			name: "host embedded in repository field matches (podinfo style)",
			values: `
image:
  repository: ghcr.io/app/main
  tag: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:aaa"},
			},
			want: []localize.Localization{
				{Path: []string{"image"}, Repository: "target.io/app/main", Digest: "sha256:aaa"},
			},
		},
		{
			name: "host embedded in repository with conflicting registry key does not match",
			values: `
image:
  registry: other.io
  repository: ghcr.io/app/main
  tag: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:aaa"},
			},
			want: nil,
		},
		{
			name: "host embedded in repository with wrong host does not match",
			values: `
image:
  repository: other.io/app/main
  tag: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:aaa"},
			},
			want: nil,
		},
		{
			name: "mapping without digest is skipped",
			values: `
image:
  repository: app/main
  tag: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: ""},
			},
			want: nil,
		},
		{
			name: "no match leaves values untouched",
			values: `
image:
  repository: app/main
  tag: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/other:1.0.0", TargetRepository: "target.io/app/other", Digest: "sha256:ccc"},
			},
			want: nil,
		},
		{
			name: "tag mismatch does not match",
			values: `
image:
  repository: app/main
  tag: 9.9.9
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:ddd"},
			},
			want: nil,
		},
		{
			name: "version field is an accepted tag alias",
			values: `
image:
  repository: app/main
  version: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:eee"},
			},
			want: []localize.Localization{
				{Path: []string{"image"}, Repository: "target.io/app/main", Digest: "sha256:eee"},
			},
		},
		{
			name: "registry omitted in values still matches on repository and tag",
			values: `
image:
  repository: app/main
  tag: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:fff"},
			},
			want: []localize.Localization{
				{Path: []string{"image"}, Repository: "target.io/app/main", Digest: "sha256:fff"},
			},
		},
		{
			name: "top-level values node is itself an image block",
			values: `
repository: app/main
tag: 1.0.0
`,
			mappings: []localize.ImageMapping{
				{Source: "ghcr.io/app/main:1.0.0", TargetRepository: "target.io/app/main", Digest: "sha256:root"},
			},
			want: []localize.Localization{
				{Path: []string{}, Repository: "target.io/app/main", Digest: "sha256:root"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var values map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(tt.values), &values))
			got := localize.Scan(values, tt.mappings)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCreateWrapper_Golden(t *testing.T) {
	values := loadValues(t, "podinfo-values.yaml")
	mappings := []localize.ImageMapping{
		{
			Source:           "ghcr.io/stefanprodan/podinfo:6.11.1",
			TargetRepository: "target.registry/library/podinfo",
			Digest:           "sha256:abc123",
		},
		{
			Source:           "ghcr.io/stefanprodan/podinfo-sidecar:1.2.3",
			TargetRepository: "target.registry/library/podinfo-sidecar",
			Digest:           "sha256:def456",
		},
	}

	locs := localize.Scan(values, mappings)
	require.Len(t, locs, 2)

	wrapper, err := localize.CreateHelmWrapper(localize.WrapperMeta{
		ChartName:    "podinfo",
		ChartVersion: "6.11.1",
		Dependency:   "oci://target.registry/charts",
	}, locs)
	require.NoError(t, err)

	assertGolden(t, "golden/Chart.yaml", wrapper.ChartYAML)
	assertGolden(t, "golden/values.yaml", wrapper.ValuesYAML)
}

func TestCreateWrapper_Deterministic(t *testing.T) {
	locs := []localize.Localization{
		{Path: []string{"sidecar", "image"}, Repository: "t/side", Digest: "sha256:2"},
		{Path: []string{"image"}, Repository: "t/main", Digest: "sha256:1"},
	}
	meta := localize.WrapperMeta{ChartName: "c", ChartVersion: "1.0.0", Dependency: "oci://t/charts"}

	first, err := localize.CreateHelmWrapper(meta, locs)
	require.NoError(t, err)
	second, err := localize.CreateHelmWrapper(meta, locs)
	require.NoError(t, err)

	assert.Equal(t, string(first.ChartYAML), string(second.ChartYAML))
	assert.Equal(t, string(first.ValuesYAML), string(second.ValuesYAML))
}

func TestCreateWrapper_RequiresNameAndVersion(t *testing.T) {
	_, err := localize.CreateHelmWrapper(localize.WrapperMeta{ChartName: "c"}, nil)
	assert.Error(t, err)
}

func TestCreateWrapper_RootAndNestedCoexist(t *testing.T) {
	locs := []localize.Localization{
		{Path: []string{"sidecar", "image"}, Repository: "t/side", Digest: "sha256:s"},
		{Path: []string{}, Repository: "t/root", Digest: "sha256:r"},
	}
	meta := localize.WrapperMeta{ChartName: "c", ChartVersion: "1.0.0", Dependency: "oci://t/charts"}

	wrapper, err := localize.CreateHelmWrapper(meta, locs)
	require.NoError(t, err)

	alias, ok := wrapper.Values["c"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "t/root", alias["repository"])
	assert.Equal(t, "sha256:r", alias["digest"])

	sidecar, ok := alias["sidecar"].(map[string]any)
	require.True(t, ok)
	image, ok := sidecar["image"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "t/side", image["repository"])
	assert.Equal(t, "sha256:s", image["digest"])
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err, "missing golden file %s; update it by hand", name)
	// string here so if there is a diff, it's human readable and not just numbers.
	assert.Equal(t, string(want), string(got))
}
