package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ocm.software/open-component-model/bindings/go/credentials"
	credv1 "ocm.software/open-component-model/bindings/go/credentials/spec/config/v1"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	v2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	helmv1alpha1 "ocm.software/open-component-model/bindings/go/helm/transformation/spec/v1alpha1"
	ocischeme "ocm.software/open-component-model/bindings/go/oci/spec/access"
	ociaccessv1 "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	"ocm.software/open-component-model/bindings/go/runtime"
	"ocm.software/open-component-model/bindings/go/transfer/internal/notation"
	transferv1alpha1 "ocm.software/open-component-model/bindings/go/transfer/v1alpha1/spec"
)

type fakeCredentialResolver struct {
	registryCreds runtime.Typed
	signerCreds   runtime.Typed
	signerErr     error
	identities    []runtime.Identity
}

func (f *fakeCredentialResolver) Resolve(_ context.Context, identity runtime.Identity) (runtime.Typed, error) {
	f.identities = append(f.identities, identity)
	switch identity[runtime.IdentityAttributeType] {
	case notation.IdentityTypeNotationSignerVersioned.String():
		if f.signerErr != nil {
			return nil, f.signerErr
		}
		return f.signerCreds, nil
	default:
		if f.registryCreds == nil {
			return nil, credentials.ErrNotFound
		}
		return f.registryCreds, nil
	}
}

func directCredentials(properties map[string]string) *credv1.DirectCredentials {
	return &credv1.DirectCredentials{
		Type:       runtime.NewVersionedType(credv1.DirectCredentialsType, credv1.Version),
		Properties: properties,
	}
}

func signTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.MustRegisterWithAlias(&SignOCIArtifactTransformation{}, SignOCIArtifactVersionedType)
	return scheme
}

func wrapperResource(t *testing.T, imageReference string, digestValue string) *v2.Resource {
	t.Helper()
	access := &ociaccessv1.OCIImage{
		Type:           runtime.Type{Name: ociaccessv1.LegacyType, Version: ociaccessv1.LegacyTypeVersion},
		ImageReference: imageReference,
	}
	raw := &runtime.Raw{}
	require.NoError(t, ocischeme.Scheme.Convert(access, raw))
	res := &v2.Resource{}
	res.Name = "podinfo-localized"
	res.Version = "6.11.1-localized.1"
	res.Access = raw
	if digestValue != "" {
		res.Digest = &v2.Digest{HashAlgorithm: "SHA-256", Value: digestValue}
	}
	return res
}

func TestSignOCIArtifact_Transform(t *testing.T) {
	digestValue := godigest.FromString("wrapper-manifest").Encoded()
	resolver := &fakeCredentialResolver{
		registryCreds: directCredentials(map[string]string{"username": "user", "password": "pass"}),
		signerCreds: directCredentials(map[string]string{
			notation.CredentialKeyCertificateChain: "chain-pem",
			notation.CredentialKeyPrivateKey:       "key-pem",
		}),
	}

	var captured notation.Request
	transformer := &SignOCIArtifact{
		Scheme:             signTestScheme(t),
		CredentialProvider: resolver,
		signFunc: func(_ context.Context, req notation.Request) (ocispec.Descriptor, error) {
			captured = req
			return ocispec.Descriptor{Digest: godigest.FromString("wrapper-manifest")}, nil
		},
	}

	transformation := &SignOCIArtifactTransformation{
		Type: SignOCIArtifactVersionedType,
		ID:   "sign",
		Spec: &SignOCIArtifactSpec{
			Resource: wrapperResource(t, "ghcr.io/target/podinfo-localized:6.11.1-localized.1", digestValue),
		},
	}

	result, err := transformer.Transform(t.Context(), transformation)
	require.NoError(t, err)

	assert.Equal(t, "ghcr.io/target/podinfo-localized:6.11.1-localized.1@sha256:"+digestValue, captured.Reference)
	assert.Equal(t, "user", captured.Credential.Username)
	assert.Equal(t, "pass", captured.Credential.Password)
	assert.Equal(t, []byte("chain-pem"), captured.CertificateChainPEM)
	assert.Equal(t, []byte("key-pem"), captured.PrivateKeyPEM)

	signed, ok := result.(*SignOCIArtifactTransformation)
	require.True(t, ok)
	require.NotNil(t, signed.Output)
	assert.Equal(t, godigest.FromString("wrapper-manifest").String(), signed.Output.Digest)
}

func TestSignOCIArtifact_TransformFileKeyMaterial(t *testing.T) {
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "chain.pem")
	keyPath := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(chainPath, []byte("chain-from-file"), 0o600))
	require.NoError(t, os.WriteFile(keyPath, []byte("key-from-file"), 0o600))

	resolver := &fakeCredentialResolver{
		signerCreds: directCredentials(map[string]string{
			notation.CredentialKeyCertificateChainFile: chainPath,
			notation.CredentialKeyPrivateKeyFile:       keyPath,
		}),
	}

	var captured notation.Request
	transformer := &SignOCIArtifact{
		Scheme:             signTestScheme(t),
		CredentialProvider: resolver,
		signFunc: func(_ context.Context, req notation.Request) (ocispec.Descriptor, error) {
			captured = req
			return ocispec.Descriptor{Digest: godigest.FromString("sig")}, nil
		},
	}

	transformation := &SignOCIArtifactTransformation{
		Type: SignOCIArtifactVersionedType,
		ID:   "sign",
		Spec: &SignOCIArtifactSpec{
			Resource: wrapperResource(t, "ghcr.io/target/podinfo-localized:6.11.1-localized.1", ""),
		},
	}

	_, err := transformer.Transform(t.Context(), transformation)
	require.NoError(t, err)
	assert.Equal(t, []byte("chain-from-file"), captured.CertificateChainPEM)
	assert.Equal(t, []byte("key-from-file"), captured.PrivateKeyPEM)
	assert.Equal(t, "ghcr.io/target/podinfo-localized:6.11.1-localized.1", captured.Reference)
	assert.Equal(t, "", captured.Credential.Username)
}

