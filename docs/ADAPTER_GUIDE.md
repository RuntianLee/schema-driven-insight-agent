# Adapter Guide — three-step, zero-code onboarding

**English** | [简体中文](ADAPTER_GUIDE.zh-CN.md)

This framework is **schema-driven**: to support a new dataset you write **no Go at all**. Onboarding = one `schema.yaml` + a db config (+ optional eval tasks / `seed.yaml`). The bundled generic runners (`cmd/init` / `cmd/etl` / `cmd/seed` / `cmd/agent` / `cmd/eval`) derive currency columns, indexes, `data_as_of`, PII de-identification, and the readiness gate from your schema — an adapter is no longer a chunk of code, just a few declarative YAML files.

The living proof is [`examples/toygame`](../examples/toygame): it has **deleted all of its Go**, keeping only `schema.yaml` + `seed.yaml` + eval tasks, and still runs seed → agent → eval end-to-end. Use it as the template for your own adapter.

Three steps:

1. **Generate a draft** (`cmd/init` introspects your PG and emits an onboarding package skeleton with TODOs);
2. **Annotate business knowledge** (complete role/pii, then fill glossary/buckets/etl_policy by domain);
3. **Run it** (`cmd/etl` for a real DB or `cmd/seed` for synthetic → `cmd/agent` → `cmd/eval`).

---

## Step 1 — Generate a draft (`cmd/init`)

`cmd/init` **read-only** introspects your production PG and generates an onboarding package draft for the tables you name:

```bash
go run github.com/RuntianLee/schema-driven-insight-agent/cmd/init@v0.7.0 \
  -db-config ./config/db.yaml \
  -tables player_basics \
  -domain my_game \
  -out ./my-adapter
```

Output manifest (all under `-out`):

| File | Contents |
|---|---|
| `schema.yaml` | Schema draft — table/column structure filled in, `role`/`pii` are **TODO placeholders** for you to annotate. |
| `db-config.example.yaml` | PG connection config template (copy to a real `db.yaml`, **never commit it**). |
| `eval/tasks/example_task.yaml` | Eval task skeleton (with an inline `fixture:` example). |
| `seed.example.yaml` | `seed.yaml` draft (one generator stub per materializable column). |
| `.gitignore` | Excludes `db.yaml` / `data/` / `*.db` / `etl_health*.json` from the repo. |

Introspection is **read-only**: it never writes to production and never prints the DSN (logs emit only a non-secret summary). The real DSN lives in the gitignored `db.yaml`.

## Step 2 — Annotate business knowledge

The draft's `role`/`pii` are `TODO` placeholders. **The draft is not runnable as-is**: the parser rejects any leftover `TODO` — this is a safety gate, so you **cannot** forget to mark PII and then materialize it into Layer 2. Once every TODO is done, fill in `glossary`, `glossary.buckets`, `derived_tables` (pivot), `scope`, and `etl_policy` per your domain.

The minimal toygame shape:

```yaml
version: 1
domain: idle_game_demo

etl_policy:
  hash_salt: toygame_demo_v0   # example only, not real PII
  min_rows: 1
  health_min_rows: 100
  frozen: true                 # synthetic snapshot, skip staleness check

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
      level:      {type: int32, role: level, index: true}
      coins:      {type: int64, role: balance, currency_type: coins}
      last_login: {type: unix_timestamp_seconds, role: last_seen}

derived_tables:
  player_currencies:                # pivot balance columns into a long table
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind, glossary_key: currency_types}
      balance:       {type: int64,  role: balance}

glossary:
  currency_types:
    coins: "in-game coins (example)"
  buckets:
    coins_balance:                  # distribution segments for the `balance` column
      - {min: 0,     max: 100,   label: "0~100"}
      - {min: 101,   max: 1000,  label: "101~1k"}
      - {min: 1001,  max: 10000, label: "1k~1w"}
      - {min: 10001, max: null,  label: "1w+"}   # max: null means +∞ (last bucket)
```

Key field attributes:

