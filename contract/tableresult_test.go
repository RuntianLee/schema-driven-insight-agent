package contract

import (
	"encoding/json"
	"testing"
)

func TestResponseTableMarshalOmitempty(t *testing.T) {
	b, _ := json.Marshal(Response{Status: StatusOK})
	if got := string(b); got != `{"status":"OK"}` {
		t.Fatalf("got %s want {\"status\":\"OK\"}", got)
	}
	r := Response{Status: StatusOK, Table: &TableResult{
		Columns:  []ColumnMeta{{Name: "server_id"}, {Name: "players"}},
		Rows:     [][]any{{int64(1), int64(150)}},
		RowCount: 1,
	}}
	b2, _ := json.Marshal(r)
	var back Response
	if err := json.Unmarshal(b2, &back); err != nil {
		t.Fatal(err)
	}
	if back.Table == nil || back.Table.RowCount != 1 || len(back.Table.Columns) != 2 {
		t.Fatalf("roundtrip lost Table: %+v", back.Table)
	}
}