func TestSignOCIArtifact_TransformMissingSignerCredentials(t *testing.T) {
	resolver := &fakeCredentialResolver{signerErr: credentials.ErrNotFound}
	transformer := &SignOCIArtifact{
		Scheme:             signTestScheme(t),
		CredentialProvider: resolver,
	}

	transformation := &SignOCIArtifactTransformation{
		Type: SignOCIArtifactVersionedType,
		ID:   "sign",
		Spec: &SignOCIArtifactSpec{
			Resource: wrapperResource(t, "ghcr.io/target/podinfo-localized:6.11.1-localized.1", ""),
		},
	}

	_, err := transformer.Transform(t.Context(), transformation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notation signing credentials")
}

func TestSignOCIArtifact_TransformIncompleteSignerCredentials(t *testing.T) {
	resolver := &fakeCredentialResolver{
		signerCreds: directCredentials(map[string]string{
			notation.CredentialKeyCertificateChain: "chain-pem",
		}),
	}
	transformer := &SignOCIArtifact{
		Scheme:             signTestScheme(t),
		CredentialProvider: resolver,
	}

	transformation := &SignOCIArtifactTransformation{
		Type: SignOCIArtifactVersionedType,
		ID:   "sign",
		Spec: &SignOCIArtifactSpec{
			Resource: wrapperResource(t, "ghcr.io/target/podinfo-localized:6.11.1-localized.1", ""),
		},
	}

	_, err := transformer.Transform(t.Context(), transformation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must provide")
}

func TestSignOCIArtifact_TransformRequiresSpec(t *testing.T) {
	transformer := &SignOCIArtifact{Scheme: signTestScheme(t)}
	_, err := transformer.Transform(t.Context(), &SignOCIArtifactTransformation{
		Type: SignOCIArtifactVersionedType,
		ID:   "sign",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.resource")
}

func TestBuildGraphDefinition_SignWrapper(t *testing.T) {
	sourceRepo := testOCIRepo("ghcr.io/source")
	targetRepo := testOCIRepo("ghcr.io/target")
	desc := testDescriptor("ocm.software/test", "1.0.0",
		[]descriptor.Resource{
			helmResource("my-chart", "1.0.0", "https://charts.example.com", "my-chart"),
			ociImageResource("my-image", "1.0.0", "ghcr.io/org/image:v1"),
		}, nil)
	resolver := testResolverFor("ocm.software/test", "1.0.0", sourceRepo, desc)
	roots := testTransferRoots("ocm.software/test", "1.0.0", targetRepo, resolver)

	cfg := transferv1alpha1.Config{
		CopyMode:    transferv1alpha1.CopyModeAllResources,
		UploadType:  transferv1alpha1.UploadAsOciArtifact,
		Localize:    true,
		SignWrapper: true,
	}
	tgd, err := BuildGraphDefinition(t.Context(), roots, cfg)
	require.NoError(t, err)

	var wrapID string
	var signSpec map[string]any
	var signID string
	for i := range tgd.Transformations {
		switch tgd.Transformations[i].Type {
		case helmv1alpha1.GenerateHelmWrapperV1alpha1:
			wrapID = tgd.Transformations[i].ID
		case SignOCIArtifactVersionedType:
			signID = tgd.Transformations[i].ID
			signSpec = tgd.Transformations[i].Spec.Data
		}
	}
	require.NotEmpty(t, wrapID, "expected a GenerateHelmWrapper transformation in the graph")
	require.NotNil(t, signSpec, "expected a SignOCIArtifact transformation in the graph")
	assert.Contains(t, signID, "Sign")

	resource, ok := signSpec["resource"].(string)
	require.True(t, ok)
	assert.Equal(t, "${"+wrapID+".output.resource}", resource)
}

func TestBuildGraphDefinition_SignWrapperOffProducesNoSignNode(t *testing.T) {
	sourceRepo := testOCIRepo("ghcr.io/source")
	targetRepo := testOCIRepo("ghcr.io/target")
	desc := testDescriptor("ocm.software/test", "1.0.0",
		[]descriptor.Resource{helmResource("my-chart", "1.0.0", "https://charts.example.com", "my-chart")}, nil)
	resolver := testResolverFor("ocm.software/test", "1.0.0", sourceRepo, desc)
	roots := testTransferRoots("ocm.software/test", "1.0.0", targetRepo, resolver)

	cfg := transferv1alpha1.Config{
		CopyMode:   transferv1alpha1.CopyModeAllResources,
		UploadType: transferv1alpha1.UploadAsOciArtifact,
		Localize:   true,
	}
	tgd, err := BuildGraphDefinition(t.Context(), roots, cfg)
	require.NoError(t, err)

	for _, tr := range tgd.Transformations {
		assert.NotEqual(t, SignOCIArtifactVersionedType, tr.Type)
	}
}

func TestSignDigestFromResource(t *testing.T) {
	res := &v2.Resource{}
	assert.Empty(t, signDigestFromResource(res))

	res.Digest = &v2.Digest{HashAlgorithm: "SHA-512", Value: "abc"}
	assert.Empty(t, signDigestFromResource(res))

	value := godigest.FromString("x").Encoded()
	res.Digest = &v2.Digest{HashAlgorithm: "SHA-256", Value: value}
	assert.True(t, strings.HasPrefix(signDigestFromResource(res), "sha256:"))
}
