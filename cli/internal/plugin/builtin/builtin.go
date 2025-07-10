package builtin

import (
	"context"
	"fmt"
	"log/slog"

	"ocm.software/open-component-model/bindings/go/plugin/manager"
	ocicredentialplugin "ocm.software/open-component-model/cli/internal/plugin/builtin/credentials/oci"
	ctfplugin "ocm.software/open-component-model/cli/internal/plugin/builtin/ctf"
	"ocm.software/open-component-model/cli/internal/plugin/builtin/input/file"
	"ocm.software/open-component-model/cli/internal/plugin/builtin/input/utf8"
	ociplugin "ocm.software/open-component-model/cli/internal/plugin/builtin/oci"
)

// Register registers built-in plugins with the plugin manager using the provided context and logger.
func Register(ctx context.Context, manager *manager.PluginManager, logger *slog.Logger) error {
	if err := ocicredentialplugin.Register(manager.CredentialRepositoryRegistry); err != nil {
		return fmt.Errorf("could not register OCI inbuilt credential plugin: %w", err)
	}

	if err := ociplugin.Register(
		ctx,
		manager.ComponentVersionRepositoryRegistry,
		manager.ResourcePluginRegistry,
		manager.DigestProcessorRegistry,
		logger,
	); err != nil {
		return fmt.Errorf("could not register OCI inbuilt plugin: %w", err)
	}

	if err := ctfplugin.Register(ctx, manager.ComponentVersionRepositoryRegistry, logger); err != nil {
		return fmt.Errorf("could not register CTF inbuilt plugin: %w", err)
	}
	if err := file.Register(manager.InputRegistry); err != nil {
		return fmt.Errorf("could not register file input plugin: %w", err)
	}
	if err := utf8.Register(manager.InputRegistry); err != nil {
		return fmt.Errorf("could not register utf8 input plugin: %w", err)
	}

	return nil
}
