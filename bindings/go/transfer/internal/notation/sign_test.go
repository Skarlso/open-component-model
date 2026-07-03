package notation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	coresignature "github.com/notaryproject/notation-core-go/signature"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeNotationRepo struct {
	resolved          ocispec.Descriptor
	resolvedRef       string
	pushedMediaType   string
	pushedBlob        []byte
	pushedSubject     ocispec.Descriptor
	pushedAnnotations map[string]string
}

func (f *fakeNotationRepo) Resolve(_ context.Context, reference string) (ocispec.Descriptor, error) {
	f.resolvedRef = reference
	return f.resolved, nil
}

func (f *fakeNotationRepo) ListSignatures(_ context.Context, _ ocispec.Descriptor, _ func([]ocispec.Descriptor) error) error {
	return nil
}

func (f *fakeNotationRepo) FetchSignatureBlob(_ context.Context, _ ocispec.Descriptor) ([]byte, ocispec.Descriptor, error) {
	return nil, ocispec.Descriptor{}, errors.New("not implemented")
}

func (f *fakeNotationRepo) PushSignature(_ context.Context, mediaType string, blob []byte, subject ocispec.Descriptor, annotations map[string]string) (ocispec.Descriptor, ocispec.Descriptor, error) {
	f.pushedMediaType = mediaType
	f.pushedBlob = blob
	f.pushedSubject = subject
	f.pushedAnnotations = annotations
	blobDesc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("signature-manifest"),
		Size:      123,
	}
	return blobDesc, manifestDesc, nil
}

func generateCertChain(t *testing.T) (chainPEM, keyPEM []byte) {
	t.Helper()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootKey.Public(), rootKey)
	require.NoError(t, err)
	rootCert, err := x509.ParseCertificate(rootDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "test-signer"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootCert, leafKey.Public(), rootKey)
	require.NoError(t, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	require.NoError(t, err)

	chainPEM = append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})...,
	)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return chainPEM, keyPEM
}

func TestSignWithRepository(t *testing.T) {
	chainPEM, keyPEM := generateCertChain(t)

	artifact := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("wrapper-manifest"),
		Size:      42,
	}
	repo := &fakeNotationRepo{resolved: artifact}

	ref := "registry.example.com/charts/podinfo-localized@" + artifact.Digest.String()
	desc, err := SignWithRepository(t.Context(), repo, ref, Request{
		CertificateChainPEM: chainPEM,
		PrivateKeyPEM:       keyPEM,
	})
	require.NoError(t, err)

	assert.Equal(t, artifact.Digest, desc.Digest)
	assert.Equal(t, artifact.Digest.String(), repo.resolvedRef)
	assert.Equal(t, artifact.Digest, repo.pushedSubject.Digest)
	assert.Equal(t, "application/jose+json", repo.pushedMediaType)

	envelope, err := coresignature.ParseEnvelope(repo.pushedMediaType, repo.pushedBlob)
	require.NoError(t, err)
	content, err := envelope.Verify()
	require.NoError(t, err)

	var payload struct {
		TargetArtifact ocispec.Descriptor `json:"targetArtifact"`
	}
	require.NoError(t, json.Unmarshal(content.Payload.Content, &payload))
	assert.Equal(t, artifact.Digest, payload.TargetArtifact.Digest)

	require.NotEmpty(t, content.SignerInfo.CertificateChain)
	assert.Equal(t, "test-signer", content.SignerInfo.CertificateChain[0].Subject.CommonName)
}

func TestSignWithRepositoryInvalidKeyMaterial(t *testing.T) {
	chainPEM, keyPEM := generateCertChain(t)
	repo := &fakeNotationRepo{}

	_, err := SignWithRepository(t.Context(), repo, "registry.example.com/foo:v1", Request{
		CertificateChainPEM: chainPEM,
		PrivateKeyPEM:       []byte("not a key"),
	})
	assert.ErrorContains(t, err, "private key")

	_, err = SignWithRepository(t.Context(), repo, "registry.example.com/foo:v1", Request{
		CertificateChainPEM: []byte("not a cert"),
		PrivateKeyPEM:       keyPEM,
	})
	assert.ErrorContains(t, err, "certificate")
}

func TestSplitScheme(t *testing.T) {
	for _, tc := range []struct {
		in        string
		ref       string
		plainHTTP bool
	}{
		{"http://localhost:5001/charts/foo:v1", "localhost:5001/charts/foo:v1", true},
		{"https://ghcr.io/charts/foo:v1", "ghcr.io/charts/foo:v1", false},
		{"ghcr.io/charts/foo:v1", "ghcr.io/charts/foo:v1", false},
	} {
		ref, plainHTTP := splitScheme(tc.in)
		assert.Equal(t, tc.ref, ref)
		assert.Equal(t, tc.plainHTTP, plainHTTP)
	}
}
