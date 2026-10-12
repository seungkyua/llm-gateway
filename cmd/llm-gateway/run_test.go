package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// whatever was written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = original
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestRunShouldReturnNonZeroAndPrintStderrWhenConfigPathDoesNotExist(t *testing.T) {
	var code int
	stderr := captureStderr(t, func() {
		code = run([]string{"--config", "/nonexistent/path/config.yaml"})
	})

	if code == 0 {
		t.Error("expected non-zero exit code for nonexistent config path")
	}
	if strings.TrimSpace(stderr) == "" {
		t.Error("expected an error message on stderr")
	}
}

func TestRunShouldReturnNonZeroAndPrintStderrWhenAdminTokenUnresolved(t *testing.T) {
	os.Unsetenv("ADMIN_TOKEN_FOR_RUN_TEST")
	dir := t.TempDir()
	path := dir + "/config.yaml"
	os.WriteFile(path, []byte(`
server:
  listen_addr: ":0"
admin:
  token: "${ADMIN_TOKEN_FOR_RUN_TEST}"
backends: []
storage:
  keys_file: "`+dir+`/keys.json"
  usage_file: "`+dir+`/usage.json"
`), 0o600)

	var code int
	stderr := captureStderr(t, func() {
		code = run([]string{"--config", path})
	})

	if code == 0 {
		t.Error("expected non-zero exit code when admin.token is unresolved")
	}
	if strings.TrimSpace(stderr) == "" {
		t.Error("expected an error message on stderr")
	}
}
