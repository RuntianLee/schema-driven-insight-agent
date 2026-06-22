package llm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// MiniMaxConfig holds all YAML-configurable parameters for the MiniMax provider.
// Convention: api_key_env names the env var that holds the secret; the committed
// YAML never contains a raw key. If api_key is set inline, config/llm.yaml MUST
// be gitignored (it already is by default).
type MiniMaxConfig struct {
	Provider       string   `yaml:"provider"`
	Endpoint       string   `yaml:"endpoint"`
	Model          string   `yaml:"model"`
	APIKeyEnv      string   `yaml:"api_key_env"`
	APIKey         string   `yaml:"api_key"` // inline, optional; never log this
	TimeoutSeconds int      `yaml:"timeout_seconds"`
	MaxTokens      int      `yaml:"max_tokens"`  // 0 = 不发（保持现状）；>0 封顶生成，防慢响应/截断
	Temperature    *float64 `yaml:"temperature"` // nil = 不发；设 0 = 确定性
}

// ParseMiniMaxConfig parses YAML bytes into MiniMaxConfig and applies defaults
// for any blank Endpoint, Model, or TimeoutSeconds fields.
func ParseMiniMaxConfig(b []byte) (MiniMaxConfig, error) {
	var cfg MiniMaxConfig
	// KnownFields(true): 拒绝未知字段，手改配置时打错字段名（如 api_ky_env）当场报错，而非静默忽略。
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	// 空文档时 Decode 返回 io.EOF（与 Unmarshal 不同），视为空配置、走默认值。
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return MiniMaxConfig{}, fmt.Errorf("parse llm config: %w", err)
	}
	// Apply defaults for blank fields.
	if cfg.Endpoint == "" {
		cfg.Endpoint = minimaxEndpoint
	}
	if cfg.Model == "" {
		cfg.Model = "MiniMax-M2.7"
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 60
	}
	return cfg, nil
}

// ResolveAPIKey returns the API key to use.
// Precedence: inline APIKey wins; otherwise reads the env var named by APIKeyEnv.
// Returns an error if neither yields a non-empty value.
// SECURITY: never log or print the returned key.
func (c MiniMaxConfig) ResolveAPIKey() (string, error) {
	if c.APIKey != "" {
		return c.APIKey, nil
	}
	if c.APIKeyEnv != "" {
		if v := os.Getenv(c.APIKeyEnv); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("minimax api key not found: set api_key or api_key_env")
}

// NewMiniMaxFromConfig resolves the API key and builds a Client using the
// endpoint, model, and timeout from cfg.
func NewMiniMaxFromConfig(cfg MiniMaxConfig) (Client, error) {
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	return newMiniMaxFull(key, cfg.Model, cfg.Endpoint, timeout, cfg.MaxTokens, cfg.Temperature), nil
}
