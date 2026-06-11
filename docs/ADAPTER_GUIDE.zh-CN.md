# Adapter 接入指南 —— 写你自己的数据 adapter

[English](ADAPTER_GUIDE.md) | **简体中文**

本框架是 **schema 驱动**的:要支持一个新数据集,你只需写一份 `schema.yaml` + 一个薄 adapter,产出一份本地 SQLite 快照,Agent 核心完全不用动。本指南以可跑的 [`examples/toygame`](../examples/toygame) 为脚手架带你走一遍——通常**不到 200 行**。

一个 adapter 是一个**独立的 Go module**,依赖本框架,提供三样东西:

1. 一份描述你数据的 `schema.yaml`;
2. 一种把数据物化成 **Layer-2 SQLite** 快照的方式(合成 seed,或真实只读 ETL);
3. (可选)把关正确性的 eval 任务。

---

## 1. 声明你的数据:`schema.yaml`

schema 是业务知识**唯一**的栖身之地。最小化的 toygame 示例:

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
  player_currencies:                # 把余额列 pivot 成长表
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind, glossary_key: currency_types}
      balance:       {type: int64,  role: balance}

glossary:
  currency_types:
    coins: "游戏金币（示例）"
  buckets:
    coins_balance:                  # `balance` 列的分布分段
      - {min: 0,     max: 100,   label: "0~100"}
      - {min: 101,   max: 1000,  label: "101~1k"}
      - {min: 1001,  max: 10000, label: "1k~1w"}
      - {min: 10001, max: null,  label: "1w+"}   # max: null 表示 +∞（末档）
