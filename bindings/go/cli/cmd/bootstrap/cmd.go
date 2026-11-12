package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"

	"github.com/spf13/cobra"

	"ocm.software/open-component-model/bindings/go/cli/cmd/download/shared"
	ocmctx "ocm.software/open-component-model/bindings/go/cli/internal/context"
	"ocm.software/open-component-model/bindings/go/cli/internal/repository/ocm"
	genericv1 "ocm.software/open-component-model/bindings/go/configuration/generic/v1/spec"
	resolverspec "ocm.software/open-component-model/bindings/go/configuration/resolvers/v1alpha1/spec"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	"ocm.software/open-component-model/bindings/go/oci/compref"
	ctfspec "ocm.software/open-component-model/bindings/go/oci/spec/repository/v1/ctf"
	"ocm.software/open-component-model/bindings/go/runtime"
)

const (
	FlagValues = "values"

	// LabelBootstrap marks the resource that `ocm bootstrap` runs.
	LabelBootstrap = "ocm.software/bootstrap"

	EnvComponent = "OCM_BOOTSTRAP_COMPONENT"
	EnvVersion   = "OCM_BOOTSTRAP_VERSION"
	EnvValues    = "OCM_BOOTSTRAP_VALUES"
	EnvOCM       = "OCM_BOOTSTRAP_OCM"
	EnvOCMConfig = "OCM_CONFIG"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap {reference} [-- args...]",
		Short: "Run the bootstrap resource of a component version (EXPERIMENTAL)",
		Args:  cobra.MinimumNArgs(1),
		Long: `Run the resource labeled "ocm.software/bootstrap: true" of a component version.

This command is a proof of concept. Its flags, environment and behaviour may change at any time.

The bootstrap resource is run as a container if its type is "ociImage" or "ociArtifact",
and as a local executable otherwise. Executables may carry "os" and "architecture" extra
identities; only the one matching the current platform is run.

The bootstrapper is told what to work on through the environment:

  OCM_BOOTSTRAP_COMPONENT  name of the component version being bootstrapped
  OCM_BOOTSTRAP_VERSION    its version
  OCM_BOOTSTRAP_VALUES     path to the file given with --values, if any
  OCM_BOOTSTRAP_OCM        path to the ocm binary running the bootstrap (executables only)
  OCM_CONFIG               the effective ocm configuration, with a resolver for the component

Because of the resolver, the bootstrapper can address its own component by name alone,
for example "ocm download resource $OCM_BOOTSTRAP_COMPONENT:$OCM_BOOTSTRAP_VERSION --identity name=config",
no matter which repository it was transferred to. For executables, "ocm" on PATH is the
binary running the bootstrap.

Arguments after "--" are passed to the bootstrapper.`,
		Example: `  # Run the bootstrapper of a component version
  ocm bootstrap ghcr.io/acme//acme.org/platform:1.0.0

  # Hand user configuration to the bootstrapper and pass it arguments
  ocm bootstrap ./ctf//acme.org/platform:1.0.0 --values ./values.yaml -- install`,
		RunE:              Bootstrap,
		DisableAutoGenTag: true,
	}

	cmd.Flags().String(FlagValues, "", "file handed to the bootstrapper as "+EnvValues+". OCM does not read or validate it.")

	return cmd
}

func Bootstrap(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	pluginManager, credentialGraph, logger, err := shared.GetContextItems(cmd)
	if err != nil {
		return err
	}

	reference, bootstrapArgs := args[0], args[1:]
	if dash := cmd.ArgsLenAtDash(); dash > 1 {
		return fmt.Errorf("expected exactly one reference before \"--\", got %d", dash)
	}

	values, err := cmd.Flags().GetString(FlagValues)
	if err != nil {
		return fmt.Errorf("getting values flag failed: %w", err)
	}
	if values != "" {
		if values, err = filepath.Abs(values); err != nil {
			return fmt.Errorf("resolving values path failed: %w", err)
		}
		if _, err := os.Stat(values); err != nil {
			return fmt.Errorf("values file: %w", err)
		}
	}

	ref, err := compref.Parse(reference)
	if err != nil {
		return fmt.Errorf("parsing component reference %q failed: %w", reference, err)
	}

	config := ocmctx.FromContext(ctx).Configuration()
	repoProvider, err := ocm.NewComponentVersionRepositoryForComponentProvider(ctx, pluginManager.ComponentVersionRepositoryRegistry, credentialGraph, config, ref)
	if err != nil {
		return fmt.Errorf("could not initialize ocm repository: %w", err)
	}
	repo, err := repoProvider.GetComponentVersionRepositoryForComponent(ctx, ref.Component, ref.Version)
	if err != nil {
		return fmt.Errorf("could not access ocm repository: %w", err)
	}
	repo, desc, err := ocm.GetPinnedComponentVersion(ctx, repo, ref)
	if err != nil {
		return fmt.Errorf("getting component version failed: %w", err)
	}

	res, err := findBootstrapResource(desc)
	if err != nil {
		return err
	}
	logger.Info("found bootstrap resource", slog.String("name", res.Name), slog.String("type", res.Type))

	workDir, err := os.MkdirTemp("", "ocm-bootstrap-*")
	if err != nil {
		return fmt.Errorf("creating work directory failed: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(workDir); err != nil {
			logger.Warn("removing work directory failed", slog.String("path", workDir), slog.String("error", err.Error()))
		}
	}()

	repoSpec, err := absoluteRepositorySpec(ref.Repository)
	if err != nil {
		return err
	}
	configPath := filepath.Join(workDir, "ocmconfig")
	if err := writeBootstrapConfig(configPath, config, desc.Component.Name, repoSpec); err != nil {
		return err
	}

	run := runSpec{
		component:  desc.Component.Name,
		version:    desc.Component.Version,
		configPath: configPath,
		values:     values,
		args:       bootstrapArgs,
		repoSpec:   repoSpec,
	}

	if isContainer(res) {
		return runContainer(cmd, res, run)
	}

	data, err := shared.DownloadResourceData(ctx, pluginManager, credentialGraph, desc, repo, res)
	if err != nil {
		return fmt.Errorf("downloading bootstrap resource %q failed: %w", res.Name, err)
	}
	return runExecutable(cmd, data, workDir, run)
}

