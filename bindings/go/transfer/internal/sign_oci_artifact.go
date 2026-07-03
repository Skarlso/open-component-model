package internal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote/auth"

	"ocm.software/open-component-model/bindings/go/credentials"
	credv1 "ocm.software/open-component-model/bindings/go/credentials/spec/config/v1"
	v2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	ocicredentials "ocm.software/open-component-model/bindings/go/oci/credentials"
	"ocm.software/open-component-model/bindings/go/oci/looseref"
	ocischeme "ocm.software/open-component-model/bindings/go/oci/spec/access"
	ociaccessv1 "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	ocicredsv1 "ocm.software/open-component-model/bindings/go/oci/spec/credentials/v1"
	credidentityv1 "ocm.software/open-component-model/bindings/go/oci/spec/identity/v1"
	"ocm.software/open-component-model/bindings/go/runtime"
	"ocm.software/open-component-model/bindings/go/transfer/internal/notation"
)

const (
	SignOCIArtifactType    = "SignOCIArtifact"
	signOCIArtifactVersion = "v1alpha1"
)

// SignOCIArtifactVersionedType is the versioned type identifier for SignOCIArtifact transformations.
var SignOCIArtifactVersionedType = runtime.NewVersionedType(SignOCIArtifactType, signOCIArtifactVersion)

// SignOCIArtifactSpec is the input specification for a SignOCIArtifact transformation.
// +k8s:deepcopy-gen=true
// +ocm:jsonschema-gen=true
type SignOCIArtifactSpec struct {
	// Resource is the uploaded OCI artifact resource to sign.
	Resource *v2.Resource `json:"resource"`
	// SignatureMediaType selects the notation signature envelope format.
	SignatureMediaType string `json:"signatureMediaType,omitempty"`
}

// SignOCIArtifactOutput is the output of a SignOCIArtifact transformation.
// +k8s:deepcopy-gen=true
// +ocm:jsonschema-gen=true
type SignOCIArtifactOutput struct {
	// Digest is the digest of the signed artifact manifest.
	Digest string `json:"digest"`
}

// SignOCIArtifactTransformation is a transformation spec that attaches
// a Notary Project X.509 signature to an OCI artifact that was uploaded to the
// target registry by a previous transformation. The signature is pushed as a
// referrer of the artifact manifest.
//
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=ocm.software/open-component-model/bindings/go/runtime.Typed
// +ocm:typegen=true
// +ocm:jsonschema-gen=true
type SignOCIArtifactTransformation struct {
	// +ocm:jsonschema-gen:enum=SignOCIArtifact/v1alpha1
	Type   runtime.Type           `json:"type"`
	ID     string                 `json:"id"`
	Spec   *SignOCIArtifactSpec   `json:"spec"`
	Output *SignOCIArtifactOutput `json:"output,omitempty"`
}

var signCredentialScheme = runtime.NewScheme()

func init() {
	credv1.MustRegister(signCredentialScheme)
}

// SignOCIArtifact is the transformer.
type SignOCIArtifact struct {
	Scheme             *runtime.Scheme
	CredentialProvider credentials.Resolver
	signFunc           func(ctx context.Context, req notation.Request) (ocispec.Descriptor, error)
}

func (t *SignOCIArtifact) Transform(ctx context.Context, step runtime.Typed) (runtime.Typed, error) {
	var transformation SignOCIArtifactTransformation
	if err := t.Scheme.Convert(step, &transformation); err != nil {
		return nil, fmt.Errorf("failed converting generic transformation to sign oci artifact transformation: %w", err)
	}
	if transformation.Spec == nil || transformation.Spec.Resource == nil || transformation.Spec.Resource.Access == nil {
		return nil, fmt.Errorf("spec.resource with access is required for sign oci artifact transformation")
	}

	image, err := ociImageFromAccessSpec(transformation.Spec.Resource.Access)
	if err != nil {
		return nil, fmt.Errorf("resource access must be an OCI image: %w", err)
	}

	reference := image.ImageReference
	if !strings.Contains(reference, "@") {
		if d := signDigestFromResource(transformation.Spec.Resource); d != "" {
			reference += "@" + d
		} else {
			slog.WarnContext(ctx, "signing a tag reference: resource carries no digest", "reference", reference)
		}
	}

	registryCredential, err := t.resolveRegistryCredential(ctx, image.ImageReference)
	if err != nil {
		return nil, err
	}

	chainPEM, keyPEM, err := t.resolveSignerKeyMaterial(ctx, image.ImageReference)
	if err != nil {
		return nil, err
	}

	sign := t.signFunc
	if sign == nil {
		sign = notation.Sign
	}
	desc, err := sign(ctx, notation.Request{
		Reference:           reference,
		CertificateChainPEM: chainPEM,
		PrivateKeyPEM:       keyPEM,
		SignatureMediaType:  transformation.Spec.SignatureMediaType,
		Credential:          registryCredential,
	})
	if err != nil {
		return nil, fmt.Errorf("failed signing artifact %q: %w", reference, err)
	}

	transformation.Output = &SignOCIArtifactOutput{Digest: desc.Digest.String()}

	slog.InfoContext(ctx, "Signed OCI artifact", "reference", reference, "digest", desc.Digest.String())

	return &transformation, nil
}

