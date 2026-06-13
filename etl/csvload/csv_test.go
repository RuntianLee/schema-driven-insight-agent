package csvload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

func TestReadCSV(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.csv")
	os.WriteFile(p, []byte("A,B\n1,France\n2,Spain\n"), 0o644)
	header, recs, err := readCSV(p)
	if err != nil {
		t.Fatalf("readCSV: %v", err)
	}
	if len(header) != 2 || header[0] != "A" || header[1] != "B" {
		t.Fatalf("header = %v", header)
	}
	if len(recs) != 2 || recs[1][1] != "Spain" {
		t.Fatalf("recs = %v", recs)
	}
}

func TestCSVPathFromSchema(t *testing.T) {
	s := &schema_protocol.Schema{DataSources: map[string]schema_protocol.DataSource{
		"source": {Type: "csv", Path: "./data/churn.csv"},
		"layer2": {Type: "sqlite", Path: "./data/churn.db"},
	}}
	got, err := CSVPathFromSchema(s, "examples/bankchurn/schema.yaml")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != filepath.Join("examples/bankchurn", "data/churn.csv") {
		t.Fatalf("got %q", got)
	}
}
