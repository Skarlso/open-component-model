package config

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"sigs.k8s.io/yaml"

	configv1 "ocm.software/open-component-model/bindings/go/configuration/v1"
	"ocm.software/open-component-model/bindings/go/runtime"
	internalctx "ocm.software/open-component-model/cli/internal/context"
	"ocm.software/open-component-model/cli/internal/flags/enum"
	"ocm.software/open-component-model/cli/internal/flags/log"
)

const (
	LoggingConfigType    = "logging.config.ocm.software"
	AttributesConfigType = "attributes.config.ocm.software"
)

// AddBuiltinPluginFlags adds CLI flags for built-in plugin configuration.
func AddBuiltinPluginFlags(flags *pflag.FlagSet) {
	flags.String("temp-folder", "", "Temporary folder location for the library and plugins.")
}

// ApplyFlagsToContext applies CLI flags to the context and returns updated context.
// Sets tempFolder in context if the flag is provided.
func ApplyFlagsToContext(ctx context.Context, cmd *cobra.Command) context.Context {
	if cmd.Flags().Changed("temp-folder") {
		if folder, err := cmd.Flags().GetString("temp-folder"); err == nil && folder != "" {
			ctx = internalctx.WithTempFolder(ctx, folder)
		}
	}
	return ctx
}

// GetBuiltinPluginLogger creates a logger for built-in plugins using the existing log infrastructure.
// This ensures consistent logging configuration across the entire CLI.
func GetBuiltinPluginLogger(cmd *cobra.Command) (*slog.Logger, error) {
	return log.GetBaseLogger(cmd)
}

// GetMergedConfigWithCLIFlags creates a new global configuration that includes CLI flag overrides.
// This ensures both external and built-in plugins receive the same merged configuration.
func GetMergedConfigWithCLIFlags(cmd *cobra.Command, baseConfig *configv1.Config) (*configv1.Config, error) {
	if baseConfig == nil {
		return nil, nil
	}

	if !cmd.Flags().Changed(log.LevelFlagName) && !cmd.Flags().Changed(log.FormatFlagName) && !cmd.Flags().Changed("temp-folder") {
		return baseConfig, nil
	}

	mergedConfig := baseConfig.DeepCopy()

	// Handle logging configuration
	if cmd.Flags().Changed(log.LevelFlagName) || cmd.Flags().Changed(log.FormatFlagName) {
		err := mergeLoggingConfig(cmd, mergedConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to merge logging configuration: %w", err)
		}
	}

	// Handle attributes configuration (tempFolder)
	if cmd.Flags().Changed("temp-folder") {
		err := mergeAttributesConfig(cmd, mergedConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to merge attributes configuration: %w", err)
		}
	}

	return mergedConfig, nil
}

// mergeLoggingConfig merges logging-related CLI flags into the configuration.
func mergeLoggingConfig(cmd *cobra.Command, config *configv1.Config) error {
	loggingConfig := make(map[string]string)

	if cmd.Flags().Changed(log.LevelFlagName) {
		if level, err := enum.Get(cmd.Flags(), log.LevelFlagName); err == nil {
			loggingConfig["level"] = level
		}
	}

	if cmd.Flags().Changed(log.FormatFlagName) {
		if format, err := enum.Get(cmd.Flags(), log.FormatFlagName); err == nil {
			loggingConfig["format"] = format
		}
	}

	if len(loggingConfig) == 0 {
		return nil
	}

	return mergeConfigType(config, LoggingConfigType, loggingConfig)
}

// mergeAttributesConfig merges attributes-related CLI flags into the configuration.
func mergeAttributesConfig(cmd *cobra.Command, config *configv1.Config) error {
	attributesConfig := make(map[string]string)

	if cmd.Flags().Changed("temp-folder") {
		if folder, err := cmd.Flags().GetString("temp-folder"); err == nil && folder != "" {
			attributesConfig["tempFolder"] = folder
		}
	}

	if len(attributesConfig) == 0 {
		return nil
	}

	return mergeConfigType(config, AttributesConfigType, attributesConfig)
}

// mergeConfigType merges a map[string]string configuration into the config for a specific type.
func mergeConfigType(config *configv1.Config, configType string, newConfig map[string]string) error {
	found := false
	for i, cfg := range config.Configurations {
		if cfg.Type.String() == configType+"/v1" {
			// Merge with existing config
			existingConfig := make(map[string]string)
			if err := yaml.Unmarshal(cfg.Data, &existingConfig); err != nil {
				return fmt.Errorf("failed to unmarshal existing %s configuration: %w", configType, err)
			}

			// Override with new values
			for k, v := range newConfig {
				existingConfig[k] = v
			}

			encoded, err := yaml.Marshal(existingConfig)
			if err != nil {
				return fmt.Errorf("failed to encode merged %s configuration: %w", configType, err)
			}
			config.Configurations[i].Data = encoded
			found = true
			break
		}
	}

	// If no config was found, create new one
	if !found {
		encoded, err := yaml.Marshal(newConfig)
		if err != nil {
			return fmt.Errorf("failed to encode %s configuration: %w", configType, err)
		}

		config.Configurations = append(config.Configurations, &runtime.Raw{
			Type: runtime.NewVersionedType(configType, "v1"),
			Data: encoded,
		})
	}

	return nil
}