// resolveRegistryCredential resolves registry credentials for the signature
// push. Missing credentials degrade to anonymous access.
func (t *SignOCIArtifact) resolveRegistryCredential(ctx context.Context, imageReference string) (auth.Credential, error) {
	if t.CredentialProvider == nil {
		return auth.EmptyCredential, nil
	}
	identity, err := registryIdentityFromReference(imageReference, credidentityv1.Type)
	if err != nil {
		return auth.EmptyCredential, err
	}
	typed, err := t.CredentialProvider.Resolve(ctx, identity)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return auth.EmptyCredential, nil
		}
		return auth.EmptyCredential, fmt.Errorf("resolving registry credentials for %q: %w", imageReference, err)
	}
	ociCreds, err := ocicredsv1.ConvertToOCICredentials(typed)
	if err != nil {
		return auth.EmptyCredential, fmt.Errorf("converting registry credentials for %q: %w", imageReference, err)
	}
	return ocicredentials.MapCredentials(ociCreds), nil
}

// resolveSignerKeyMaterial resolves the signing certificate chain and private
// key from the credential graph. Signing was explicitly requested, so missing
// key material is a hard error rather than a silent skip.
func (t *SignOCIArtifact) resolveSignerKeyMaterial(ctx context.Context, imageReference string) ([]byte, []byte, error) {
	if t.CredentialProvider == nil {
		return nil, nil, fmt.Errorf("no credential provider configured: notation signing requires %s credentials", notation.IdentityTypeNotationSigner)
	}
	identity, err := registryIdentityFromReference(imageReference, notation.IdentityTypeNotationSignerVersioned)
	if err != nil {
		return nil, nil, err
	}
	typed, err := t.CredentialProvider.Resolve(ctx, identity)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving notation signing credentials for identity %v: %w", identity, err)
	}
	direct := &credv1.DirectCredentials{}
	if err := signCredentialScheme.Convert(typed, direct); err != nil {
		return nil, nil, fmt.Errorf("converting notation signing credentials: %w", err)
	}
	chainPEM, err := credentialBytes(direct.Properties, notation.CredentialKeyCertificateChain, notation.CredentialKeyCertificateChainFile)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := credentialBytes(direct.Properties, notation.CredentialKeyPrivateKey, notation.CredentialKeyPrivateKeyFile)
	if err != nil {
		return nil, nil, err
	}
	if len(chainPEM) == 0 || len(keyPEM) == 0 {
		return nil, nil, fmt.Errorf("notation signing credentials must provide %s/%s and %s/%s",
			notation.CredentialKeyCertificateChain, notation.CredentialKeyCertificateChainFile,
			notation.CredentialKeyPrivateKey, notation.CredentialKeyPrivateKeyFile)
	}
	return chainPEM, keyPEM, nil
}

// credentialBytes reads a credential property either inline or from a file path.
func credentialBytes(properties map[string]string, inlineKey, fileKey string) ([]byte, error) {
	if v := properties[inlineKey]; v != "" {
		return []byte(v), nil
	}
	if p := properties[fileKey]; p != "" {
		content, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading credential file for %s: %w", fileKey, err)
		}
		return content, nil
	}
	return nil, nil
}

// registryIdentityFromReference derives a registry-scoped consumer identity of
// the given type from an OCI image reference.
func registryIdentityFromReference(imageReference string, typ runtime.Type) (runtime.Identity, error) {
	ref, err := looseref.ParseReference(imageReference)
	if err != nil {
		return nil, fmt.Errorf("parsing image reference %q: %w", imageReference, err)
	}
	identity, err := runtime.ParseURLToIdentity(ref.RegistryWithScheme())
	if err != nil {
		return nil, fmt.Errorf("parsing registry URL to identity: %w", err)
	}
	identity.SetType(typ)
	return identity, nil
}

// ociImageFromAccessSpec decodes an access specification into an OCIImage,
// guarding against foreign access types silently decoding into an empty spec.
// TODO: Literally the same as helm/transformation/ociImageFromAccess. Should
// probably extract this somehow.
func ociImageFromAccessSpec(access runtime.Typed) (*ociaccessv1.OCIImage, error) {
	t := access.GetType()
	obj, err := ocischeme.Scheme.NewObject(t)
	if err != nil {
		return nil, fmt.Errorf("error creating new object for type %s: %w", t, err)
	}
	if err := ocischeme.Scheme.Convert(access, obj); err != nil {
		return nil, fmt.Errorf("error converting access to object of type %s: %w", t, err)
	}
	img, ok := obj.(*ociaccessv1.OCIImage)
	if !ok {
		return nil, fmt.Errorf("access type %s is not an OCI image", t)
	}
	return img, nil
}

// TODO: Also a duplicate for now.
func signDigestFromResource(res *v2.Resource) string {
	if res.Digest == nil || res.Digest.Value == "" {
		return ""
	}
	if res.Digest.HashAlgorithm != "" && res.Digest.HashAlgorithm != "SHA-256" {
		return ""
	}
	return digest.NewDigestFromEncoded(digest.SHA256, res.Digest.Value).String()
}
