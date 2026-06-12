# Adapter 接入指南 —— 三步零代码接入

[English](ADAPTER_GUIDE.md) | **简体中文**

本框架是 **schema 驱动**的：要支持一个新数据集，你**不写任何 Go**。接入 = 一份 `schema.yaml` + 一份 db 配置（+ 可选的 eval 任务 / `seed.yaml`）。框架自带的通用 runner（`cmd/init` / `cmd/etl` / `cmd/seed` / `cmd/agent` / `cmd/eval`）从你的 schema 推导出货币列、索引、`data_as_of`、PII 脱敏与就绪门控——adapter 不再是一段代码，而是几份声明式 YAML。

可跑的活证见 [`examples/toygame`](../examples/toygame)：它**删光了所有 Go**，只剩 `schema.yaml` + `seed.yaml` + eval 任务，照样跑通 seed → agent → eval。把它当作你自己 adapter 的模板。

三步：

1. **生成草稿**（`cmd/init` 内省你的 PG，吐出带 TODO 的接入包骨架）；
2. **标注业务知识**（补全 role/pii，再按业务补 glossary/buckets/etl_policy）；
3. **跑起来**（`cmd/etl` 真库或 `cmd/seed` 合成 → `cmd/agent` → `cmd/eval`）。

---

## 第 1 步 —— 生成草稿（`cmd/init`）

`cmd/init` **只读**内省你的生产 PG，按你点名的表生成一份接入包草稿：

```bash
go run github.com/RuntianLee/schema-driven-insight-agent/cmd/init@v0.2.0 \
  -db-config ./config/db.yaml \
  -tables player_basics \
  -domain my_game \
  -out ./my-adapter
```

产物清单（全部落在 `-out` 目录）：

| 文件 | 内容 |
|---|---|
| `schema.yaml` | schema 草稿——表/列结构已填，`role`/`pii` 是 **TODO 占位**待你标注。 |
| `db-config.example.yaml` | PG 连接配置样板（复制成真实 `db.yaml`，**绝不提交**）。 |
| `eval/tasks/example_task.yaml` | eval 任务骨架（含内联 `fixture:` 示例）。 |
| `seed.example.yaml` | `seed.yaml` 草稿（每个可物化列一个待填生成器）。 |
| `.gitignore` | 把 `db.yaml` / `data/` / `*.db` / `etl_health*.json` 排除入库。 |

内省**只读**：不写生产库、不打印 DSN（日志只出非密摘要）。真实 DSN 放在 gitignore 的 `db.yaml` 里。

## 第 2 步 —— 标注业务知识

草稿的 `role`/`pii` 是 `TODO` 占位。**草稿不可直接运行**：解析器拒绝任何残留 `TODO`，这是一道安全闸——你**不可能**忘标 PII 就把它物化进 Layer 2。完成全部 TODO 后，再按业务补 `glossary`、`glossary.buckets`、`derived_tables`（pivot）、`scope`、`etl_policy`。

最小化的 toygame 形态：

```yaml
version: 1
domain: idle_game_demo

etl_policy:
  hash_salt: toygame_demo_v0   # 仅示例，非真实 PII
  min_rows: 1
  health_min_rows: 100
  frozen: true                 # 合成快照，跳过失鲜检查

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

关键字段属性：

| 属性 | 含义 |
|---|---|
| `role` | 语义角色（`actor_id`、`level`、`balance`、`last_seen` …）。决定工具输出哪些列（例如只有 `role: balance` 才有总额/人均）。**草稿里是 TODO，必须标。** |
| `pii: true` | **绝不**物化进 Layer 2、绝不进 Digest、绝不可查。 |
| `omit_in_layer2: true` | 非 PII 但排除出快照（如内部列）。 |
| `index: true` | 在 Layer-2 该列建索引（物化列才允许；PII 列标 index 会被拒绝）。 |
| `glossary.buckets.<key>` | 分布分段。`max` 为**含上界**，升序；末档用 `max: null` 表示 +∞。 |

**PII 红线**：标了 `pii` 的字段在物化时被自动剔除——你**不可能**意外泄漏它。PII 在三个面用同一条规则把守：查询构造时拒绝、绝不物化进 Layer 2、绝不出现在喂给 LLM 的 schema Digest。解析器同时校验 bucket 单调性与列/role 白名单，并拒绝 PII 列标 `index`。

## 第 3 步 —— 跑起来（零 Go）

物化 Layer-2 快照（二选一），再跑 agent，可选跑 eval。两条 runner 都从 schema 推导一切，**无需任何 adapter 代码**。

### 物化 —— 真 Postgres（`cmd/etl`，只读）

```bash
go run github.com/RuntianLee/schema-driven-insight-agent/cmd/etl@v0.2.0 \
  -schema ./my-adapter/schema.yaml -db-config ./config/db.yaml
