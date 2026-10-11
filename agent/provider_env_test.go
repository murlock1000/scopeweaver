package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromEnvReadsMountedCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(path, []byte("mounted-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY_FILE", path)
	cfg, ok := FromEnv()
	if !ok || cfg.APIKey != "mounted-secret" {
		t.Fatalf("ok=%v key=%q", ok, cfg.APIKey)
	}
}