| Attribute | Meaning |
|---|---|
| `role` | Semantic role (`actor_id`, `level`, `balance`, `last_seen`, …). Decides which columns the tool outputs (e.g. only `role: balance` yields total/per-capita). **It's a TODO in the draft — you must annotate it.** |
| `pii: true` | **Never** materialized into Layer 2, never in the Digest, never queryable. |
| `omit_in_layer2: true` | Non-PII but excluded from the snapshot (e.g. internal columns). |
| `index: true` | Build an index on this Layer-2 column (materialized columns only; marking a PII column `index` is rejected). |
| `glossary.buckets.<key>` | Distribution segments. `max` is an **inclusive** upper bound, ascending; the last bucket uses `max: null` for +∞. |

**PII red line**: fields marked `pii` are automatically stripped at materialization — you **cannot** leak them by accident. PII is guarded on three faces with one rule: rejected at query build time, never materialized into Layer 2, never present in the schema Digest fed to the LLM. The parser also validates bucket monotonicity and the column/role whitelist, and rejects PII columns marked `index`.

### Optional — Advisor operational playbook

To use the two-agent pipeline (`cmd/agent -advise`, and the `advisor_grounding` eval gate below), add an optional **top-level** `advisor.playbook` block — free-text operational guidance the framework passes **opaquely** to the Advisor agent (it is never parsed for business meaning):

```yaml
advisor:
  playbook: |
    High-churn segment (e.g. a region/age band well above the mean): investigate that
    segment's product experience and acquisition quality first; assess targeted retention.
    All recommendations are drafts — validate with the business before acting.
```

The Advisor consumes **only** the Analyst's structured output plus this playbook — never the raw data (a multi-agent boundary enforced at compile time) — and emits draft recommendations, each `source_ref`-traceable to a specific analyst result. Omit the block entirely if you don't use the Advisor; a bare `advisor:` key with no `playbook` is rejected (same three-state rule as `etl_policy`).

## Step 3 — Run it (zero Go)

Materialize a Layer-2 snapshot (pick one), then run the agent, optionally run eval. Both runners derive everything from the schema — **no adapter code required**.

### Materialize — real Postgres (`cmd/etl`, read-only)

```bash
go run github.com/RuntianLee/schema-driven-insight-agent/cmd/etl@v0.7.0 \
  -schema ./my-adapter/schema.yaml -db-config ./config/db.yaml
```

`cmd/etl` connects **read-only** to production, de-identifies in the ETL (Layer 1) (`HashPID` hashes PII), derives currency columns/indexes/`data_as_of` from the schema, passes a row-count safety gate, and supports an optional declarative `scope` filter (add a top-level `scope:` block to scope to segments). The agent never sees Layer 0/1 — only the produced Layer-2 SQLite.

### Materialize — synthetic data (`cmd/seed`, no database needed)

```bash
go run github.com/RuntianLee/schema-driven-insight-agent/cmd/seed@v0.7.0 \
  -schema ./my-adapter/schema.yaml -spec ./my-adapter/seed.yaml
```

`cmd/seed` generates a deterministic synthetic snapshot from a declarative `seed.yaml` (see "seed.yaml generator reference" below), mirroring `cmd/etl`'s readiness/health semantics. For dev/demo you can experience the full flow with no real DB.

### Bring your own CSV (zero Go)

Have a CSV (a Kaggle export, a report dump) instead of a live Postgres? The CSV is treated as a Layer-1 source and de-identified into Layer-2 exactly like the Postgres path.

1. Write `schema.yaml`: declare a `data_sources` entry `{type: csv, path: ./your.csv}` plus the `layer2` sqlite output, map each CSV header to a field (mark identifiers `pii: true` / `omit_in_layer2: true`), and — if your CSV has no timestamp column — set `etl_policy.data_as_of` to a static snapshot time.
2. Build: `go run ./cmd/csv -schema path/to/schema.yaml`. actor_id columns are hashed, `omit_in_layer2` columns dropped.
3. Connect exactly like any adapter: point `cmd/agent` / `cmd/eval` at the produced db.

See [`examples/bankchurn`](../examples/bankchurn) for a complete worked example (Kaggle Bank Customer Churn, CC0, 10k rows).

### Run the agent (`cmd/agent`, three envs)

The CLI (`cmd/agent`) reads these environment variables:

| env | Purpose |
|---|---|
| `SCHEMA_PATH` | Path to your `schema.yaml` |
| `SQLITE_PATH` | Path to your Layer-2 `.db` |
| `ETL_HEALTH_PATH` | Path to your `etl_health.json` |
| `TRAJECTORY_DB_PATH` | Where run trajectories are persisted (optional) |

