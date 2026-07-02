package v1alpha1

import (
	"ocm.software/open-component-model/bindings/go/blob/filesystem/spec/access/v1alpha1"
	v2 "ocm.software/open-component-model/bindings/go/descriptor/v2"
	"ocm.software/open-component-model/bindings/go/runtime"
)

const GenerateHelmWrapperType = "GenerateHelmWrapper"

// GenerateHelmWrapper is a transformer spec to generate a localized wrapper chart for a transferred Helm chart.
// The wrapper is then uploaded next to the original chart in the target OCI repository.
//
// +k8s:deepcopy-gen:interfaces=ocm.software/open-component-model/bindings/go/runtime.Typed
// +k8s:deepcopy-gen=true
// +ocm:typegen=true
// +ocm:jsonschema-gen=true
type GenerateHelmWrapper struct {
	// +ocm:jsonschema-gen:enum=GenerateHelmWrapper/v1alpha1
	Type   runtime.Type               `json:"type"`
	ID     string                     `json:"id"`
	Spec   *GenerateHelmWrapperSpec   `json:"spec"`
	Output *GenerateHelmWrapperOutput `json:"output,omitempty"`
}

// GenerateHelmWrapperSpec is the input specification for the GenerateHelmWrapper transformation.
// +k8s:deepcopy-gen=true
// +ocm:jsonschema-gen=true
type GenerateHelmWrapperSpec struct {
	// ChartFile is the file access specification for the chart archive. Gotten from GetHelmChart.
	ChartFile v1alpha1.File `json:"chartFile"`
	// ChartResource is the chart resource after upload to the target repository. This will contain the
	// right Access for the created chart.
	ChartResource *v2.Resource `json:"chartResource"`
	// Images pairs each image resource before transfer with its uploaded counterpart.
	Images []ImagePair `json:"images,omitempty"`
	// Annotations are set on the wrapper OCI manifest.
	// TODO: we do this for now instead of referrers. This will contain the component id and the repository spec.
	Annotations map[string]string `json:"annotations,omitempty"`
	// OutputPath is the path of the intermediate working folder.
	OutputPath string `json:"outputPath,omitempty"`
}

// ImagePair holds an image resource before and after transfer.
// +k8s:deepcopy-gen=true
// +ocm:jsonschema-gen=true
type ImagePair struct {
	// Source is the image resource before transfer.
	Source *v2.Resource `json:"source"`
	// Target is the image resource after upload to the target repository.
	Target *v2.Resource `json:"target"`
}

// GenerateHelmWrapperOutput is the output specification of the GenerateHelmWrapper transformation.
// +k8s:deepcopy-gen=true
// +ocm:jsonschema-gen=true
type GenerateHelmWrapperOutput struct {
	// Resource is the uploaded wrapper resource with its OCI access.
	Resource *v2.Resource `json:"resource"`
}