```

`cmd/etl` **只读**连接生产库，在 ETL（第 1 层）做脱敏（`HashPID` 哈希 PII），从 schema 推导货币列/索引/`data_as_of`，过一道行数安全闸门，并支持可选的声明式 `scope` 过滤（schema 顶层加 `scope:` 块限定分段）。Agent 永远看不到第 0/1 层——只看到产出的 Layer-2 SQLite。

### 物化 —— 合成数据（`cmd/seed`，无需任何数据库）

```bash
go run github.com/RuntianLee/schema-driven-insight-agent/cmd/seed@v0.2.0 \
  -schema ./my-adapter/schema.yaml -spec ./my-adapter/seed.yaml
```

`cmd/seed` 按声明式 `seed.yaml` 生成确定性合成快照（详见下文「seed.yaml 生成器参考」），镜像 `cmd/etl` 的就绪/health 语义。dev/demo 无真库即可体验全流程。

### 跑 Agent（`cmd/agent`，三个 env）

CLI（`cmd/agent`）读以下环境变量：

| env | 用途 |
|---|---|
| `SCHEMA_PATH` | 你的 `schema.yaml` 路径 |
| `SQLITE_PATH` | 你的 Layer-2 `.db` 路径 |
| `ETL_HEALTH_PATH` | 你的 `etl_health.json` 路径 |
| `TRAJECTORY_DB_PATH` | 运行轨迹的落库路径（可选） |

```bash
SCHEMA_PATH=./my-adapter/schema.yaml \
SQLITE_PATH=./my-adapter/data/my.db \
ETL_HEALTH_PATH=./my-adapter/data/etl_health.json \
go run ./cmd/agent -q "金币余额分布是怎样的？"
```

Agent 流程：校验就绪 → 把你的 schema 解析成 Digest → 把 `query_distribution` 工具调用经白名单 SQL 构造器路由 → 用主动洞察叙述分布。

### 跑 Eval（`cmd/eval`，可选门控）

```bash
go run ./cmd/eval \
  -schema ./my-adapter/schema.yaml \
  -tasks ./my-adapter/eval/tasks \
  -db ./my-adapter/data/my.db
```

## Eval 任务与内联 fixture

声明 benchmark 任务，让 CI 把关正确性。任务既可内联自带 `fixture:`（零代码 adapter，任务自给数据），也可对一份共享库跑（`-db`，toygame quickstart 形态）。

```yaml
id: coins_distribution
title: "金币余额分布"
question: "金币余额分布是怎样的？"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"player_currencies","column":"balance","bucket_key":"coins_balance"}}'
  - "金币集中在低区间：60% 玩家 ≤100。"
fixture:                       # 内联数据：任务自给，零外部库
  tables:
    player_currencies:
      groups:
        - {count: 600, values: {balance: 50}}
        - {count: 300, values: {balance: 500}}
        - {count: 80,  values: {balance: 5000}}
        - {count: 20,  values: {balance: 50000}}
evaluators:
  data_correctness:           # 确定性 —— 决定 CI 退出码
    tool: query_distribution
    expect_status: OK
    profile: {count: 1000}
    rows:
      - match: {bucket: "0~100"}
        expect: {player_count: 600}
  reasoning_quality: {rubric: "...", min_score: 3}   # LLM 评判 —— 仅报告
  insight_novelty:   {rubric: "...", min_score: 3}
