package localization_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/localization"
	ocifunctions "ocm.software/open-component-model/bindings/go/oci/cel/functions"
	"ocm.software/open-component-model/bindings/go/runtime"
)

const signedDigest = "sha256:262578cde928d5c9eba3bce079976444f624c13ed0afb741d90d5423877496cb"

func resourceWithLabel(label descriptor.Label) *descriptor.Resource {
	return &descriptor.Resource{
		ElementMeta: descriptor.ElementMeta{
			ObjectMeta: descriptor.ObjectMeta{Name: "chart", Labels: []descriptor.Label{label}},
		},
	}
}

func TestRulesFor(t *testing.T) {
	tests := []struct {
		name  string
		label descriptor.Label
		want  []localization.Rule
		err   string
	}{
		{
			name: "valid rules",
			label: descriptor.Label{
				Name: localization.LabelName, Version: localization.LabelVersion, Signing: true,
				Value: []byte(`[{"resource":{"name":"image"},"values":[{"path":"image.repository","from":"image"},{"path":"image.tag","from":"reference"}]}]`),
			},
			want: []localization.Rule{{
				Resource: runtime.Identity{"name": "image"},
				Values: []localization.Value{
					{Path: "image.repository", From: localization.PartImage},
					{Path: "image.tag", From: localization.PartReference},
				},
			}},
		},
		{
			name: "unsigned label",
			label: descriptor.Label{
				Name: localization.LabelName, Version: localization.LabelVersion,
				Value: []byte(`[{"resource":{"name":"image"},"values":[{"path":"image","from":"image"}]}]`),
			},
			err: "signing: true",
		},
		{
			name: "unsupported version",
			label: descriptor.Label{
				Name: localization.LabelName, Version: "v2", Signing: true,
				Value: []byte(`[{"resource":{"name":"image"},"values":[{"path":"image","from":"image"}]}]`),
			},
			err: "only \"v1\" is supported",
		},
		{
			name: "unknown part",
			label: descriptor.Label{
				Name: localization.LabelName, Version: localization.LabelVersion, Signing: true,
				Value: []byte(`[{"resource":{"name":"image"},"values":[{"path":"image","from":"host"}]}]`),
			},
			err: "unknown part",
		},
		{
			name: "unknown field",
			label: descriptor.Label{
				Name: localization.LabelName, Version: localization.LabelVersion, Signing: true,
				Value: []byte(`[{"resource":{"name":"image"},"value":[{"path":"image","from":"image"}]}]`),
			},
			err: "unknown field",
		},
		{
			name: "invalid path",
			label: descriptor.Label{
				Name: localization.LabelName, Version: localization.LabelVersion, Signing: true,
				Value: []byte(`[{"resource":{"name":"image"},"values":[{"path":"image..tag","from":"tag"}]}]`),
			},
			err: "invalid path",
		},
		{
			name: "path written twice",
			label: descriptor.Label{
				Name: localization.LabelName, Version: localization.LabelVersion, Signing: true,
				Value: []byte(`[{"resource":{"name":"a"},"values":[{"path":"image","from":"image"}]},{"resource":{"name":"b"},"values":[{"path":"image","from":"image"}]}]`),
			},
			err: "more than one rule",
		},
		{
			name:  "no label",
			label: descriptor.Label{Name: "other", Value: []byte(`{}`)},
			err:   "no localization rules",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			rules, err := localization.RulesFor(resourceWithLabel(tc.label))
			if tc.err != "" {
				r.ErrorContains(err, tc.err)
				return
			}
			r.NoError(err)
			r.Equal(tc.want, rules)
		})
	}
}

func TestPin(t *testing.T) {
	ociDigest := &descriptor.Digest{
		HashAlgorithm:          "SHA-256",
		NormalisationAlgorithm: descriptor.OCIArtifactDigestV1,
		Value:                  "262578cde928d5c9eba3bce079976444f624c13ed0afb741d90d5423877496cb",
	}

	t.Run("pins a tag-only reference to the signed digest", func(t *testing.T) {
		r := require.New(t)
		ref, err := localization.Pin(ocifunctions.Reference{Host: "ghcr.io", Repository: "a/b", Tag: "1.0"}, ociDigest)
		r.NoError(err)
		r.Equal(signedDigest, ref.Digest)
		r.Equal("1.0", ref.Tag)
	})

	t.Run("rejects an access digest that differs from the signed one", func(t *testing.T) {
		r := require.New(t)
		_, err := localization.Pin(ocifunctions.Reference{
			Repository: "a/b",
			Digest:     "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		}, ociDigest)
		r.ErrorContains(err, "does not match")
	})

	t.Run("leaves non-OCI digests alone", func(t *testing.T) {
		r := require.New(t)
		ref, err := localization.Pin(ocifunctions.Reference{Repository: "a/b", Tag: "1.0"}, &descriptor.Digest{
			HashAlgorithm:          "SHA-256",
			NormalisationAlgorithm: descriptor.GenericBlobDigestV1,
			Value:                  "262578cde928d5c9eba3bce079976444f624c13ed0afb741d90d5423877496cb",
		})
		r.NoError(err)
		r.Empty(ref.Digest)
	})
}

func TestApply(t *testing.T) {
	ref := ocifunctions.Reference{Host: "registry.example.com", Repository: "mirror/podinfo", Tag: "6.9.1", Digest: signedDigest}

	t.Run("writes every part into nested paths", func(t *testing.T) {
		r := require.New(t)
		values := map[string]any{}
		r.NoError(localization.Apply(values, localization.Rule{
			Resource: runtime.Identity{"name": "image"},
			Values: []localization.Value{
				{Path: "image.registry", From: localization.PartRegistry},
				{Path: "image.repository", From: localization.PartRepository},
				{Path: "image.tag", From: localization.PartTag},
				{Path: "image.digest", From: localization.PartDigest},
				{Path: "podinfo.image", From: localization.PartImage},
				{Path: "podinfo.tag", From: localization.PartReference},
				{Path: "sidecar", From: localization.PartImageReference},
			},
		}, ref))
		r.Equal(map[string]any{
			"image": map[string]any{
				"registry":   "registry.example.com",
				"repository": "mirror/podinfo",
				"tag":        "6.9.1",
				"digest":     signedDigest,
			},
			"podinfo": map[string]any{
				"image": "registry.example.com/mirror/podinfo",
				"tag":   "6.9.1@" + signedDigest,
			},
			"sidecar": "registry.example.com/mirror/podinfo:6.9.1@" + signedDigest,
		}, values)
	})

	t.Run("fails when a path runs through a value", func(t *testing.T) {
		r := require.New(t)
		values := map[string]any{"image": "already-set"}
		err := localization.Apply(values, localization.Rule{
			Values: []localization.Value{{Path: "image.tag", From: localization.PartTag}},
		}, ref)
		r.ErrorContains(err, "already set")
	})
}
