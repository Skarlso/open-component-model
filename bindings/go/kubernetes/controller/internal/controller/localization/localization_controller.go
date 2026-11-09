package localization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/api/v1alpha1"
	celfunctions "ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/cel/functions"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/event"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/localization"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/ocm"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/resolution"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/status"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/util"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/verification"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/pkg/configuration"
	ocifunctions "ocm.software/open-component-model/bindings/go/oci/cel/functions"
	"ocm.software/open-component-model/bindings/go/runtime"
)

// Reconciler writes the image references a component version signs into a ConfigMap
// that a deployment engine reads as Helm values.
type Reconciler struct {
	*ocm.BaseReconciler

	Resolver *resolution.Resolver
}

var _ ocm.Reconciler = (*Reconciler)(nil)

const componentRefIndex = "spec.componentRef.name"

var errTargetNotOwned = errors.New("target ConfigMap exists and is not owned by this Localization")

func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, concurrency int) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Localization{}, componentRefIndex, func(obj client.Object) []string {
		loc, ok := obj.(*v1alpha1.Localization)
		if !ok {
			return nil
		}

		return []string{loc.Spec.ComponentRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Localization{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&v1alpha1.Component{},
			handler.EnqueueRequestsFromMapFunc(r.localizationsForComponent),
			builder.WithPredicates(ocm.ComponentInfoChangedPredicate{}),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: concurrency,
			RateLimiter:             r.RateLimit.Limiter(),
		}).
		Complete(r)
}

func (r *Reconciler) localizationsForComponent(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &v1alpha1.LocalizationList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace()), client.MatchingFields{componentRefIndex: obj.GetName()}); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, loc := range list.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: k8stypes.NamespacedName{Namespace: loc.GetNamespace(), Name: loc.GetName()},
		})
	}

	return requests
}

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=localizations/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

//nolint:cyclop,funlen // follows the Resource reconciler step by step
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	loc := &v1alpha1.Localization{}
	if err := r.Get(ctx, req.NamespacedName, loc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	old := loc.DeepCopy()
	defer func(ctx context.Context) {
		status.UpdateBeforePatch(loc, r.EventRecorder, 0, err)
		if !equality.Semantic.DeepEqual(loc.Status, old.Status) {
			err = errors.Join(err, r.GetClient().Status().Patch(ctx, loc, client.MergeFrom(old)))
		}
	}(ctx)

	// The target ConfigMap is garbage collected through its owner reference.
	if loc.Spec.Suspend || !loc.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	component, err := util.GetReadyObject[v1alpha1.Component, *v1alpha1.Component](ctx, r.Client, client.ObjectKey{
		Namespace: loc.GetNamespace(),
		Name:      loc.Spec.ComponentRef.Name,
	})
	if err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get ready component: %w", err)
	}
	if component.Status.Component.RepositorySpec == nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.GetComponentVersionFailedReason, "repository spec in component status must not be nil")

		return ctrl.Result{}, fmt.Errorf("repository spec in component status must not be nil for component: %s", component.Name)
	}

	configs, err := ocm.GetEffectiveConfig(ctx, r.GetClient(), loc, component)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.GetConfigurationFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to configure context: %w", err)
	}
	loc.Status.EffectiveOCMConfig = configs

	resolveOpts, verifications, err := r.resolveOptions(ctx, loc, component, configs)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.GetConfigurationFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	// The component signature covers the rules label and the digests the images are pinned to.
	desc, err := r.Resolver.GetComponentVersion(ctx, resolveOpts,
		resolution.Verification{Verifications: verifications},
		component.Status.Component.Component,
		component.Status.Component.Version)
	if err = r.tolerateNotSafelyDigestible(loc, err); err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.GetComponentVersionFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to get component version: %w", err)
	}

	rulesDesc, _, err := ocm.ResolveReferencePath(ctx, r.Resolver, desc, loc.Spec.Resource.ByReference.ReferencePath, resolveOpts)
	if err = r.tolerateNotSafelyDigestible(loc, err); err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.GetOCMResourceFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to resolve reference path: %w", err)
	}

	res, err := findResource(rulesDesc, loc.Spec.Resource.ByReference.Resource)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.GetOCMResourceFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	rules, err := localization.RulesFor(res)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.LocalizationFailedReason, err.Error())

		return ctrl.Result{}, err
	}

	values := map[string]any{}
	images := make([]v1alpha1.LocalizedImage, 0, len(rules))
	for _, rule := range rules {
		ref, err := r.resolveImage(ctx, loc, rulesDesc, rule, resolveOpts)
		if err != nil {
			err = fmt.Errorf("image resource %v: %w", rule.Resource, err)
			status.MarkNotReady(r.EventRecorder, loc, v1alpha1.LocalizationFailedReason, err.Error())

			return ctrl.Result{}, err
		}
		if err := localization.Apply(values, rule, ref); err != nil {
			err = fmt.Errorf("image resource %v: %w", rule.Resource, err)
			status.MarkNotReady(r.EventRecorder, loc, v1alpha1.LocalizationFailedReason, err.Error())

			return ctrl.Result{}, err
		}
		images = append(images, v1alpha1.LocalizedImage{
			Resource:     rule.Resource,
			Reference:    localization.Parts(ref)[localization.PartImageReference],
			DigestPinned: ref.Digest != "",
		})
	}

	data, err := yaml.Marshal(values)
	if err != nil {
		status.MarkNotReady(r.EventRecorder, loc, v1alpha1.MarshalFailedReason, err.Error())

		return ctrl.Result{}, fmt.Errorf("failed to marshal values: %w", err)
	}

	if err := r.applyTarget(ctx, loc, data); err != nil {
		reason := v1alpha1.ApplyFailed
		if errors.Is(err, errTargetNotOwned) {
			reason = v1alpha1.TargetNotOwnedReason
		}
		status.MarkNotReady(r.EventRecorder, loc, reason, err.Error())

		return ctrl.Result{}, err
	}

	loc.Status.Component = &v1alpha1.ComponentInfo{
		RepositorySpec: component.Status.Component.RepositorySpec,
		Component:      rulesDesc.Component.Name,
		Version:        rulesDesc.Component.Version,
	}
	loc.Status.Images = images
	logger.V(1).Info("localized", "images", len(images), "configMap", loc.Spec.Target.ConfigMapName)
	status.MarkReady(r.EventRecorder, loc, "Localized %d images from %s:%s", len(images), rulesDesc.Component.Name, rulesDesc.Component.Version)

	return ctrl.Result{}, nil
}

