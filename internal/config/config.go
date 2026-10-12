// Package config loads and validates llm-gateway's config.yaml, including
// ${VAR} environment variable substitution and config path resolution.
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is used when neither --config flag nor
// LLM_GATEWAY_CONFIG env var is set.
const DefaultConfigPath = "./config.yaml"

// EnvConfigPath is the environment variable that can override the config
// file path (lower priority than the --config CLI flag).
const EnvConfigPath = "LLM_GATEWAY_CONFIG"

// Config is the root structure of config.yaml.
type Config struct {
	Server struct {
		ListenAddr string `yaml:"listen_addr"`
	} `yaml:"server"`

	Admin struct {
		Token string `yaml:"token"`
	} `yaml:"admin"`

	Backends []BackendConfig `yaml:"backends"`

	LoadBalancer struct {
		Strategy            string `yaml:"strategy"`
		HealthCheckInterval string `yaml:"health_check_interval"`
	} `yaml:"load_balancer"`

	RateLimit struct {
		Default struct {
			RPM int `yaml:"rpm"`
			TPM int `yaml:"tpm"`
		} `yaml:"default"`
	} `yaml:"rate_limit"`

	TokenLimit struct {
		Default struct {
			MaxTokensPerRequest int    `yaml:"max_tokens_per_request"`
			QuotaPeriod         string `yaml:"quota_period"`
			QuotaTokens         int64  `yaml:"quota_tokens"`
		} `yaml:"default"`
	} `yaml:"token_limit"`

	TokenCounting struct {
		Method string `yaml:"method"`
	} `yaml:"token_counting"`

	Storage struct {
		KeysFile  string `yaml:"keys_file"`
		UsageFile string `yaml:"usage_file"`
	} `yaml:"storage"`

	Logging struct {
		Format   string `yaml:"format"`
		Output   string `yaml:"output"`
		FilePath string `yaml:"file_path"`
	} `yaml:"logging"`
}

// BackendConfig describes a single vLLM backend URL.
type BackendConfig struct {
	URL string `yaml:"url"`
}

// HealthCheckIntervalDuration parses LoadBalancer.HealthCheckInterval,
// defaulting to 5 seconds when unset or unparsable.
func (c *Config) HealthCheckIntervalDuration() time.Duration {
	const defaultInterval = 5 * time.Second
	if c.LoadBalancer.HealthCheckInterval == "" {
		return defaultInterval
	}
	d, err := time.ParseDuration(c.LoadBalancer.HealthCheckInterval)
	if err != nil {
		return defaultInterval
	}
	return d
}

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substituteEnvVars replaces every ${VAR} occurrence with the value of the
// matching environment variable. Unset variables are replaced with an empty
// string so that required-field validation can detect them afterwards.
func substituteEnvVars(raw []byte) []byte {
	return envVarPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := envVarPattern.FindSubmatch(match)[1]
		return []byte(os.Getenv(string(name)))
	})
}

// Load reads, substitutes env vars in, parses, and validates the config
// file at path. It returns an error (never panics) when the file is
// missing, the YAML is malformed, or a required field is unresolved.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: failed to read %q: %w", path, err)
	}

	substituted := substituteEnvVars(raw)

	var cfg Config
	if err := yaml.Unmarshal(substituted, &cfg); err != nil {
		return nil, fmt.Errorf("config: failed to parse YAML in %q: %w", path, err)
	}

	if cfg.Admin.Token == "" {
		return nil, fmt.Errorf("config: admin.token is required but unresolved (set the referenced environment variable, e.g. ADMIN_TOKEN)")
	}

	return &cfg, nil
}

// ResolvePath decides the config file path using the priority:
// CLI flag > LLM_GATEWAY_CONFIG env var > DefaultConfigPath.
func ResolvePath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envValue := os.Getenv(EnvConfigPath); envValue != "" {
		return envValue
	}
	return DefaultConfigPath
}
