// Package localization reads the localization rules a component version signs into a
// resource, e.g. a Helm chart, and renders them into a values tree: which image resource
// goes into which values path.
package localization

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	ocifunctions "ocm.software/open-component-model/bindings/go/oci/cel/functions"
	"ocm.software/open-component-model/bindings/go/repository/integrity"
	"ocm.software/open-component-model/bindings/go/runtime"
)

const (
	// LabelName is the label that carries the localization rules of a resource.
	LabelName = "ocm.software/localization-rules"
	// LabelVersion is the label version this package reads.
	LabelVersion = "v1"
)

// ErrNoRules is returned when a resource carries no localization rules.
var ErrNoRules = errors.New("resource carries no localization rules")

// Part is a part of an image reference that a rule writes into a values path.
type Part string

const (
	// PartRegistry is the registry host, e.g. ghcr.io.
	PartRegistry Part = "registry"
	// PartRepository is the repository without the registry, e.g. stefanprodan/podinfo.
	PartRepository Part = "repository"
	// PartImage is registry and repository, e.g. ghcr.io/stefanprodan/podinfo.
	PartImage Part = "image"
	// PartTag is the tag, e.g. 6.9.1.
	PartTag Part = "tag"
	// PartDigest is the digest, e.g. sha256:262578....
	PartDigest Part = "digest"
	// PartReference is tag@digest, tag or digest, as toOCI().reference renders it.
	PartReference Part = "reference"
	// PartImageReference is the full reference, e.g. ghcr.io/stefanprodan/podinfo:6.9.1@sha256:262578....
	PartImageReference Part = "imageReference"
)

var parts = map[Part]bool{
	PartRegistry: true, PartRepository: true, PartImage: true, PartTag: true,
	PartDigest: true, PartReference: true, PartImageReference: true,
}

// Rule localizes one image resource into one or more values paths.
type Rule struct {
	// Resource identifies the image resource in the component version that carries the rules.
	Resource runtime.Identity `json:"resource"`
	// ReferencePath leads from that component version to the one holding the image resource.
	ReferencePath []runtime.Identity `json:"referencePath,omitempty"`
	// Values lists the values paths the image reference is written to.
	Values []Value `json:"values"`
}

// Value writes one part of an image reference into a values path.
type Value struct {
	// Path is a dot-separated path into the values, e.g. image.repository.
	Path string `json:"path"`
	// From is the part of the image reference written to Path.
	From Part `json:"from"`
}

// RulesFor returns the rules res carries in its [LabelName] label.
func RulesFor(res *descriptor.Resource) ([]Rule, error) {
	for _, label := range res.Labels {
		if label.Name != LabelName {
			continue
		}
		// An unsigned label would let anyone with write access to the repository redirect images.
		if !label.Signing {
			return nil, fmt.Errorf("label %s must set signing: true", LabelName)
		}
		if label.Version != LabelVersion {
			return nil, fmt.Errorf("label %s has version %q, only %q is supported", LabelName, label.Version, LabelVersion)
		}

		var rules []Rule
		decoder := json.NewDecoder(bytes.NewReader(label.Value))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&rules); err != nil {
			return nil, fmt.Errorf("decoding label %s: %w", LabelName, err)
		}
		if err := validate(rules); err != nil {
			return nil, fmt.Errorf("invalid label %s: %w", LabelName, err)
		}

		return rules, nil
	}

	return nil, fmt.Errorf("%w: resource %s has no %s label", ErrNoRules, res.Name, LabelName)
}

func validate(rules []Rule) error {
	if len(rules) == 0 {
		return errors.New("no rules")
	}
	paths := map[string]bool{}
	for i, rule := range rules {
		if rule.Resource["name"] == "" {
			return fmt.Errorf("rule %d: resource must name a resource", i)
		}
		if len(rule.Values) == 0 {
			return fmt.Errorf("rule %d: no values", i)
		}
		for _, value := range rule.Values {
			if value.Path == "" || strings.Contains(value.Path, "..") ||
				strings.HasPrefix(value.Path, ".") || strings.HasSuffix(value.Path, ".") {
				return fmt.Errorf("rule %d: invalid path %q", i, value.Path)
			}
			if !parts[value.From] {
				return fmt.Errorf("rule %d: path %s: unknown part %q", i, value.Path, value.From)
			}
			if paths[value.Path] {
				return fmt.Errorf("rule %d: path %s is written by more than one rule", i, value.Path)
			}
			paths[value.Path] = true
		}
	}

	return nil
}

// Pin sets the digest of ref to the signed digest d of the image resource, so the
// workload pulls exactly the signed image. A digest in ref that differs from d is an error.
// Digests that do not name an OCI manifest leave ref unchanged.
func Pin(ref ocifunctions.Reference, d *descriptor.Digest) (ocifunctions.Reference, error) {
	if d == nil || d.NormalisationAlgorithm != descriptor.OCIArtifactDigestV1 {
		return ref, nil
	}
	signed, err := integrity.ParseDigest(d)
	if err != nil {
		return ref, fmt.Errorf("parsing resource digest: %w", err)
	}
	if signed == "" {
		return ref, nil
	}
	if ref.Digest != "" && ref.Digest != signed.String() {
		return ref, fmt.Errorf("access digest %s does not match the signed resource digest %s", ref.Digest, signed)
	}
	ref.Digest = signed.String()

	return ref, nil
}

// Parts renders ref as the parts a rule can write.
func Parts(ref ocifunctions.Reference) map[Part]string {
	image := ref.Repository
	if ref.Host != "" {
		image = ref.Host + "/" + ref.Repository
	}
	imageReference := image
	if ref.Tag != "" {
		imageReference += ":" + ref.Tag
	}
	if ref.Digest != "" {
		imageReference += "@" + ref.Digest
	}

	return map[Part]string{
		PartRegistry:       ref.Host,
		PartRepository:     ref.Repository,
		PartImage:          image,
		PartTag:            ref.Tag,
		PartDigest:         ref.Digest,
		PartReference:      ref.Map()["reference"],
		PartImageReference: imageReference,
	}
}

// Apply writes the parts of ref that rule names into values.
func Apply(values map[string]any, rule Rule, ref ocifunctions.Reference) error {
	rendered := Parts(ref)
	for _, value := range rule.Values {
		if err := set(values, strings.Split(value.Path, "."), rendered[value.From]); err != nil {
			return fmt.Errorf("path %s: %w", value.Path, err)
		}
	}

	return nil
}

func set(values map[string]any, path []string, value string) error {
	key := path[0]
	if len(path) == 1 {
		if _, exists := values[key]; exists {
			return errors.New("already set")
		}
		values[key] = value

		return nil
	}

	next, exists := values[key]
	if !exists {
		next = map[string]any{}
		values[key] = next
	}
	nested, ok := next.(map[string]any)
	if !ok {
		return fmt.Errorf("%s is already set to a value", key)
	}

	return set(nested, path[1:], value)
}
