# Adapter Guide — write your own data adapter

**English** | [简体中文](ADAPTER_GUIDE.zh-CN.md)

This framework is **schema-driven**: to support a new dataset you write one `schema.yaml` and a thin adapter that produces a local SQLite snapshot. The agent core stays untouched. This guide walks through it using the runnable [`examples/toygame`](../examples/toygame) adapter as the scaffold — typically **under 200 lines** total.

An adapter is a **separate Go module** that depends on this framework and provides three things:

1. a `schema.yaml` describing your data,
2. a way to materialize a **Layer-2 SQLite** snapshot (synthetic seed, or a real read-only ETL),
3. (optional) eval tasks that gate correctness.

---

## 1. Declare your data: `schema.yaml`

The schema is the **only** place business knowledge lives. Minimal toygame example:

```yaml
version: 1
domain: idle_game_demo

data_sources:
  layer2:
    type: sqlite
    path: ./data/toygame.db

state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:  {type: int64, role: actor_id, pk: true, pii: true}
      level:      {type: int32, role: level}
      coins:      {type: int64, role: balance, currency_type: coins}
      last_login: {type: unix_timestamp_seconds, role: last_seen}

derived_tables:
  player_currencies:                # a "pivot" of balance columns into long form
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind, glossary_key: currency_types}
      balance:       {type: int64,  role: balance}

glossary:
  currency_types:
    coins: "in-game coins (demo)"
  buckets:
    coins_balance:                  # distribution segments for the `balance` column
      - {min: 0,     max: 100,   label: "0~100"}
      - {min: 101,   max: 1000,  label: "101~1k"}
      - {min: 1001,  max: 10000, label: "1k~1w"}
      - {min: 10001, max: null,  label: "1w+"}   # null max = +∞ (last bucket)
```

Key field attributes:

| attribute | meaning |
|---|---|
| `role` | semantic tag (`actor_id`, `level`, `balance`, `last_seen`, …). Drives which output columns the tool emits (e.g. only `role: balance` gets value totals/averages). |
| `pii: true` | **never** materialized to Layer 2, never in the Digest, never queryable. |
| `omit_in_layer2: true` | non-PII but excluded from the snapshot (e.g. internal columns). |
| `glossary.buckets.<key>` | distribution segments. `max` is the inclusive upper bound; ascending; the last bucket uses `max: null` for +∞. |

The parser validates bucket monotonicity and the column/role whitelist. A field marked `pii` is dropped from materialization automatically — you cannot accidentally leak it.

## 2. Materialize a Layer-2 snapshot

You produce a SQLite file with your `state_tables` (non-PII columns) plus, if you declared a pivot, the derived currencies table. Two paths:

### Path A — synthetic seed (what toygame uses; no database needed)

Reuse the framework's loaders. Sketch from [`examples/toygame/seed/seed.go`](../examples/toygame/seed/seed.go):

```go
import (
    fetl "github.com/RuntianLee/schema-driven-insight-agent/etl"
    "github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

func Seed(dbPath, schemaPath string) (int64, error) {
    raw, _ := os.ReadFile(schemaPath)
    s, _ := schema_protocol.Parse(raw)
    cols, _ := fetl.BasicsColumns(s, "player_basics") // non-PII cols, sorted

    var basics [][]any            // rows in `cols` order
    var currencies []fetl.CurrencyRow
    // ... generate deterministic synthetic rows ...

    if err := fetl.LoadBasics(basics, cols, "player_basics", dbPath, []string{"level"}); err != nil {
        return 0, err
    }
    // LoadCurrencies also stamps _meta.schema_version (the agent checks this on startup)
    if err := fetl.LoadCurrencies(currencies, "player_currencies", dbPath, s.Version); err != nil {
        return 0, err
    }
    return int64(len(basics)), nil
}
```

Then write an `etl_health.json` so the agent's startup gate passes — see [`examples/toygame/cmd/seed/main.go`](../examples/toygame/cmd/seed/main.go):

