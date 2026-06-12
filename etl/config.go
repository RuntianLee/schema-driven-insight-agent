package etl

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// PGConfig holds all YAML-configurable parameters for the production Postgres connection.
// Convention: password_env names the env var that holds the secret; the committed
// example YAML never contains a raw password. If password is
// set inline, the config YAML MUST be gitignored (it already is).
type PGConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	User        string `yaml:"user"`
	DBName      string `yaml:"dbname"`
	SSLMode     string `yaml:"sslmode"`
	PasswordEnv string `yaml:"password_env"`
	Password    string `yaml:"password"` // inline, optional; NEVER log this field
}

// ParsePGConfig parses YAML bytes into PGConfig using KnownFields strict mode
// (unknown field names are rejected immediately, matching strict mode).
// An empty or comment-only document (io.EOF) is treated as an empty config with
// defaults applied — not an error.
// Default applied: Port → 5432 when zero.
func ParsePGConfig(b []byte) (PGConfig, error) {
	var cfg PGConfig
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	// 空文档时 Decode 返回 io.EOF（与 Unmarshal 不同），视为空配置、走默认值。
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return PGConfig{}, fmt.Errorf("parse pg config: %w", err)
	}
	// Apply defaults.
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	return cfg, nil
}

// DSN resolves the password and builds a percent-encoded postgres:// connection URL.
//
// Password precedence: inline Password if non-empty; else os.Getenv(PasswordEnv); else error.
// Requires Host, User, DBName non-empty (else error).
//
// SECURITY: NEVER log the password or the returned DSN anywhere.
func (c PGConfig) DSN() (string, error) {
	if c.Host == "" {
		return "", fmt.Errorf("pg config: host is required")
	}
	if c.User == "" {
		return "", fmt.Errorf("pg config: user is required")
	}
	if c.DBName == "" {
		return "", fmt.Errorf("pg config: dbname is required")
	}

	pw, err := c.resolvePassword()
	if err != nil {
		return "", err
	}

	port := c.Port
	if port == 0 {
		port = 5432
	}

	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, pw),
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(port)),
		Path:   "/" + c.DBName,
	}
	if c.SSLMode != "" {
		q := url.Values{}
		q.Set("sslmode", c.SSLMode)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// resolvePassword returns the password to use.
// Precedence: inline Password wins; otherwise reads the env var named by PasswordEnv.
// Returns an error if neither yields a non-empty value.
// SECURITY: never log or print the returned value.
func (c PGConfig) resolvePassword() (string, error) {
	if c.Password != "" {
		return c.Password, nil
	}
	if c.PasswordEnv != "" {
		if v := os.Getenv(c.PasswordEnv); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("db password not found: set password or password_env")
}

// ResolveDSNFromConfig 解析生产 PG 连接（td etl-runner 的三级解析上提为通用）：
//  1. cfgPath 文件存在 → 严格解析 + DSN()；任何错误硬失败（不静默回退）。
//  2. 文件不存在（os.ErrNotExist）且 dsnEnv 环境变量非空 → 用 env。
//  3. 都没有 → 带 setup 指引的错误。
//
// 返回 summary 为可安全打印的非密摘要。SECURITY: 返回的 dsn 绝不打印/入日志。
func ResolveDSNFromConfig(cfgPath, dsnEnv string) (dsn, summary string, err error) {
	raw, readErr := os.ReadFile(cfgPath)
	if readErr == nil {
		cfg, parseErr := ParsePGConfig(raw)
		if parseErr != nil {
			return "", "", fmt.Errorf("PG config parse error (%s): %w", cfgPath, parseErr)
		}
		d, dsnErr := cfg.DSN()
		if dsnErr != nil {
			return "", "", fmt.Errorf("PG config resolve error (%s): %w", cfgPath, dsnErr)
		}
		return d, fmt.Sprintf("host=%s port=%d db=%s user=%s (config: %s)",
			cfg.Host, cfg.Port, cfg.DBName, cfg.User, cfgPath), nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return "", "", fmt.Errorf("PG config unreadable (%s): %w", cfgPath, readErr)
	}
	if dsnEnv != "" {
		if v := os.Getenv(dsnEnv); v != "" {
			return v, fmt.Sprintf("(%s env)", dsnEnv), nil
		}
	}
	return "", "", fmt.Errorf("PG 未配置：创建 %s（参考 db-config.example.yaml）或设置 %s", cfgPath, dsnEnv)
}
