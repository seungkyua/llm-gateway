package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}

const validConfigYAML = `
server:
  listen_addr: ":8080"

admin:
  token: "${ADMIN_TOKEN}"

backends:
  - url: "http://vllm-1:8000"
  - url: "http://vllm-2:8000"

load_balancer:
  strategy: "round_robin_healthcheck"
  health_check_interval: "5s"

rate_limit:
  default:
    rpm: 60
    tpm: 100000

token_limit:
  default:
    max_tokens_per_request: 4096
    quota_period: "monthly"
    quota_tokens: 1000000

token_counting:
  method: "usage_field"

storage:
  keys_file: "./data/keys.json"
  usage_file: "./data/usage.json"

logging:
  format: "json"
  output: "stdout"
  file_path: "./logs/llm-gateway.log"
`

func TestLoadShouldReturnConfigWhenYAMLIsValidAndAdminTokenResolved(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "secret-token-123")
	path := writeTempConfig(t, validConfigYAML)

	cfg, err := Load(path)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Server.ListenAddr != ":8080" {
		t.Errorf("expected listen_addr :8080, got %q", cfg.Server.ListenAddr)
	}
}

func TestLoadShouldSubstituteEnvVarWhenPlaceholderPresent(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "secret-token-123")
	path := writeTempConfig(t, validConfigYAML)

	cfg, err := Load(path)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Admin.Token != "secret-token-123" {
		t.Errorf("expected admin.token to be substituted, got %q", cfg.Admin.Token)
	}
}

func TestLoadShouldReturnErrorWhenAdminTokenUnresolved(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "")
	os.Unsetenv("ADMIN_TOKEN")
	path := writeTempConfig(t, validConfigYAML)

	_, err := Load(path)

	if err == nil {
		t.Fatal("expected error when admin.token is unresolved, got nil")
	}
}

func TestLoadShouldReturnErrorWhenYAMLIsInvalid(t *testing.T) {
	path := writeTempConfig(t, "server:\n  listen_addr: [unterminated\n")

	_, err := Load(path)

	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoadShouldReturnErrorWhenPathDoesNotExist(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")

	if err == nil {
		t.Fatal("expected error for nonexistent config path, got nil")
	}
}

func TestResolvePathShouldPreferFlagOverEnvAndDefault(t *testing.T) {
	t.Setenv("LLM_GATEWAY_CONFIG", "/from/env.yaml")

	got := ResolvePath("/from/flag.yaml")

	if got != "/from/flag.yaml" {
		t.Errorf("expected flag path to win, got %q", got)
	}
}

func TestResolvePathShouldPreferEnvOverDefaultWhenFlagEmpty(t *testing.T) {
	t.Setenv("LLM_GATEWAY_CONFIG", "/from/env.yaml")

	got := ResolvePath("")

	if got != "/from/env.yaml" {
		t.Errorf("expected env path to win, got %q", got)
	}
}

func TestResolvePathShouldReturnDefaultWhenFlagAndEnvEmpty(t *testing.T) {
	os.Unsetenv("LLM_GATEWAY_CONFIG")

	got := ResolvePath("")

	if got != DefaultConfigPath {
		t.Errorf("expected default path %q, got %q", DefaultConfigPath, got)
	}
}