```

数据源解析优先级：**任务内联 `fixture:` > Go 调用方注入的 `FixtureFunc` > `-db` 共享库**。`data_correctness` 把真实工具输出逐字段对比钉死值（确定性让它精确可断言），它决定 `cmd/eval` 的退出码（gate 失败 → 退出 1，可直接接入 CI）。LLM-judge 评测器（`reasoning_quality`/`insight_novelty`）给叙述质量打分，运行在报告模式。

mock 道（默认）无需 LLM key：agent 回放 `llm_turns`、judge 用 mock，`data_correctness` 完全确定性。真 LLM 道传 `-llm minimax -config config/llm.yaml`。

## seed.yaml 生成器参考

`seed.yaml` 声明每张表的行数与每个**物化列**的生成器（缺生成器的物化列会被拒）。三种生成器三选一：

```yaml
seed: 2026611          # 可选；固定 RNG 种子 → 同 spec 字节级可复现（缺省内置常量）
as_of: 1700000000      # 可选；last_seen 列不得超过它，且首行钉 = as_of（MAX 精确）
tables:
  player_basics:
    rows: 1000
    columns:
      last_login: {const: 1700000000}            # const：所有行同值
      coins:                                      # enum：加权离散值
        enum:
          - {value: 50,    weight: 600}
          - {value: 500,   weight: 300}
          - {value: 5000,  weight: 80}
          - {value: 50000, weight: 20}
      level:                                      # buckets：加权区间
        buckets:
          - {min: 1, max: 30}                     # 均匀
          - {min: 31, max: 60, weight: 2, skew: cube}    # skew: cube 偏 min（长尾）
          - {min: 61, max: 99, skew: recent}             # skew: recent 偏 max（近期在线/留存形态）
```

- **const**：所有行同一常量值。
- **enum**：加权离散取值；权重缺省 1。
- **buckets**：加权整数区间（含上下界）；`skew` 可选 `cube`（r³ 偏向 min，长尾近似）或 `recent`（1-(1-r)² 偏向 max）。
- **加权配额用最大余数法精确分配**：各档计数之和恒等于 `rows`，无误差——这正是 toygame 的 coins 能精确钉死 600/300/80/20 的原因。
- **每个物化列必须有生成器**（PII/omit 列不物化，无需声明）。
- `seed` 固定 RNG → 可复现；`as_of` 锚定 `last_seen`。

## etl_policy 参考

`etl_policy` 块（`cmd/etl`/`cmd/seed` 必需）六项语义：

| 字段 | 含义 |
|---|---|
| `hash_salt` | PII 哈希盐（`HashPID` 用）。明文盐，仅适合示例/合成。 |
| `hash_salt_env` | 从环境变量读盐；**设了则优先于 `hash_salt`**（生产用，盐不入库）。 |
| `min_rows` | ETL 行数安全闸门：物化行数低于它则失败（防空库/半截抽取）。 |
| `health_min_rows` | 写入 health 的就绪行数下限覆盖（示例数据集小 → 调低）。 |
| `frozen` | `true` = 冻结的合成/历史快照，Agent 启动跳过失鲜检查。 |
| `health_path` | `etl_health.json` 输出路径覆盖（相对 schema 目录；缺省 = db 同目录 `etl_health.json`）。 |

## 检查清单

- [ ] `schema.yaml`：全部 TODO 完成（**含 role/pii 标注**），含 `state_tables`、可选 pivot、`glossary.buckets`、`etl_policy`。
- [ ] `cmd/etl`（真库）或 `cmd/seed`（合成）跑出 Layer-2 SQLite **+ `etl_health.json`**。
- [ ] 用 `SCHEMA_PATH`/`SQLITE_PATH`/`ETL_HEALTH_PATH` 三个 env 指向你的 adapter，跑 `cmd/agent`。
- [ ] （可选）eval 任务（内联 `fixture:` 或 `-db` 共享库），做 `data_correctness` 门。

这就是全部契约。零 Go。
