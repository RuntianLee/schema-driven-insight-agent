package llm

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "llm.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func noSuchPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "no-such-file.yaml")
}

func TestResolve(t *testing.T) {
	t.Run("config present and valid → MiniMax client with configured model", func(t *testing.T) {
		p := writeCfg(t, "provider: minimax\nendpoint: https://example.com\nmodel: test-model-cfg\napi_key: \"x\"\n")
		client, err := Resolve(p)
		if err != nil {
			t.Fatalf("want nil error, got: %v", err)
		}
		if got := client.Model(); got != "test-model-cfg" {
			t.Errorf("Model() = %q, want %q", got, "test-model-cfg")
		}
	})

	t.Run("config absent + MINIMAX_API_KEY set → env client with MINIMAX_MODEL", func(t *testing.T) {
		t.Setenv("MINIMAX_API_KEY", "env-key")
		t.Setenv("MINIMAX_MODEL", "env-model-xyz")
		client, err := Resolve(noSuchPath(t))
		if err != nil {
			t.Fatalf("want nil error, got: %v", err)
		}
		if got := client.Model(); got != "env-model-xyz" {
			t.Errorf("Model() = %q, want %q", got, "env-model-xyz")
		}
	})

	t.Run("config absent + MINIMAX_API_KEY set + no MINIMAX_MODEL → default model", func(t *testing.T) {
		t.Setenv("MINIMAX_API_KEY", "env-key")
		t.Setenv("MINIMAX_MODEL", "")
		client, err := Resolve(noSuchPath(t))
		if err != nil {
			t.Fatalf("want nil error, got: %v", err)
		}
		if got := client.Model(); got != "MiniMax-M2.7" {
			t.Errorf("Model() = %q, want %q", got, "MiniMax-M2.7")
		}
	})

	t.Run("config absent + no env key → stateless mock", func(t *testing.T) {
		t.Setenv("MINIMAX_API_KEY", "")
		t.Setenv("MINIMAX_MODEL", "")
		client, err := Resolve(noSuchPath(t))
		if err != nil {
			t.Fatalf("want nil error, got: %v", err)
		}
		if got := client.Model(); got != "mock" {
			t.Errorf("Model() = %q, want %q", got, "mock")
		}
	})

	t.Run("config present but malformed YAML → error", func(t *testing.T) {
		p := writeCfg(t, "provider: minimax\nunknown_field: bad\napi_key: \"x\"\n")
		if _, err := Resolve(p); err == nil {
			t.Fatal("want non-nil error for malformed config, got nil")
		}
	})
}

func TestResolveStrict(t *testing.T) {
	t.Run("config present and valid → real client (same as Resolve)", func(t *testing.T) {
		p := writeCfg(t, "provider: minimax\nmodel: strict-cfg\napi_key: \"x\"\n")
		client, err := ResolveStrict(p)
		if err != nil {
			t.Fatalf("want nil error, got: %v", err)
		}
		if got := client.Model(); got != "strict-cfg" {
			t.Errorf("Model() = %q, want %q", got, "strict-cfg")
		}
	})

	t.Run("config absent + env key set → real env client", func(t *testing.T) {
		t.Setenv("MINIMAX_API_KEY", "env-key")
		t.Setenv("MINIMAX_MODEL", "strict-env")
		client, err := ResolveStrict(noSuchPath(t))
		if err != nil {
			t.Fatalf("want nil error, got: %v", err)
		}
		if got := client.Model(); got != "strict-env" {
			t.Errorf("Model() = %q, want %q", got, "strict-env")
		}
	})

	t.Run("config absent + no env key → ERROR (no silent mock)", func(t *testing.T) {
		t.Setenv("MINIMAX_API_KEY", "")
		t.Setenv("MINIMAX_MODEL", "")
		if _, err := ResolveStrict(noSuchPath(t)); err == nil {
			t.Fatal("ResolveStrict must error when no real LLM available, got nil")
		}
	})
}
