package localize

import (
	"fmt"

	"github.com/opencontainers/go-digest"

	"ocm.software/open-component-model/bindings/go/oci/looseref"
	accessv1 "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
)

// NewImageMapping builds an ImageMapping from a source and a target OCIImage.
func NewImageMapping(source, target *accessv1.OCIImage) (ImageMapping, error) {
	ref, err := looseref.ParseReference(target.ImageReference)
	if err != nil {
		return ImageMapping{}, fmt.Errorf("parse target reference %q: %w", target.ImageReference, err)
	}

	repository := ref.Repository
	if ref.Registry != "" {
		repository = ref.Registry + "/" + ref.Repository
	}

	var dgst string
	if _, err := digest.Parse(ref.Reference.Reference); err == nil {
		dgst = ref.Reference.Reference
	}

	return ImageMapping{
		Source:           source.ImageReference,
		TargetRepository: repository,
		Digest:           dgst,
	}, nil
}