```

关键字段属性:

| 属性 | 含义 |
|---|---|
| `role` | 语义角色（`actor_id`、`level`、`balance`、`last_seen` …）。决定工具输出哪些列（例如只有 `role: balance` 才有总额/人均）。 |
| `pii: true` | **绝不**物化进 Layer 2、绝不进 Digest、绝不可查。 |
| `omit_in_layer2: true` | 非 PII 但排除出快照（如内部列）。 |
| `glossary.buckets.<key>` | 分布分段。`max` 为**含上界**,升序;末档用 `max: null` 表示 +∞。 |

解析器会校验 bucket 单调性与列/role 白名单。标了 `pii` 的字段在物化时被自动剔除——你**不可能**意外泄漏它。

## 2. 物化一份 Layer-2 快照

你要产出一个 SQLite 文件,含你的 `state_tables`(非 PII 列),以及——如果你声明了 pivot——派生的 currencies 表。两条路:

### 路 A —— 合成 seed(toygame 用的方式;无需任何数据库)

复用框架的 loader。摘自 [`examples/toygame/seed/seed.go`](../examples/toygame/seed/seed.go):

```go
import (
    fetl "github.com/RuntianLee/schema-driven-insight-agent/etl"
    "github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

func Seed(dbPath, schemaPath string) (int64, error) {
    raw, _ := os.ReadFile(schemaPath)
    s, _ := schema_protocol.Parse(raw)
    cols, _ := fetl.BasicsColumns(s, "player_basics") // 非 PII 列，已排序

    var basics [][]any            // 行，按 `cols` 顺序
    var currencies []fetl.CurrencyRow
    // ... 生成确定性合成行 ...

    if err := fetl.LoadBasics(basics, cols, "player_basics", dbPath, []string{"level"}); err != nil {
        return 0, err
    }
    // LoadCurrencies 同时戳 _meta.schema_version（Agent 启动时会校验）
    if err := fetl.LoadCurrencies(currencies, "player_currencies", dbPath, s.Version); err != nil {
        return 0, err
    }
    return int64(len(basics)), nil
}
```

然后写一份 `etl_health.json`,让 Agent 的启动门控通过——见 [`examples/toygame/cmd/seed/main.go`](../examples/toygame/cmd/seed/main.go):

```go
import "github.com/RuntianLee/schema-driven-insight-agent/etl_health"

etl_health.Write(healthPath, etl_health.Health{
    Status:          etl_health.StatusOK,
    Rows:            n,
    FinishedAt:      time.Now(),
    SchemaVersion:   1,
    Frozen:          true,        // 冻结的合成快照，跳过失鲜检查
    MinRowsOverride: &minRows,    // 示例数据集小 → 调低就绪行数下限
})
```

### 路 B —— 真 Postgres ETL(只读)

对真实数据集,你的 adapter **只读**连接生产库,并在 ETL(第 1 层)做脱敏。`framework/etl` 包提供 pgx 辅助(`ExtractBasics`、`ExtractCurrencies`、`LoadBasics`、`LoadCurrencies`、用于 PII 哈希的 `HashPID`),外加一道行数安全闸门,以及一个可选的声明式 `scope` 过滤(在 `schema.yaml` 顶层加 `scope:` 块,把范围限定到某些分段)。Agent 永远看不到第 0/1 层——只看到产出的 Layer-2 SQLite。真实 DSN 放在 gitignore 的 config 里,**绝不提交**。

## 3. 跑 Agent

CLI(`cmd/agent`)读以下环境变量(默认值相对示例):

| env | 用途 |
|---|---|
| `SCHEMA_PATH` | 你的 `schema.yaml` 路径 |
| `SQLITE_PATH` | 你的 Layer-2 `.db` 路径 |
| `ETL_HEALTH_PATH` | 你的 `etl_health.json` 路径 |
| `TRAJECTORY_DB_PATH` | 运行轨迹的落库路径 |

```bash
SCHEMA_PATH=./my-adapter/schema.yaml \
SQLITE_PATH=./my-adapter/data/my.db \
ETL_HEALTH_PATH=./my-adapter/data/etl_health.json \
go run ./cmd/agent -q "金币余额分布是怎样的？"
```

Agent 流程:校验就绪 → 把你的 schema 解析成 Digest → 把 `query_distribution` 工具调用经白名单 SQL 构造器路由 → 用主动洞察叙述分布。

## 4. (可选)Eval 任务

声明 benchmark 任务,让 CI 把关正确性。摘自 [`examples/toygame/eval/tasks/coins_distribution.yaml`](../examples/toygame/eval/tasks/coins_distribution.yaml):

```yaml
id: coins_distribution
title: "金币余额分布"
question: "金币余额分布是怎样的？"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"player_currencies","column":"balance","bucket_key":"coins_balance"}}'
  - "金币集中在低区间：60% 玩家 ≤100。"
evaluators:
  data_correctness:           # 确定性 —— 可在 CI 把关
    tool: query_distribution
    expect_status: OK
    profile: {count: 1000}
    rows:
      - match: {bucket: "0~100"}
        expect: {player_count: 600}
  reasoning_quality: {rubric: "...", min_score: 3}   # LLM 评判 —— 仅报告
  insight_novelty:   {rubric: "...", min_score: 3}
```

`data_correctness` 把真实工具输出逐字段对比钉死值(确定性 seed 让它精确可断言)。LLM-judge 评测器给叙述质量打分,运行在报告模式。

## 检查清单

- [ ] `schema.yaml`:含 `state_tables`(**标好 PII!**)、可选 pivot、`glossary.buckets`。
- [ ] 一个 seed 或 ETL,写出 Layer-2 SQLite(复用 `etl.LoadBasics`/`LoadCurrencies`)。
- [ ] 一份 `etl_health.json`,让启动就绪通过。
- [ ] 用 `SCHEMA_PATH`/`SQLITE_PATH`/`ETL_HEALTH_PATH` 指向你的 adapter,跑 `cmd/agent`。
- [ ] (可选)eval 任务,做 `data_correctness` 门。

这就是全部契约。复制 `examples/toygame`,换掉 schema,把 Agent 指过去。
