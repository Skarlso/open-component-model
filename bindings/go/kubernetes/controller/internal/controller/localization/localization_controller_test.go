package localization

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	descruntime "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/api/v1alpha1"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/localization"
	"ocm.software/open-component-model/bindings/go/kubernetes/controller/internal/test"
	ocispec "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	"ocm.software/open-component-model/bindings/go/runtime"
)

const (
	podinfoDigest = "262578cde928d5c9eba3bce079976444f624c13ed0afb741d90d5423877496cb"
	redisDigest   = "7a91508d9177f43552b60cfc0182d7c30a84e95bed03854855b3ab29b6a85db2"
)

func ociResource(name, resourceType, imageReference, digest string) descruntime.Resource {
	GinkgoHelper()

	access := &runtime.Raw{}
	Expect(runtime.NewScheme(runtime.WithAllowUnknown()).Convert(&ocispec.OCIImage{
		Type:           runtime.NewVersionedType(ocispec.LegacyType, ocispec.LegacyTypeVersion),
		ImageReference: imageReference,
	}, access)).To(Succeed())

	res := descruntime.Resource{
		ElementMeta: descruntime.ElementMeta{ObjectMeta: descruntime.ObjectMeta{Name: name, Version: "1.0.0"}},
		Type:        resourceType,
		Relation:    descruntime.ExternalRelation,
		Access:      access,
	}
	if digest != "" {
		res.Digest = &descruntime.Digest{
			HashAlgorithm:          "SHA-256",
			NormalisationAlgorithm: descruntime.OCIArtifactDigestV1,
			Value:                  digest,
		}
	}

	return res
}

func descriptorFor(name, version string, resources []descruntime.Resource, references ...descruntime.Reference) *descruntime.Descriptor {
	return &descruntime.Descriptor{
		Component: descruntime.Component{
			ComponentMeta: descruntime.ComponentMeta{ObjectMeta: descruntime.ObjectMeta{Name: name, Version: version}},
			Provider:      descruntime.Provider{Name: "ocm.software"},
			Resources:     resources,
			References:    references,
		},
	}
}

func rulesLabel(signing bool) descruntime.Label {
	return descruntime.Label{
		Name:    localization.LabelName,
		Version: localization.LabelVersion,
		Signing: signing,
		Value: []byte(`[
  {"resource": {"name": "podinfo-image"},
   "values": [{"path": "image.repository", "from": "image"}, {"path": "image.tag", "from": "reference"}]},
  {"resource": {"name": "redis-image"}, "referencePath": [{"name": "redis"}],
   "values": [{"path": "redis.image", "from": "imageReference"}]}
]`),
	}
}

