package etl

import (
	"strings"
	"testing"
)

// --- ParsePGConfig tests ---

func TestParsePGConfig_ValidPasswordEnvForm(t *testing.T) {
	raw := []byte(`
host: db.prod.example.com
port: 5432
user: readonly
dbname: gamedb
sslmode: require
password_env: B3_PG_PASSWORD
`)
	cfg, err := ParsePGConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "db.prod.example.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "db.prod.example.com")
	}
	if cfg.Port != 5432 {
		t.Errorf("Port = %d, want 5432", cfg.Port)
	}
	if cfg.User != "readonly" {
		t.Errorf("User = %q, want %q", cfg.User, "readonly")
	}
	if cfg.DBName != "gamedb" {
		t.Errorf("DBName = %q, want %q", cfg.DBName, "gamedb")
	}
	if cfg.SSLMode != "require" {
		t.Errorf("SSLMode = %q, want %q", cfg.SSLMode, "require")
	}
	if cfg.PasswordEnv != "B3_PG_PASSWORD" {
		t.Errorf("PasswordEnv = %q, want %q", cfg.PasswordEnv, "B3_PG_PASSWORD")
	}
	if cfg.Password != "" {
		t.Errorf("Password should be empty, got non-empty")
	}
}

func TestParsePGConfig_PortDefaultApplied(t *testing.T) {
	// Port omitted → default 5432.
	raw := []byte(`
host: localhost
user: u
dbname: db
password_env: PG_PW
`)
	cfg, err := ParsePGConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 5432 {
		t.Errorf("Port default: got %d, want 5432", cfg.Port)
	}
}

func TestParsePGConfig_UnknownFieldRejected(t *testing.T) {
	// KnownFields strict: typo in field name must error.
	raw := []byte("host: localhost\npasswrod_env: B3_PG_PASSWORD\n")
	if _, err := ParsePGConfig(raw); err == nil {
		t.Fatal("expected error for unknown field 'passwrod_env', got nil")
	}
}

func TestParsePGConfig_EmptyDocNoError(t *testing.T) {
	// Empty / comment-only document: EOF treated as empty config, no error.
	cfg, err := ParsePGConfig([]byte("# only a comment\n"))
	if err != nil {
		t.Fatalf("empty doc must not error: %v", err)
	}
	// Port default should still be applied.
	if cfg.Port != 5432 {
		t.Errorf("Port default not applied on empty doc: got %d", cfg.Port)
	}
}

// --- PGConfig.DSN tests ---

func TestDSN_InlinePassword(t *testing.T) {
	cfg := PGConfig{
		Host:     "db.example.com",
		Port:     5432,
		User:     "readonly",
		DBName:   "gamedb",
		SSLMode:  "require",
		Password: "mysecret",
	}
	dsn, err := cfg.DSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must start with postgres:// and contain user, host, dbname, sslmode.
	if !strings.HasPrefix(dsn, "postgres://") {
		t.Errorf("DSN does not start with postgres://: %q", dsn)
	}
	if !strings.Contains(dsn, "db.example.com") {
		t.Errorf("DSN missing host: %q", dsn)
	}
	if !strings.Contains(dsn, "/gamedb") {
		t.Errorf("DSN missing dbname: %q", dsn)
	}
	if !strings.Contains(dsn, "sslmode=require") {
		t.Errorf("DSN missing sslmode: %q", dsn)
	}
}

func TestDSN_SpecialCharsPercentEncoded(t *testing.T) {
	// Password with @ : / characters must be percent-encoded, not literal.
	cfg := PGConfig{
		Host:     "db.example.com",
		Port:     5432,
		User:     "readonly",
		DBName:   "gamedb",
		Password: "p@ss:w/rd",
	}
	dsn, err := cfg.DSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify special chars are NOT literally present in the user-info portion.
	// The user-info is the part before the @ in the authority (before the host).
	// Extract user-info: between "postgres://" and "@".
	withoutScheme := strings.TrimPrefix(dsn, "postgres://")
	atIdx := strings.LastIndex(withoutScheme, "@")
	if atIdx < 0 {
		t.Fatalf("DSN has no @ separator: %q", dsn)
	}
	userInfo := withoutScheme[:atIdx]
	// The raw special chars must not appear literally in user-info.
	if strings.Contains(userInfo, "p@ss:w/rd") {
		t.Errorf("password with special chars is NOT percent-encoded in user-info: %q", userInfo)
	}
	// The encoded DSN should contain the percent-encoded form.
	// '@' encodes to '%40', ':' after user is delim but in password encodes to '%3A', '/' encodes to '%2F'.
	if !strings.Contains(dsn, "%40") {
		t.Errorf("DSN missing %%40 (encoded @) in password: %q", dsn)
	}
}

