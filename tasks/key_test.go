package tasks

import (
	"os"
	"path/filepath"
	"testing"
)

// TestKey: a bare key's file, an env file under pi-ai's name for the provider, and a missing name.
func TestKey(t *testing.T) {
	dir := t.TempDir()
	bare, envf := filepath.Join(dir, "gemini.key"), filepath.Join(dir, "openrouter.env")
	os.WriteFile(bare, []byte("abc\n"), 0o600)
	os.WriteFile(envf, []byte("# OpenRouter\nexport OPENROUTER_API_KEY=\"sk-or-x\"\nOTHER=1\n"), 0o600)
	t.Setenv("OPENROUTER_API_KEY", "")
	if k, err := Key("google", bare); k != "abc" || err != nil {
		t.Fatalf("bare: %q %v", k, err)
	}
	if k, err := Key("openrouter", envf); k != "sk-or-x" || err != nil {
		t.Fatalf("env file: %q %v", k, err)
	}
	if _, err := Key("anthropic", envf); err == nil {
		t.Fatal("no anthropic key in the env file, and no error")
	}
}