```bash
SCHEMA_PATH=./my-adapter/schema.yaml \
SQLITE_PATH=./my-adapter/data/my.db \
ETL_HEALTH_PATH=./my-adapter/data/etl_health.json \
go run ./cmd/agent -q "What's the coin-balance distribution?"
```

Agent flow: verify readiness → parse your schema into a Digest → route the `query_distribution` tool call through the whitelisted SQL builder → narrate the distribution with proactive insight.

#### Two-agent mode (`-advise`)

Add `-advise` to run the Analyst → Advisor pipeline: the Analyst runs once, its structured results are handed to the Advisor (using your `advisor.playbook`), and the draft recommendations are rendered after the answer. Needs a real LLM (`config/llm.yaml`); with the mock fallback the Advisor has nothing to draft from.

```bash
SCHEMA_PATH=./my-adapter/schema.yaml \
SQLITE_PATH=./my-adapter/data/my.db \
ETL_HEALTH_PATH=./my-adapter/data/etl_health.json \
go run ./cmd/agent -advise -q "Which segments churn most, and what should we do?"
```

### Run eval (`cmd/eval`, optional gate)

```bash
go run ./cmd/eval \
  -schema ./my-adapter/schema.yaml \
  -tasks ./my-adapter/eval/tasks \
  -db ./my-adapter/data/my.db
```

## Eval tasks and inline fixtures

Declare benchmark tasks so CI gates correctness. A task can either carry an inline `fixture:` (zero-code adapter, the task supplies its own data) or run against a shared DB (`-db`, the toygame quickstart shape).

```yaml
id: coins_distribution
title: "coin-balance distribution"
question: "What's the coin-balance distribution?"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"player_currencies","column":"balance","bucket_key":"coins_balance"}}'
  - "Coins concentrate in the low range: 60% of players hold ≤100."
fixture:                       # inline data: self-supplied, no external DB
  tables:
    player_currencies:
      groups:
        - {count: 600, values: {balance: 50}}
        - {count: 300, values: {balance: 500}}
        - {count: 80,  values: {balance: 5000}}
        - {count: 20,  values: {balance: 50000}}
evaluators:
  data_correctness:           # deterministic — decides the CI exit code
    tool: query_distribution
    expect_status: OK
    profile: {count: 1000}
    rows:
      - match: {bucket: "0~100"}
        expect: {player_count: 600}
  reasoning_quality: {rubric: "...", min_score: 3}   # LLM judge — report only
  insight_novelty:   {rubric: "...", min_score: 3}
```

Data-source resolution priority: **inline `fixture:` > Go-injected `FixtureFunc` > `-db` shared DB**. `data_correctness` compares the real tool output field-by-field against pinned values (determinism makes it exactly assertable), and it decides `cmd/eval`'s exit code (gate fail → exit 1, drop-in for CI). The LLM-judge evaluators (`reasoning_quality`/`insight_novelty`) score narrative quality in report mode.

For insight tasks where the Analyst makes quantitative claims, list `attribution_grounding: {}` — a **deterministic gate** that resolves the Analyst's self-produced attribution block (each `claim → anchor → claimed_value`) against the real tool cells and fails if any claim is unresolvable or mismatched (so it joins `data_correctness`/`advisor_grounding` in the CI exit code). Pair it with `claim_coverage: {}`, an **off-gate** LLM signal that reports what fraction of the narrative's quantitative claims were actually declared in the attribution block. Both skip cleanly when the Analyst emits no attribution block (backward-compatible).

The mock lane (default) needs no LLM key: the agent replays `llm_turns`, the judge uses a mock, and `data_correctness` is fully deterministic. For the real-LLM lane pass `-llm minimax -config config/llm.yaml`.

### Advisor grounding gate (two-agent tasks)

If your `schema.yaml` declares an `advisor.playbook`, a task can opt into the Analyst→Advisor relay by listing the **`advisor_grounding`** evaluator — a second **deterministic gate** (alongside `data_correctness`) that checks every drafted recommendation's `source_ref` traces back to a real analyst result, and that at least `min_items` recommendations were produced. In the mock lane the task supplies the Advisor's scripted draft via a top-level **`advisor_turn`** field; the real-LLM lane drives the Advisor with the configured model (and `advisor_turn` is ignored).