func TestDSN_PasswordEnvUsed(t *testing.T) {
	t.Setenv("B3_PG_TEST_PW", "env-password")
	cfg := PGConfig{
		Host:        "db.example.com",
		Port:        5432,
		User:        "readonly",
		DBName:      "gamedb",
		PasswordEnv: "B3_PG_TEST_PW",
	}
	dsn, err := cfg.DSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(dsn, "postgres://") {
		t.Errorf("DSN does not start with postgres://: %q", dsn)
	}
}

func TestDSN_NeitherPasswordSet_Error(t *testing.T) {
	cfg := PGConfig{
		Host:   "db.example.com",
		Port:   5432,
		User:   "readonly",
		DBName: "gamedb",
		// No Password, no PasswordEnv.
	}
	_, err := cfg.DSN()
	if err == nil {
		t.Fatal("expected error when neither password nor password_env is set, got nil")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("error message should mention 'password': %v", err)
	}
}

func TestDSN_MissingHost_Error(t *testing.T) {
	cfg := PGConfig{
		// Host empty
		Port:     5432,
		User:     "readonly",
		DBName:   "gamedb",
		Password: "pw",
	}
	_, err := cfg.DSN()
	if err == nil {
		t.Fatal("expected error for missing host, got nil")
	}
}

func TestDSN_MissingUser_Error(t *testing.T) {
	cfg := PGConfig{
		Host:     "db.example.com",
		Port:     5432,
		DBName:   "gamedb",
		Password: "pw",
	}
	_, err := cfg.DSN()
	if err == nil {
		t.Fatal("expected error for missing user, got nil")
	}
}

func TestDSN_MissingDBName_Error(t *testing.T) {
	cfg := PGConfig{
		Host:     "db.example.com",
		Port:     5432,
		User:     "readonly",
		Password: "pw",
	}
	_, err := cfg.DSN()
	if err == nil {
		t.Fatal("expected error for missing dbname, got nil")
	}
}

func TestDSN_DefaultPort5432WhenZero(t *testing.T) {
	cfg := PGConfig{
		Host:     "db.example.com",
		Port:     0, // zero → default 5432
		User:     "readonly",
		DBName:   "gamedb",
		Password: "pw",
	}
	dsn, err := cfg.DSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(dsn, ":5432") {
		t.Errorf("DSN should contain default port :5432: %q", dsn)
	}
}

func TestDSN_NoSSLModeOmitQueryParam(t *testing.T) {
	cfg := PGConfig{
		Host:     "db.example.com",
		Port:     5432,
		User:     "readonly",
		DBName:   "gamedb",
		Password: "pw",
		SSLMode:  "", // blank → omit sslmode param entirely
	}
	dsn, err := cfg.DSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(dsn, "sslmode") {
		t.Errorf("DSN should not contain sslmode when SSLMode is empty: %q", dsn)
	}
}

func TestDSN_InlinePasswordWinsOverEnv(t *testing.T) {
	t.Setenv("B3_PG_TEST_PW2", "env-password")
	cfg := PGConfig{
		Host:        "db.example.com",
		Port:        5432,
		User:        "readonly",
		DBName:      "gamedb",
		Password:    "inline-wins",
		PasswordEnv: "B3_PG_TEST_PW2",
	}
	dsn, err := cfg.DSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// inline-wins must be used (encoded as-is since no special chars).
	if !strings.Contains(dsn, "inline-wins") {
		t.Errorf("inline password should take precedence; DSN does not contain it: %q", dsn)
	}
}

func TestDSN_PasswordEnvNamedButUnset_Error(t *testing.T) {
	// password_env 指向一个未设置的环境变量 → 应报错，不得静默构造空密码 DSN。
	cfg := PGConfig{Host: "h", User: "u", DBName: "d", PasswordEnv: "B3_PG_PASSWORD_DEFINITELY_UNSET_X9Z"}
	if _, err := cfg.DSN(); err == nil {
		t.Fatal("expected error when password_env names an unset/empty var")
	}
}
