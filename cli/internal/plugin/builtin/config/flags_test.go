package config

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/yaml"

	configv1 "ocm.software/open-component-model/bindings/go/configuration/v1"
	"ocm.software/open-component-model/bindings/go/runtime"
	internalctx "ocm.software/open-component-model/cli/internal/context"
	"ocm.software/open-component-model/cli/internal/flags/log"
)

func createTestCommand() *cobra.Command {
	cmd := &cobra.Command{}
	log.RegisterLoggingFlags(cmd.Flags())
	AddBuiltinPluginFlags(cmd.Flags())
	return cmd
}

func setFlag(cmd *cobra.Command, name, value string) {
	_ = cmd.Flags().Set(name, value)
	cmd.Flags().Lookup(name).Changed = true
}

func createTestConfig(logLevel, logFormat, tempFolder string) *configv1.Config {
	config := &configv1.Config{
		Type: runtime.NewVersionedType(configv1.ConfigType, configv1.ConfigTypeV1),
		Configurations: []*runtime.Raw{},
	}

	if logLevel != "" || logFormat != "" {
		loggingConfig := make(map[string]string)
		if logLevel != "" {
			loggingConfig["level"] = logLevel
		}
		if logFormat != "" {
			loggingConfig["format"] = logFormat
		}
		data, _ := yaml.Marshal(loggingConfig)
		config.Configurations = append(config.Configurations, &runtime.Raw{
			Type: runtime.NewVersionedType(LoggingConfigType, "v1"),
			Data: data,
		})
	}

	if tempFolder != "" {
		attributesConfig := map[string]string{"tempFolder": tempFolder}
		data, _ := yaml.Marshal(attributesConfig)
		config.Configurations = append(config.Configurations, &runtime.Raw{
			Type: runtime.NewVersionedType(AttributesConfigType, "v1"),
			Data: data,
		})
	}

	return config
}

func TestApplyFlagsToContext(t *testing.T) {
	cmd := createTestCommand()
	ctx := context.Background()

	// Test without temp-folder flag
	result := ApplyFlagsToContext(ctx, cmd)
	assert.Equal(t, "", internalctx.TempFolderFromContext(result))

	// Test with temp-folder flag
	setFlag(cmd, "temp-folder", "/tmp/test")
	result = ApplyFlagsToContext(ctx, cmd)
	assert.Equal(t, "/tmp/test", internalctx.TempFolderFromContext(result))
}

func TestGetMergedConfigWithCLIFlags_NoFlags(t *testing.T) {
	cmd := createTestCommand()
	baseConfig := createTestConfig("info", "text", "/tmp/base")

	result, err := GetMergedConfigWithCLIFlags(cmd, baseConfig)
	assert.NoError(t, err)
	assert.Equal(t, baseConfig, result) // Should return original config unchanged
}

func TestGetMergedConfigWithCLIFlags_WithFlags(t *testing.T) {
	cmd := createTestCommand()
	setFlag(cmd, log.LevelFlagName, "debug")
	setFlag(cmd, "temp-folder", "/tmp/override")

	baseConfig := createTestConfig("info", "text", "/tmp/base")

	result, err := GetMergedConfigWithCLIFlags(cmd, baseConfig)
	assert.NoError(t, err)

	// Check that logging config was updated
	var foundLogging bool
	for _, cfg := range result.Configurations {
		t.Logf("Config type: %s", cfg.Type.String())
		if cfg.Type.String() == LoggingConfigType+"/v1" {
			var loggingConfig map[string]string
			err := yaml.Unmarshal(cfg.Data, &loggingConfig)
			assert.NoError(t, err)
			assert.Equal(t, "debug", loggingConfig["level"])
			foundLogging = true
		}
	}
	assert.True(t, foundLogging, "Should have logging configuration")
}