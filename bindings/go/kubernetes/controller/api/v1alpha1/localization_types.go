package v1alpha1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"ocm.software/open-component-model/bindings/go/runtime"
)

const KindLocalization = "Localization"

// LocalizationSpec defines the desired state of Localization.
type LocalizationSpec struct {
	// ComponentRef is a reference to a Component.
	// +required
	ComponentRef corev1.LocalObjectReference `json:"componentRef"`

	// Resource identifies the ocm resource, e.g. a Helm chart, whose signed
	// ocm.software/localization-rules label is applied.
	// +required
	Resource ResourceID `json:"resource"`

	// Target is the ConfigMap the localized values are written to.
	// +required
	Target LocalizationTarget `json:"target"`

	// OCMConfig defines references to secrets, config maps or ocm api
	// objects providing configuration data including credentials.
	// +optional
	OCMConfig []OCMConfiguration `json:"ocmConfig,omitempty"`

	// Suspend tells the controller to suspend the reconciliation of this
	// Localization.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// LocalizationTarget is a ConfigMap in the namespace of the Localization. The controller
// creates it and refuses to write to a ConfigMap it does not own.
type LocalizationTarget struct {
	// ConfigMapName is the name of the ConfigMap.
	// +kubebuilder:validation:MinLength=1
	// +required
	ConfigMapName string `json:"configMapName"`

	// Key is the ConfigMap key the values are written to as YAML. The default is the
	// key Flux HelmRelease valuesFrom reads by default.
	// +kubebuilder:default:="values.yaml"
	// +optional
	Key string `json:"key,omitempty"`
}

// LocalizedImage is an image reference written to the target.
type LocalizedImage struct {
	// Resource is the identity of the image resource.
	// +required
	Resource runtime.Identity `json:"resource"`

	// Reference is the reference the values were rendered from.
	// +required
	Reference string `json:"reference"`

	// DigestPinned reports whether Reference carries a digest.
	// +required
	DigestPinned bool `json:"digestPinned"`
}

// LocalizationStatus defines the observed state of Localization.
type LocalizationStatus struct {
	// ObservedGeneration is the last observed generation of the Localization
	// object.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the Localization.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Component is the component version that carries the applied rules.
	// +optional
	Component *ComponentInfo `json:"component,omitempty"`

	// Images lists the image references written to the target.
	// +optional
	Images []LocalizedImage `json:"images,omitempty"`

	// EffectiveOCMConfig specifies the entirety of config maps and secrets
	// whose configuration data was applied to the Localization reconciliation,
	// in the order the configuration data was applied.
	// +optional
	EffectiveOCMConfig []OCMConfiguration `json:"effectiveOCMConfig,omitempty"`
}

// Localization is the Schema for the localizations API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].message`,description="Indicates if the Localization is Ready",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Displays the Age of the Localization"
type Localization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalizationSpec   `json:"spec"`
	Status LocalizationStatus `json:"status,omitempty"`
}

func (in *Localization) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

func (in *Localization) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

func (in *Localization) GetVID() map[string]string {
	vid := fmt.Sprintf("%s:%s", in.GetNamespace(), in.GetName())
	metadata := make(map[string]string)
	metadata[GroupVersion.Group+"/localization_version"] = vid

	return metadata
}

func (in *Localization) SetObservedGeneration(v int64) {
	in.Status.ObservedGeneration = v
}

func (in *Localization) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *Localization) GetKind() string {
	return KindLocalization
}

func (in *Localization) GetSpecifiedOCMConfig() []OCMConfiguration {
	return in.Spec.OCMConfig
}

func (in *Localization) GetEffectiveOCMConfig() []OCMConfiguration {
	return in.Status.EffectiveOCMConfig
}

// +kubebuilder:object:root=true

// LocalizationList contains a list of Localization.
type LocalizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Localization `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Localization{}, &LocalizationList{})
}
