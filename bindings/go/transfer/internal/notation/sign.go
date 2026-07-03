// Package notation signs OCI artifacts with Notary Project X.509 signatures.
// The signature is pushed to the registry as a referrer of the signed
// artifact manifest, where verifiers such as the notation CLI or Flux
// (OCIRepository verify provider "notation") can discover it.
package notation

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/notaryproject/notation-core-go/signature/jws"
	notationgo "github.com/notaryproject/notation-go"
	notationregistry "github.com/notaryproject/notation-go/registry"
	"github.com/notaryproject/notation-go/signer"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"ocm.software/open-component-model/bindings/go/runtime"
)

const (
	// IdentityTypeNotationSigner consumer identity type.
	IdentityTypeNotationSigner = "NotationSigner"
	// IdentityTypeNotationSignerVersion version.
	IdentityTypeNotationSignerVersion = "v1alpha1"

	// CredentialKeyCertificateChain pem encoded chain.
	CredentialKeyCertificateChain = "certificateChain"
	// CredentialKeyCertificateChainFile signing certificate.
	CredentialKeyCertificateChainFile = "certificateChainFile"
	// CredentialKeyPrivateKey private key.
	CredentialKeyPrivateKey = "privateKey"
	// CredentialKeyPrivateKeyFile private key file.
	CredentialKeyPrivateKeyFile = "privateKeyFile"
)

// IdentityTypeNotationSignerVersioned type.
// TODO: Probably will need to figure out how to do this nicely or fold it into another cred type.
var IdentityTypeNotationSignerVersioned = runtime.NewVersionedType(IdentityTypeNotationSigner, IdentityTypeNotationSignerVersion)

// Request holds everything needed to sign a single OCI artifact.
type Request struct {
	// Reference is the artifact to sign.
	Reference string
	// CertificateChainPEM signing certificate chain.
	CertificateChainPEM []byte
	// PrivateKeyPEM private key.
	PrivateKeyPEM []byte
	// SignatureMediaType media type.
	SignatureMediaType string
	// Credential authentication.
	Credential auth.Credential
	// UserAgent is set on registry requests when non-empty.
	UserAgent string
}

// Sign signs the artifact at req.Reference and pushes the signature to the
// same repository. It returns the descriptor of the signed artifact manifest.
// This creates the notation signing registry and then calls SignWithRepository.
func Sign(ctx context.Context, req Request) (ocispec.Descriptor, error) {
	ref, plainHTTP := splitScheme(req.Reference)

	repo, err := remote.NewRepository(ref)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parsing artifact reference %q: %w", ref, err)
	}
	repo.PlainHTTP = plainHTTP

	client := &auth.Client{
		Client:     retry.DefaultClient,
		Credential: auth.StaticCredential(repo.Reference.Registry, req.Credential),
	}
	if req.UserAgent != "" {
		client.Header = http.Header{"User-Agent": []string{req.UserAgent}}
	}
	repo.Client = client

	return SignWithRepository(ctx, notationregistry.NewRepository(repo), ref, req)
}

// SignWithRepository does the actual signing.
func SignWithRepository(ctx context.Context, repo notationregistry.Repository, artifactReference string, req Request) (ocispec.Descriptor, error) {
	key, err := parsePrivateKey(req.PrivateKeyPEM)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parsing signing private key: %w", err)
	}
	certs, err := parseCertificates(req.CertificateChainPEM)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parsing signing certificate chain: %w", err)
	}

	notationSigner, err := signer.NewGenericSigner(key, certs)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("creating notation signer: %w", err)
	}

	mediaType := req.SignatureMediaType
	if mediaType == "" {
		mediaType = jws.MediaTypeEnvelope
	}

	desc, err := notationgo.Sign(ctx, notationSigner, repo, notationgo.SignOptions{
		SignerSignOptions: notationgo.SignerSignOptions{
			SignatureMediaType: mediaType,
		},
		ArtifactReference: artifactReference,
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("signing %q: %w", artifactReference, err)
	}
	return desc, nil
}

// splitScheme strips an explicit scheme prefix off an OCI reference. An
// http:// prefix marks the registry as plain HTTP.
func splitScheme(reference string) (ref string, plainHTTP bool) {
	switch {
	case strings.HasPrefix(reference, "http://"):
		return strings.TrimPrefix(reference, "http://"), true
	case strings.HasPrefix(reference, "https://"):
		return strings.TrimPrefix(reference, "https://"), false
	}
	return reference, false
}

func parsePrivateKey(pemBytes []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found in private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported private key format: expected PKCS#8, PKCS#1 or SEC1 PEM")
}

func parseCertificates(pemBytes []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no certificates found in chain")
	}
	return certs, nil
}