func (r *Reconciler) resolveOptions(
	ctx context.Context,
	loc *v1alpha1.Localization,
	component *v1alpha1.Component,
	configs []v1alpha1.OCMConfiguration,
) (*resolution.Options, []verification.Verification, error) {
	repoSpec := &runtime.Raw{}
	if err := runtime.NewScheme(runtime.WithAllowUnknown()).Decode(
		bytes.NewReader(component.Status.Component.RepositorySpec.Raw), repoSpec); err != nil {
		return nil, nil, fmt.Errorf("failed to decode repository spec: %w", err)
	}

	cfg, err := configuration.LoadConfigurations(ctx, r.Client, loc.GetNamespace(), configs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load configurations: %w", err)
	}

	pm, err := r.PluginManagerFor(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create plugin manager: %w", err)
	}

	verifications, err := verification.GetVerifications(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get verifications: %w", err)
	}

	return &resolution.Options{
		RepositorySpec: repoSpec,
		Configuration:  cfg,
		PluginManager:  pm,
	}, verifications, nil
}

// resolveImage returns the current location of the image resource a rule names, pinned to
// its signed digest. The image is not fetched: the workload pulls it by that digest.
func (r *Reconciler) resolveImage(
	ctx context.Context,
	loc *v1alpha1.Localization,
	parent *descriptor.Descriptor,
	rule localization.Rule,
	opts *resolution.Options,
) (ocifunctions.Reference, error) {
	desc, repoSpec, err := ocm.ResolveReferencePath(ctx, r.Resolver, parent, rule.ReferencePath, opts)
	if err = r.tolerateNotSafelyDigestible(loc, err); err != nil {
		return ocifunctions.Reference{}, err
	}

	res, err := findResource(desc, rule.Resource)
	if err != nil {
		return ocifunctions.Reference{}, err
	}

	access := &runtime.Raw{}
	if err := runtime.NewScheme(runtime.WithAllowUnknown()).Convert(res.Access, access); err != nil {
		return ocifunctions.Reference{}, fmt.Errorf("converting access: %w", err)
	}

	repoSpecData, err := json.Marshal(repoSpec)
	if err != nil {
		return ocifunctions.Reference{}, fmt.Errorf("marshaling repository spec: %w", err)
	}

	ref, err := ocifunctions.ResolveAccess(access, ocifunctions.WithReferenceResolver(celfunctions.LocalBlobResolver(&v1alpha1.ComponentInfo{
		RepositorySpec: &apiextensionsv1.JSON{Raw: repoSpecData},
		Component:      desc.Component.Name,
		Version:        desc.Component.Version,
	})))
	if err != nil {
		return ocifunctions.Reference{}, err
	}

	return localization.Pin(ref, res.Digest)
}

func (r *Reconciler) applyTarget(ctx context.Context, loc *v1alpha1.Localization, data []byte) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      loc.Spec.Target.ConfigMapName,
			Namespace: loc.GetNamespace(),
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.GetClient(), cm, func() error {
		// Adopting an existing ConfigMap would let any Localization author overwrite it.
		if !cm.CreationTimestamp.IsZero() && !metav1.IsControlledBy(cm, loc) {
			return errTargetNotOwned
		}
		cm.Data = map[string]string{loc.Spec.Target.Key: string(data)}

		return controllerutil.SetControllerReference(loc, cm, r.GetScheme())
	})
	if err != nil {
		return fmt.Errorf("failed to apply target ConfigMap %s: %w", cm.Name, err)
	}

	return nil
}

// tolerateNotSafelyDigestible reports ErrNotSafelyDigestible as an event, as the Resource
// controller does, and returns every other error.
func (r *Reconciler) tolerateNotSafelyDigestible(loc *v1alpha1.Localization, err error) error {
	if errors.Is(err, resolution.ErrNotSafelyDigestible) {
		event.New(r.EventRecorder, loc, nil, v1alpha1.EventSeverityError, "%s", err.Error())

		return nil
	}

	return err
}

func findResource(desc *descriptor.Descriptor, identity runtime.Identity) (*descriptor.Resource, error) {
	for i, res := range desc.Component.Resources {
		if identity.Match(res.ToIdentity(), ocm.IdentityFuncIgnoreVersion()) {
			return &desc.Component.Resources[i], nil
		}
	}

	return nil, fmt.Errorf("resource with identity %v not found in component %s:%s",
		identity, desc.Component.Name, desc.Component.Version)
}
