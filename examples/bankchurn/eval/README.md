# bankchurn AB reasoning 任务集 — 跨域 memory 泛化验证（公开可复现）

证 memory 机制（口径标签 facet 派生 + 软重排 + seed 覆盖）在**非游戏域**可移植：把 b3 上
reasoning +0.19 的跨任务泛化，升级为「跨域可移植」。

## 内容

- `tasks_ab_reasoning/`（3 train）/ `tasks_ab_reasoning_heldout/`（5 held-out）：mean / threshold /
  composite 三形状，「查询易、解读 rubric 苛刻」任务，**内联 `fixture:`**（跑 mock 道确定性验 golden，
  不碰真实 churn.db，无空结果风险）。
- `memory_seed/lessons.yaml`：3 条「可迁移解读方法教训」（银行域口径，去事实化）。

## 防泄漏作业（跨域结论可信度的关键）

- seed 由**独立 subagent** 仅凭 `tasks_ab_reasoning/`（3 train）+ `schema.yaml` **盲写**，
  禁读 held-out 与任何其他域（b3）的 seed；
- seed commit **早于**全部 held-out commit —— git 时间线即防泄漏证据
  （`train → seed 冻结 → held-out`）。

## 跑法

### 1) 确定性验 golden（mock 道，无需真 LLM，CI 同款）

```bash
# 在仓根
go run ./cmd/eval -llm mock -mode suite -adapter bankchurn \
  -schema examples/bankchurn/schema.yaml -tasks examples/bankchurn/eval/tasks_ab_reasoning
go run ./cmd/eval -llm mock -mode suite -adapter bankchurn \
  -schema examples/bankchurn/schema.yaml -tasks examples/bankchurn/eval/tasks_ab_reasoning_heldout
```
通过 = 每条 `data_correctness 1.00 ✓` + `GATE ... : PASS ✓`。
（mock 道下 reasoning_quality / answer_grounding 显示 `⚠below-min` 属预期，那两个 judge 仅在真 LLM A/B 道生效。）

### 2) 真 LLM A/B（off-gate，验跨域增益）

`<LLM_CONFIG>` 指向带 MiniMax 凭证的 provider YAML（如私有适配器仓 `config/llm.yaml`）。

```bash
# related 臂：建库灌 seed，读 held-out
go run ./cmd/memory -init -memory-db /tmp/bc_related.db
go run ./cmd/memory -memory-db /tmp/bc_related.db -adapter bankchurn \
  -manual examples/bankchurn/eval/memory_seed/lessons.yaml
go run ./cmd/eval -mode ab -llm minimax -runs 10 -reflexion-attempts 1 \
  -adapter bankchurn -schema examples/bankchurn/schema.yaml \
  -tasks examples/bankchurn/eval/tasks_ab_reasoning_heldout \
  -memory-db /tmp/bc_related.db -memory-limit 1 \
  -out /tmp/bc_related_out -config <LLM_CONFIG>

# empty 对照臂：纯空库
go run ./cmd/memory -init -memory-db /tmp/bc_empty.db
go run ./cmd/eval -mode ab -llm minimax -runs 10 -reflexion-attempts 1 \
  -adapter bankchurn -schema examples/bankchurn/schema.yaml \
  -tasks examples/bankchurn/eval/tasks_ab_reasoning_heldout \
  -memory-db /tmp/bc_empty.db -memory-limit 1 \
  -out /tmp/bc_empty_out -config <LLM_CONFIG>
```

**限流纪律**：串行、不并行多进程、judge `attempts=1`；卡住先查 `ps`（CPU≈0=卡网络）。
完整三阶梯（T1 冒烟 → T2 低成本全量 → T3 完整版）与结果见适配器仓
`docs/experiments/2026-06-23-bankchurn-cross-domain-memory.md`。

## 判据

reasoning related-B > empty-B（方向正）+ empty-B 不显著为正 + `off_facet=0` + `data_correctness` 零回归。
