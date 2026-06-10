package llm

import (
	"testing"
)

// --- ParseMiniMaxConfig tests ---

func TestParseMiniMaxConfig_ValidAPIKeyEnvForm(t *testing.T) {
	raw := []byte(`
provider: minimax
endpoint: https://api.minimax.io/v1/text/chatcompletion_v2
model: MiniMax-M2.7
api_key_env: MINIMAX_API_KEY
timeout_seconds: 60
`)
	cfg, err := ParseMiniMaxConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Provider != "minimax" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "minimax")
	}
	if cfg.Endpoint != "https://api.minimax.io/v1/text/chatcompletion_v2" {
		t.Errorf("Endpoint = %q, want default URL", cfg.Endpoint)
	}
	if cfg.Model != "MiniMax-M2.7" {
		t.Errorf("Model = %q, want %q", cfg.Model, "MiniMax-M2.7")
	}
	if cfg.APIKeyEnv != "MINIMAX_API_KEY" {
		t.Errorf("APIKeyEnv = %q, want %q", cfg.APIKeyEnv, "MINIMAX_API_KEY")
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey should be empty, got %q", cfg.APIKey)
	}
	if cfg.TimeoutSeconds != 60 {
		t.Errorf("TimeoutSeconds = %d, want 60", cfg.TimeoutSeconds)
	}
}