```yaml
id: advisor_retention
question: "Which markets churn most, and how should we respond?"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"customers","column":"Exited","group_by":["Geography"]}}'
  - "Germany churns ~32%, well above France/Spain (~16%)."
advisor_turn: |
  {"summary":"Per-market retention (draft)","items":[
    {"observation":"Germany churn ~32%, well above other markets","source_ref":"q1","action":"Investigate German product experience and acquisition quality; assess targeted retention","priority":"high","caveat":"Draft — validate with the business"}
  ]}
evaluators:
  advisor_grounding: {min_items: 1}            # deterministic — also a CI gate
  reasoning_quality: {rubric: "...", min_score: 3}
```

`source_ref` values are `q1, q2, …` (the 1-based index of the Analyst's tool calls). See `examples/bankchurn/eval/tasks/advisor_retention.yaml` and `examples/toygame/eval/tasks/advisor_churn.yaml` for the two bundled cross-domain benchmarks.

## seed.yaml generator reference

`seed.yaml` declares each table's row count and a generator for every **materialized column** (a materialized column with no generator is rejected). Pick exactly one of three generators:

```yaml
seed: 2026611          # optional; fixes the RNG seed → byte-reproducible per spec (built-in constant by default)
as_of: 1700000000      # optional; last_seen columns may not exceed it, and the first row is pinned = as_of (exact MAX)
tables:
  player_basics:
    rows: 1000
    columns:
      last_login: {const: 1700000000}            # const: same value on every row
      coins:                                      # enum: weighted discrete values
        enum:
          - {value: 50,    weight: 600}
          - {value: 500,   weight: 300}
          - {value: 5000,  weight: 80}
          - {value: 50000, weight: 20}
      level:                                      # buckets: weighted ranges
        buckets:
          - {min: 1, max: 30}                     # uniform
          - {min: 31, max: 60, weight: 2, skew: cube}    # skew: cube biases toward min (long tail)
          - {min: 61, max: 99, skew: recent}             # skew: recent biases toward max (recency/retention shape)
```

- **const**: same constant value on every row.
- **enum**: weighted discrete values; weight defaults to 1.
- **buckets**: weighted integer ranges (inclusive bounds); optional `skew` is `cube` (r³ biases toward min, long-tail approximation) or `recent` (1-(1-r)² biases toward max).
- **Weighted quotas use the largest-remainder method for exact allocation**: per-bucket counts sum exactly to `rows`, no drift — this is why toygame's coins pin exactly to 600/300/80/20.
- **Every materialized column needs a generator** (PII/omit columns aren't materialized, so they need none).
- `seed` fixes the RNG → reproducible; `as_of` anchors `last_seen`.

## etl_policy reference

The `etl_policy` block (required by `cmd/etl`/`cmd/seed`) has six semantics:

| Field | Meaning |
|---|---|
| `hash_salt` | PII hashing salt (used by `HashPID`). A plaintext salt, fit only for examples/synthetic. |
| `hash_salt_env` | Read the salt from an env var; **takes precedence over `hash_salt` when set** (for production, salt stays out of the repo). |
| `min_rows` | ETL row-count safety gate: fails if materialized rows fall below it (guards against an empty DB / half-finished extract). |
| `health_min_rows` | Readiness row-count floor override written into health (small example dataset → lower it). |
| `frozen` | `true` = a frozen synthetic/historical snapshot; the agent skips the staleness check at startup. |
| `health_path` | `etl_health.json` output path override (relative to the schema dir; default = `etl_health.json` next to the db). |

## Checklist

- [ ] `schema.yaml`: every TODO done (**including role/pii annotations**), with `state_tables`, optional pivot, `glossary.buckets`, `etl_policy`.
- [ ] `cmd/etl` (real DB) or `cmd/seed` (synthetic) produces a Layer-2 SQLite **+ `etl_health.json`**.
- [ ] Run `cmd/agent` with the three envs `SCHEMA_PATH`/`SQLITE_PATH`/`ETL_HEALTH_PATH` pointed at your adapter.
- [ ] (Optional) eval tasks (inline `fixture:` or `-db` shared DB) for a `data_correctness` gate.
- [ ] (Optional, two-agent) `advisor.playbook` in `schema.yaml` + tasks with the `advisor_grounding` gate (and `advisor_turn` for the mock lane).

That's the whole contract. Zero Go.