// runSpec is what every runner hands to the bootstrapper.
type runSpec struct {
	component, version string
	configPath, values string
	args               []string
	repoSpec           runtime.Typed
}

func (r runSpec) env(configPath, values string) []string {
	env := []string{
		EnvComponent + "=" + r.component,
		EnvVersion + "=" + r.version,
		EnvOCMConfig + "=" + configPath,
	}
	if values != "" {
		env = append(env, EnvValues+"="+values)
	}
	return env
}

func findBootstrapResource(desc *descriptor.Descriptor) (*descriptor.Resource, error) {
	var candidates []*descriptor.Resource
	for i := range desc.Component.Resources {
		res := &desc.Component.Resources[i]
		ok, err := hasBootstrapLabel(res)
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", res.Name, err)
		}
		if ok && matchesPlatform(res) {
			candidates = append(candidates, res)
		}
	}
	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("component version %s:%s has no resource labeled %q for %s/%s",
			desc.Component.Name, desc.Component.Version, LabelBootstrap, goruntime.GOOS, goruntime.GOARCH)
	case 1:
		return candidates[0], nil
	default:
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.ToIdentity().String()
		}
		return nil, fmt.Errorf("expected exactly one bootstrap resource, found %d: %v", len(candidates), names)
	}
}

func hasBootstrapLabel(res *descriptor.Resource) (bool, error) {
	idx := slices.IndexFunc(res.Labels, func(l descriptor.Label) bool { return l.Name == LabelBootstrap })
	if idx < 0 {
		return false, nil
	}
	var enabled bool
	if err := res.Labels[idx].GetValue(&enabled); err != nil {
		return false, fmt.Errorf("label %q must be a boolean: %w", LabelBootstrap, err)
	}
	return enabled, nil
}

// matchesPlatform only filters executables: a container image index selects its own platform.
func matchesPlatform(res *descriptor.Resource) bool {
	if isContainer(res) {
		return true
	}
	if os, ok := res.ExtraIdentity["os"]; ok && os != goruntime.GOOS {
		return false
	}
	if arch, ok := res.ExtraIdentity["architecture"]; ok && arch != goruntime.GOARCH {
		return false
	}
	return true
}

func isContainer(res *descriptor.Resource) bool {
	return res.Type == "ociImage" || res.Type == "ociArtifact"
}

// absoluteRepositorySpec makes CTF paths absolute, because the bootstrapper runs from
// another working directory or, for containers, sees the archive at the same path.
func absoluteRepositorySpec(spec runtime.Typed) (runtime.Typed, error) {
	ctf, ok := spec.(*ctfspec.Repository)
	if !ok {
		return spec, nil
	}
	abs, err := filepath.Abs(ctf.FilePath)
	if err != nil {
		return nil, fmt.Errorf("resolving CTF path %q failed: %w", ctf.FilePath, err)
	}
	ctf = ctf.DeepCopy()
	ctf.FilePath = abs
	return ctf, nil
}

// writeBootstrapConfig writes the effective configuration plus a resolver for the
// bootstrapped component. The resolver goes first because the first matching resolver
// wins; a catch-all at the end mirrors how the CLI treats the repository of a reference.
func writeBootstrapConfig(path string, base *genericv1.Config, component string, repoSpec runtime.Typed) error {
	cfg := &genericv1.Config{
		Type: runtime.NewVersionedType(genericv1.ConfigType, genericv1.ConfigTypeV1),
	}
	if repoSpec != nil {
		resolverCfg, err := resolverConfig(repoSpec, component)
		if err != nil {
			return err
		}
		cfg.Configurations = append(cfg.Configurations, resolverCfg)
	}
	if base != nil {
		cfg.Configurations = append(cfg.Configurations, base.Configurations...)
	}
	if repoSpec != nil {
		catchAll, err := resolverConfig(repoSpec, "*")
		if err != nil {
			return err
		}
		cfg.Configurations = append(cfg.Configurations, catchAll)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding bootstrap configuration failed: %w", err)
	}
	// The configuration can hold credentials.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing bootstrap configuration failed: %w", err)
	}
	return nil
}

func resolverConfig(repoSpec runtime.Typed, pattern string) (*runtime.Raw, error) {
	repoRaw, err := toRaw(repoSpec)
	if err != nil {
		return nil, fmt.Errorf("encoding repository specification failed: %w", err)
	}
	raw, err := toRaw(&resolverspec.Config{
		Type: runtime.NewVersionedType(resolverspec.ConfigType, resolverspec.Version),
		Resolvers: []*resolverspec.Resolver{{
			Repository:           repoRaw,
			ComponentNamePattern: pattern,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("encoding resolver configuration failed: %w", err)
	}
	return raw, nil
}

func toRaw(v any) (*runtime.Raw, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var raw runtime.Raw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

var errNoContainerRuntime = errors.New("no container runtime found: install docker or podman")