var _ = Describe("Localization Controller", func() {
	var (
		namespace     string
		componentName string
		ctfPath       string
	)

	BeforeEach(func(ctx SpecContext) {
		ns := test.NamespaceForTest(ctx)
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		namespace = ns.GetName()
		componentName = "ocm.software/localization-" + namespace
		ctfPath = filepath.Join(GinkgoT().TempDir(), "ctf")
		Expect(os.MkdirAll(ctfPath, 0o777)).To(Succeed())
	})

	// setup stores podinfo in versions 1.0.0 and 1.1.0, each referencing a redis component,
	// and returns a ready Component pointing at version.
	setup := func(ctx SpecContext, signedRules bool, version string) *v1alpha1.Component {
		chart := ociResource("chart", "helmChart", "ghcr.io/stefanprodan/charts/podinfo:6.9.1", "")
		chart.Labels = []descruntime.Label{rulesLabel(signedRules)}

		redis := descriptorFor(componentName+"/redis", "7.0.0", []descruntime.Resource{
			ociResource("redis-image", "ociImage", "quay.io/opstree/redis:7", redisDigest),
		})
		descs := []*descruntime.Descriptor{redis}
		for _, v := range []string{"1.0.0", "1.1.0"} {
			descs = append(descs, descriptorFor(componentName, v, []descruntime.Resource{
				chart,
				ociResource("podinfo-image", "ociImage", "ghcr.io/stefanprodan/podinfo:"+v, podinfoDigest),
			}, descruntime.Reference{
				ElementMeta: descruntime.ElementMeta{ObjectMeta: descruntime.ObjectMeta{Name: "redis", Version: "7.0.0"}},
				Component:   redis.Component.Name,
			}))
		}
		_, specData := test.SetupCTFComponentVersionRepository(ctx, ctfPath, descs)

		component := test.MockComponent(ctx, "podinfo", namespace, &test.MockComponentOptions{
			Client:   k8sClient,
			Recorder: recorder,
			Info: v1alpha1.ComponentInfo{
				Component:      componentName,
				Version:        version,
				RepositorySpec: &apiextensionsv1.JSON{Raw: specData},
			},
			Repository: "repository",
		})
		DeferCleanup(func(ctx SpecContext) {
			test.DeleteObject(ctx, k8sClient, component)
		})

		return component
	}

	createLocalization := func(ctx SpecContext) *v1alpha1.Localization {
		loc := &v1alpha1.Localization{
			ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: namespace},
			Spec: v1alpha1.LocalizationSpec{
				ComponentRef: corev1.LocalObjectReference{Name: "podinfo"},
				Resource: v1alpha1.ResourceID{ByReference: v1alpha1.ResourceReference{
					Resource: runtime.Identity{"name": "chart"},
				}},
				Target: v1alpha1.LocalizationTarget{ConfigMapName: "podinfo-values"},
			},
		}
		Expect(k8sClient.Create(ctx, loc)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			test.DeleteObject(ctx, k8sClient, loc)
		})

		return loc
	}

	valuesOf := func(g Gomega, ctx SpecContext) map[string]any {
		cm := &corev1.ConfigMap{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "podinfo-values"}, cm)).To(Succeed())
		values := map[string]any{}
		g.Expect(yaml.Unmarshal([]byte(cm.Data["values.yaml"]), &values)).To(Succeed())

		return values
	}

	It("writes signed image references pinned to their digests into the target", func(ctx SpecContext) {
		component := setup(ctx, true, "1.0.0")
		loc := createLocalization(ctx)

		test.WaitForReadyObject(ctx, k8sClient, loc, map[string]any{
			"Status.Component.Version": "1.0.0",
			"Status.Images": ConsistOf(
				v1alpha1.LocalizedImage{
					Resource:     runtime.Identity{"name": "podinfo-image"},
					Reference:    "ghcr.io/stefanprodan/podinfo:1.0.0@sha256:" + podinfoDigest,
					DigestPinned: true,
				},
				v1alpha1.LocalizedImage{
					Resource:     runtime.Identity{"name": "redis-image"},
					Reference:    "quay.io/opstree/redis:7@sha256:" + redisDigest,
					DigestPinned: true,
				},
			),
		})

		Eventually(func(g Gomega, ctx SpecContext) {
			g.Expect(valuesOf(g, ctx)).To(Equal(map[string]any{
				"image": map[string]any{
					"repository": "ghcr.io/stefanprodan/podinfo",
					"tag":        "1.0.0@sha256:" + podinfoDigest,
				},
				"redis": map[string]any{
					"image": "quay.io/opstree/redis:7@sha256:" + redisDigest,
				},
			}))
		}).WithContext(ctx).Should(Succeed())

		By("moving the component to a new version")
		old := component.DeepCopy()
		component.Status.Component.Version = "1.1.0"
		Expect(k8sClient.Status().Patch(ctx, component, client.MergeFrom(old))).To(Succeed())

		Eventually(func(g Gomega, ctx SpecContext) {
			g.Expect(valuesOf(g, ctx)).To(HaveKeyWithValue("image", HaveKeyWithValue("tag", "1.1.0@sha256:"+podinfoDigest)))
		}).WithContext(ctx).WithTimeout(test.DefaultKubernetesOperationTimeout).Should(Succeed())
	})

	It("rejects rules that are not signed", func(ctx SpecContext) {
		setup(ctx, false, "1.0.0")
		loc := createLocalization(ctx)

		test.WaitForNotReadyObjectWithMessage(ctx, k8sClient, loc, v1alpha1.LocalizationFailedReason, "signing: true")
	})

	It("does not overwrite a ConfigMap it does not own", func(ctx SpecContext) {
		setup(ctx, true, "1.0.0")
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "podinfo-values", Namespace: namespace},
			Data:       map[string]string{"values.yaml": "keep: me\n"},
		}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())

		loc := createLocalization(ctx)

		test.WaitForNotReadyObject(ctx, k8sClient, loc, v1alpha1.TargetNotOwnedReason)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(existing), existing)).To(Succeed())
		Expect(existing.Data).To(Equal(map[string]string{"values.yaml": "keep: me\n"}))
	})
})
