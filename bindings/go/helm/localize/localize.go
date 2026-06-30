package localize

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"sigs.k8s.io/yaml"

	"ocm.software/open-component-model/bindings/go/oci/looseref"
)

// A repository field marks a values node as an image block (Renovate's
// helm-values convention); registry, tag and version refine the reference.
const (
	keyRepository = "repository"
	keyRegistry   = "registry"
	keyTag        = "tag"
	keyVersion    = "version"
	keyDigest     = "digest"
)

// ImageMapping links an image as it appears in the source chart to where it
// lives after transfer.
type ImageMapping struct {
	// Source is the image reference.
	// For example: ghcr.io/stefanprodan/podinfo:6.11.1.
	Source string
	// TargetRepository is the localized repository.
	TargetRepository string
	// Digest pins the localized image content.
	Digest string
}

// Localization is a single override to apply under the dependency alias in the
// wrapper's values.yaml.
type Localization struct {
	Path       []string
	Repository string
	Digest     string
}

// Scan is the main entry point for starting a lookup in the values file for
// possible replacement locations.
// TODO: Mappings will be constructed from image resources. Source is the image ref
// and target repo and digest will come from the access spec that will have the
// new location of the resource.
func Scan(values map[string]any, mappings []ImageMapping) []Localization {
	// Parse each source reference once. A mapping without a digest cannot pin an
	// image, so it is dropped here rather than re-checked at every node.
	parsed := make([]parsedMapping, 0, len(mappings))
	for _, m := range mappings {
		if m.Digest == "" {
			continue
		}
		source, err := looseref.ParseReference(m.Source)
		if err != nil {
			continue
		}
		parsed = append(parsed, parsedMapping{
			source:           source,
			targetRepository: m.TargetRepository,
			digest:           m.Digest,
		})
	}

	var locs []Localization
	walk(values, nil, parsed, &locs)
	// sort for determinism.
	sort.Slice(locs, func(i, j int) bool {
		return slices.Compare(locs[i].Path, locs[j].Path) < 0
	})
	return locs
}

// parsedMapping is an ImageMapping with its source reference parsed once, ready
// to be compared against many image blocks during the walk.
type parsedMapping struct {
	source           looseref.LooseReference
	targetRepository string
	digest           string
}

// walk is a basic dfs algo to traverse all possible locations for rewriting.
func walk(node map[string]any, path []string, mappings []parsedMapping, out *[]Localization) {
	if loc, ok := matchImageBlock(node, path, mappings); ok {
		*out = append(*out, loc) // append to out slice that keeps track of the dfs list
	}

	for _, k := range slices.Sorted(maps.Keys(node)) {
		if child, ok := node[k].(map[string]any); ok {
			walk(child, append(append([]string{}, path...), k), mappings, out)
		}
	}
}

// matchImageBlock reads the image fields from a values map node and, if they
// resolve to a digest-pinned mapping, returns the override to apply.
func matchImageBlock(node map[string]any, path []string, mappings []parsedMapping) (Localization, bool) {
	// check to see if this node is an image block
	repository, ok := node[keyRepository].(string)
	if !ok || repository == "" {
		return Localization{}, false
	}
	registry, _ := node[keyRegistry].(string)
	tag := stringField(node, keyTag, keyVersion)

	for _, m := range mappings {
		if !resolvesToResource(registry, repository, tag, m.source) {
			continue
		}
		return Localization{
			Path:       append([]string{}, path...),
			Repository: m.targetRepository,
			Digest:     m.digest,
		}, true
	}
	return Localization{}, false
}

func stringField(node map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := node[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// resolvesToResource checks whether the image fields from a values block points
// at the same image as a transferred resource.
// TODO: this might trip with multiple values of the same like app/main:1.0.0 and app/main:2.0.0
// Unlikely, but still.. should probably be more intelligent here.
func resolvesToResource(registry, repository, tag string, s looseref.LooseReference) bool {
	if repository != s.Repository {
		return false
	}
	if registry != "" && s.Registry != "" && registry != s.Registry {
		return false
	}
	if tag != "" && s.Tag != "" && tag != s.Tag {
		return false
	}
	return true
}

// WrapperMeta contains the original chart identity and the OCI location it was
// pushed to and the pinned dependency.
type WrapperMeta struct {
	// ChartName name of the original chart.
	ChartName string
	// ChartVersion version of the original chart.
	ChartVersion string
	// Dependency is the OCI repository directory holding the original chart,
	// exp: "oci://target.registry/charts".
	Dependency string
	// Alias is the subchart alias that will be overridden.
	Alias string
}

// Wrapper is the generated wrapper chart, both as structured Helm types and as
// deterministic serialized bytes ready to be packaged.
type Wrapper struct {
	Metadata   *chartv2.Metadata
	Values     map[string]any
	ChartYAML  []byte
	ValuesYAML []byte
}

// CreateHelmWrapper builds the wrapper chart for the given original chart and the
// localizations produced by Scan.
func CreateHelmWrapper(meta WrapperMeta, locs []Localization) (*Wrapper, error) {
	if meta.ChartName == "" || meta.ChartVersion == "" {
		return nil, fmt.Errorf("wrapper requires chart name and version")
	}
	alias := meta.Alias
	if alias == "" {
		alias = meta.ChartName
	}

	md := &chartv2.Metadata{
		APIVersion:  chartv2.APIVersionV2,
		Name:        meta.ChartName + "-localized",
		Version:     meta.ChartVersion + "+localized.1",
		Type:        "application",
		Description: fmt.Sprintf("Localized wrapper for %s, generated by OCM transfer.", meta.ChartName),
		Dependencies: []*chartv2.Dependency{{
			Name:       meta.ChartName,
			Version:    meta.ChartVersion,
			Repository: meta.Dependency,
			Alias:      alias,
		}},
	}

	overrides := map[string]any{}
	for _, loc := range locs {
		setPath(overrides, loc.Path, map[string]any{
			keyRepository: loc.Repository,
			keyDigest:     loc.Digest,
		})
	}
	values := map[string]any{alias: overrides}

	chartYAML, err := yaml.Marshal(md)
	if err != nil {
		return nil, fmt.Errorf("marshal Chart.yaml: %w", err)
	}
	valuesYAML, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal values.yaml: %w", err)
	}

	return &Wrapper{
		Metadata:   md,
		Values:     values,
		ChartYAML:  chartYAML,
		ValuesYAML: valuesYAML,
	}, nil
}

// setPath writes leaf at the given path within root.
func setPath(root map[string]any, path []string, leaf map[string]any) {
	if len(path) == 0 {
		maps.Insert(root, maps.All(leaf))
		return
	}
	node := root
	for _, key := range path[:len(path)-1] {
		next, ok := node[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			node[key] = next
		}
		// move pointer to next node
		node = next
	}
	node[path[len(path)-1]] = leaf
}
