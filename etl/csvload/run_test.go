package csvload

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const testSchema = `
version: 1
domain: bank_churn
etl_policy:
  hash_salt: test_salt_v0
  min_rows: 1
  health_min_rows: 1
  frozen: true
  data_as_of: 1704067200
data_sources:
  source: {type: csv, path: ./bank.csv}
  layer2: {type: sqlite, path: ./bank.db}
state_tables:
  customers:
    nature: snapshot
    primary_key: [CustomerId]
    fields:
      CustomerId:  {type: int64,  role: actor_id, pk: true, pii: true}
      Surname:     {type: string, role: actor_name, pii: true, omit_in_layer2: true}
      Geography:   {type: string, role: dimension, index: true}
      Age:         {type: int32,  role: level, index: true}
      Balance:     {type: int64,  role: balance, currency_type: account}
      Exited:      {type: int32,  role: churn_flag, index: true}
derived_tables:
  customer_balances:
    derived_from: customers
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind}
      balance:       {type: int64,  role: balance}
`

const testCSV = `CustomerId,Surname,Geography,Age,Balance,Exited
15634602,Hargrave,France,42,0.00,1
15647311,Hill,Spain,41,83807.86,0
15619304,Onio,France,42,159660.80,1
`

func TestRun_BuildsDeidentifiedLayer2(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.yaml")
	os.WriteFile(schemaPath, []byte(testSchema), 0o644)
	os.WriteFile(filepath.Join(dir, "bank.csv"), []byte(testCSV), 0o644)

	if err := Run(RunOptions{SchemaPath: schemaPath}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "bank.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	db.QueryRow("SELECT COUNT(*) FROM customers").Scan(&n)
	if n != 3 {
		t.Fatalf("customers rows = %d, want 3", n)
	}
	if _, err := db.Query("SELECT Surname FROM customers"); err == nil {
		t.Errorf("Surname 不应物化（omit_in_layer2）")
	}
	if _, err := db.Query("SELECT CustomerId FROM customers"); err == nil {
		t.Errorf("CustomerId 不应物化（pii）")
	}

	var bal int64
	db.QueryRow("SELECT Balance FROM customers WHERE Age=41").Scan(&bal)
	if bal != 83808 {
		t.Errorf("Balance = %d, want 83808", bal)
	}

	var pid string
	db.QueryRow("SELECT player_id FROM customer_balances LIMIT 1").Scan(&pid)
	if pid == "15634602" || pid == "" {
		t.Errorf("player_id 应为脱敏 hash，得 %q", pid)
	}
	db.QueryRow("SELECT COUNT(*) FROM customer_balances").Scan(&n)
	if n != 3 {
		t.Errorf("customer_balances rows = %d, want 3", n)
	}

	var asOf string
	db.QueryRow("SELECT value FROM _meta WHERE key='data_as_of'").Scan(&asOf)
	if asOf != "1704067200" {
		t.Errorf("data_as_of = %q, want 1704067200", asOf)
	}

	if _, err := os.Stat(filepath.Join(dir, "etl_health.json")); err != nil {
		t.Errorf("health 未写: %v", err)
	}
}