func TestParseMiniMaxConfig_DefaultsApplied(t *testing.T) {
	// Completely empty YAML — all fields blank/zero.
	cfg, err := ParseMiniMaxConfig([]byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoint != minimaxEndpoint {
		t.Errorf("Endpoint default: got %q, want %q", cfg.Endpoint, minimaxEndpoint)
	}
	if cfg.Model != "MiniMax-M2.7" {
		t.Errorf("Model default: got %q, want %q", cfg.Model, "MiniMax-M2.7")
	}
	if cfg.TimeoutSeconds != 60 {
		t.Errorf("TimeoutSeconds default: got %d, want 60", cfg.TimeoutSeconds)
	}
}

func TestParseMiniMaxConfig_BlankEndpointAndModel(t *testing.T) {
	raw := []byte(`
provider: minimax
api_key_env: MINIMAX_API_KEY
`)
	cfg, err := ParseMiniMaxConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Endpoint != minimaxEndpoint {
		t.Errorf("Endpoint default: got %q, want %q", cfg.Endpoint, minimaxEndpoint)
	}
	if cfg.Model != "MiniMax-M2.7" {
		t.Errorf("Model default: got %q", cfg.Model)
	}
	if cfg.TimeoutSeconds != 60 {
		t.Errorf("TimeoutSeconds default: got %d", cfg.TimeoutSeconds)
	}
}

func TestParseMiniMaxConfig_InvalidYAML(t *testing.T) {
	_, err := ParseMiniMaxConfig([]byte(`{bad yaml :`))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// --- ResolveAPIKey tests ---

func TestResolveAPIKey_InlineWins(t *testing.T) {
	cfg := MiniMaxConfig{
		APIKey:    "inline-secret",
		APIKeyEnv: "SOME_ENV_VAR",
	}
	// Ensure the env var is set to something different — inline must win.
	t.Setenv("SOME_ENV_VAR", "env-secret")

	key, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "inline-secret" {
		t.Errorf("got %q, want inline-secret", key)
	}
}

func TestResolveAPIKey_EnvVarUsedWhenNoInline(t *testing.T) {
	cfg := MiniMaxConfig{
		APIKeyEnv: "TEST_MINIMAX_KEY_RESOLVE",
	}
	t.Setenv("TEST_MINIMAX_KEY_RESOLVE", "from-env-value")

	key, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "from-env-value" {
		t.Errorf("got %q, want from-env-value", key)
	}
}

func TestResolveAPIKey_NeitherSet_ReturnsError(t *testing.T) {
	cfg := MiniMaxConfig{
		// APIKey empty, APIKeyEnv empty → both paths fail
	}
	_, err := cfg.ResolveAPIKey()
	if err == nil {
		t.Fatal("expected error when neither api_key nor api_key_env is set, got nil")
	}
}

func TestResolveAPIKey_EnvVarNamedButEmpty_ReturnsError(t *testing.T) {
	cfg := MiniMaxConfig{
		APIKeyEnv: "TEST_MINIMAX_KEY_EMPTY",
	}
	t.Setenv("TEST_MINIMAX_KEY_EMPTY", "") // explicitly empty

	_, err := cfg.ResolveAPIKey()
	if err == nil {
		t.Fatal("expected error when named env var is empty, got nil")
	}
}

// --- NewMiniMaxFromConfig tests ---

func TestNewMiniMaxFromConfig_InlineKey(t *testing.T) {
	cfg := MiniMaxConfig{
		APIKey:         "sk-test-inline",
		Model:          "MiniMax-M2.7",
		Endpoint:       minimaxEndpoint,
		TimeoutSeconds: 30,
	}
	client, err := NewMiniMaxFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.Model() != "MiniMax-M2.7" {
		t.Errorf("Model() = %q, want MiniMax-M2.7", client.Model())
	}
}

func TestNewMiniMaxFromConfig_EnvKey(t *testing.T) {
	t.Setenv("TEST_MINIMAX_FROM_CONFIG_KEY", "sk-env-value")
	cfg := MiniMaxConfig{
		APIKeyEnv:      "TEST_MINIMAX_FROM_CONFIG_KEY",
		Model:          "MiniMax-M2.7",
		Endpoint:       minimaxEndpoint,
		TimeoutSeconds: 60,
	}
	client, err := NewMiniMaxFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.Model() != "MiniMax-M2.7" {
		t.Errorf("Model() = %q, want MiniMax-M2.7", client.Model())
	}
}

func TestNewMiniMaxFromConfig_NoKeyReturnsError(t *testing.T) {
	cfg := MiniMaxConfig{
		Model:          "MiniMax-M2.7",
		Endpoint:       minimaxEndpoint,
		TimeoutSeconds: 60,
	}
	_, err := NewMiniMaxFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error when no API key is available, got nil")
	}
}

func TestNewMiniMaxFromConfig_CustomEndpointPreserved(t *testing.T) {
	customEndpoint := "https://custom.minimax.example/v1/chat"
	cfg := MiniMaxConfig{
		APIKey:         "sk-test",
		Model:          "MiniMax-M2.7",
		Endpoint:       customEndpoint,
		TimeoutSeconds: 60,
	}
	client, err := NewMiniMaxFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the client is usable (Model() works) — endpoint is not exposed by
	// the Client interface but is used in Call; verified by examining minimaxClient.
	mc, ok := client.(*minimaxClient)
	if !ok {
		t.Fatal("client is not *minimaxClient")
	}
	if mc.endpoint != customEndpoint {
		t.Errorf("endpoint = %q, want %q", mc.endpoint, customEndpoint)
	}
}

func TestParseMiniMaxConfig_UnknownFieldRejected(t *testing.T) {
	// 手改配置打错字段名应当场报错，而非静默忽略（KnownFields strict mode）。
	raw := []byte("provider: minimax\napi_ky_env: MINIMAX_API_KEY\n")
	if _, err := ParseMiniMaxConfig(raw); err == nil {
		t.Fatal("expected error for unknown field 'api_ky_env', got nil")
	}
}

func TestParseMiniMaxConfig_EmptyDocUsesDefaults(t *testing.T) {
	// 空文档（Decoder 返回 io.EOF）应视为空配置并套用默认值，而非报错。
	cfg, err := ParseMiniMaxConfig([]byte("# only a comment\n"))
	if err != nil {
		t.Fatalf("empty doc must not error: %v", err)
	}
	if cfg.Endpoint == "" || cfg.Model != "MiniMax-M2.7" || cfg.TimeoutSeconds != 60 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}
