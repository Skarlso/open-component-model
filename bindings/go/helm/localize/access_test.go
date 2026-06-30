package localize_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ocm.software/open-component-model/bindings/go/helm/localize"
	accessv1 "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
)

func TestNewImageMapping(t *testing.T) {
	dgst := "sha256:" + strings.Repeat("a", 64)

	t.Run("reads target repository and digest from the access spec", func(t *testing.T) {
		access := &accessv1.OCIImage{ImageReference: "target.registry/library/podinfo:6.11.1@" + dgst}

		m, err := localize.NewImageMapping("ghcr.io/stefanprodan/podinfo:6.11.1", access)
		require.NoError(t, err)
		assert.Equal(t, "ghcr.io/stefanprodan/podinfo:6.11.1", m.Source)
		assert.Equal(t, "target.registry/library/podinfo", m.TargetRepository)
		assert.Equal(t, dgst, m.Digest)
	})

	t.Run("tag-only target yields an empty digest", func(t *testing.T) {
		access := &accessv1.OCIImage{ImageReference: "target.registry/library/podinfo:6.11.1"}

		m, err := localize.NewImageMapping("ghcr.io/stefanprodan/podinfo:6.11.1", access)
		require.NoError(t, err)
		assert.Equal(t, "target.registry/library/podinfo", m.TargetRepository)
		assert.Empty(t, m.Digest)
	})

	t.Run("invalid target reference errors", func(t *testing.T) {
		access := &accessv1.OCIImage{ImageReference: "target.io/app@sha256:abc123"}

		_, err := localize.NewImageMapping("ghcr.io/app:1.0.0", access)
		require.Error(t, err)
	})
}