```go
import "github.com/RuntianLee/schema-driven-insight-agent/etl_health"

etl_health.Write(healthPath, etl_health.Health{
    Status:          etl_health.StatusOK,
    Rows:            n,
    FinishedAt:      time.Now(),
    SchemaVersion:   1,
    Frozen:          true,        // a frozen synthetic snapshot skips the staleness check
    MinRowsOverride: &minRows,    // small example dataset → lower the readiness floor
})
```

### Path B — real Postgres ETL (read-only)

For a real dataset, your adapter connects to production **read-only** and de-identifies in the ETL (Layer 1). The `framework/etl` package has pgx-based helpers (`ExtractBasics`, `ExtractCurrencies`, `LoadBasics`, `LoadCurrencies`, `HashPID` for PII hashing) plus a row-count safety gate and an optional declarative `scope` filter (restrict to certain segments via a `scope:` block in `schema.yaml`). The agent never sees Layer 0/1 — only the resulting Layer-2 SQLite. Keep your real DSN in a gitignored config; never commit it.

## 3. Run the agent

The CLI (`cmd/agent`) reads these env vars (with example-relative defaults):

| env | purpose |
|---|---|
| `SCHEMA_PATH` | path to your `schema.yaml` |
| `SQLITE_PATH` | path to your Layer-2 `.db` |
| `ETL_HEALTH_PATH` | path to your `etl_health.json` |
| `TRAJECTORY_DB_PATH` | where run trajectories are recorded |

```bash
SCHEMA_PATH=./my-adapter/schema.yaml \
SQLITE_PATH=./my-adapter/data/my.db \
ETL_HEALTH_PATH=./my-adapter/data/etl_health.json \
go run ./cmd/agent -q "what does the coin balance distribution look like?"
```

The agent: validates readiness → parses your schema into a Digest → routes `query_distribution` tool calls through the whitelisted SQL builder → narrates the distribution with proactive insight.

## 4. (Optional) Eval tasks

Declare benchmark tasks so CI can gate correctness. From [`examples/toygame/eval/tasks/coins_distribution.yaml`](../examples/toygame/eval/tasks/coins_distribution.yaml):

```yaml
id: coins_distribution
title: "coin balance distribution"
question: "what does the coin balance distribution look like?"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"player_currencies","column":"balance","bucket_key":"coins_balance"}}'
  - "Coins concentrate in the low tier: 60% of players hold ≤100."
evaluators:
  data_correctness:           # deterministic — gate-able in CI
    tool: query_distribution
    expect_status: OK
    profile: {count: 1000}
    rows:
      - match: {bucket: "0~100"}
        expect: {player_count: 600}
  reasoning_quality: {rubric: "...", min_score: 3}   # LLM-judge — report only
  insight_novelty:   {rubric: "...", min_score: 3}
```

`data_correctness` compares the real tool output field-by-field against pinned values (a deterministic seed makes this exact). The LLM-judge evaluators score narration quality and run in report mode.

Run the suite with `cmd/eval` (exit code 1 when the gate fails — wire it straight into CI):

```bash
go run ./cmd/eval \
  -schema examples/toygame/schema.yaml \
  -tasks examples/toygame/eval/tasks \
  -db examples/toygame/data/toygame.db
```

The mock lane (default) needs no LLM key: the agent replays `llm_turns` and the judge is mocked, so `data_correctness` is fully deterministic. Pass `-llm minimax -config config/llm.yaml` for the real-LLM lane.

## Checklist

- [ ] `schema.yaml` with `state_tables` (mark PII!), optional pivot, and `glossary.buckets`.
- [ ] A seed or ETL that writes the Layer-2 SQLite (reuse `etl.LoadBasics`/`LoadCurrencies`).
- [ ] An `etl_health.json` so startup readiness passes.
- [ ] Run `cmd/agent` with `SCHEMA_PATH`/`SQLITE_PATH`/`ETL_HEALTH_PATH` pointing at your adapter.
- [ ] (Optional) eval tasks for a `data_correctness` gate.

That's the whole contract. Copy `examples/toygame`, swap the schema, point the agent at it.
