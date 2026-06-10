package etl

import "testing"

func TestParseScope(t *testing.T) {
	withScope := []byte(`
version: 1
scope:
  filter_column: server_id
  server_ids: [101, 202]
state_tables: {}
`)
	sc, err := ParseScope(withScope)
	if err != nil {
		t.Fatalf("ParseScope: %v", err)
	}
	if sc == nil || sc.FilterColumn != "server_id" || len(sc.ServerIDs) != 2 || sc.ServerIDs[0] != 101 {
		t.Fatalf("unexpected scope: %+v", sc)
	}

	noScope := []byte("version: 1\nstate_tables: {}\n")
	sc2, err := ParseScope(noScope)
	if err != nil {
		t.Fatalf("ParseScope(noScope): %v", err)
	}
	if sc2 != nil {
		t.Fatalf("expected nil scope, got %+v", sc2)
	}

	badScope := []byte("scope:\n  server_ids: [1]\n") // 缺 filter_column
	if _, err := ParseScope(badScope); err == nil {
		t.Fatal("expected error for scope without filter_column")
	}
}

func TestWhereClause(t *testing.T) {
	var nilScope *Scope
	if w, args := nilScope.WhereClause(); w != "" || args != nil {
		t.Fatalf("nil scope must yield empty clause, got %q %v", w, args)
	}

	sc := &Scope{FilterColumn: "server_id", ServerIDs: []int64{101, 202}}
	w, args := sc.WhereClause()
	if w != " WHERE server_id IN ($1, $2)" {
		t.Fatalf("unexpected clause: %q", w)
	}
	if len(args) != 2 || args[0].(int64) != 101 || args[1].(int64) != 202 {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestBuildSelectQuery(t *testing.T) {
	noWhere := buildSelectQuery("player_basics", []string{"a", "b"}, "")
	if noWhere != "SELECT a, b FROM player_basics" {
		t.Fatalf("unexpected: %q", noWhere)
	}
	withWhere := buildSelectQuery("player_basics", []string{"a"}, " WHERE server_id IN ($1)")
	if withWhere != "SELECT a FROM player_basics WHERE server_id IN ($1)" {
		t.Fatalf("unexpected: %q", withWhere)
	}
}
