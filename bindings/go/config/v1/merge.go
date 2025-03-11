package v1

// Merge merges the provided configs into a single config.
func Merge(configs ...*Config) *Config {
	merged := new(Config)
	merged.Type = configs[0].Type
	merged.Repositories = make([]RepositoryConfigEntry, 0)
	merged.Consumers = make([]Consumer, 0)

	for _, config := range configs {
		merged.Repositories = append(merged.Repositories, config.Repositories...)
		merged.Consumers = append(merged.Consumers, config.Consumers...)
	}

	return merged
}
