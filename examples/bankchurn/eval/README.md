# bankchurn AB reasoning 任务集 + memory seed

这组任务用于 `cmd/eval` 的真 LLM A/B 道（`-mode ab`，off-gate），度量 reflection memory 对
解读质量（`reasoning_quality`）的影响；以 bankchurn（Kaggle 银行客户流失，CC0）为非游戏域示例。

## 内容

- `tasks_ab_reasoning/`（3 train）/ `tasks_ab_reasoning_heldout/`（5 held-out）：mean / threshold /
  composite 三形状的「查询易、解读 rubric 苛刻」任务，**内联 `fixture:`**（跑确定性 mock 道即可验
  golden，不依赖真实 churn.db）。held-out 覆盖 CreditScore / EstimatedSalary / Tenure 等列。
- `memory_seed/lessons.yaml`：3 条可迁移的解读方法教训（银行域口径，去事实化）。
  seed **仅依据 train 任务 + schema 撰写、不参考 held-out**（见 commit 历史：seed 早于全部 held-out）。

## 跑法

### 确定性验 golden（无需真 LLM）

```bash
go run ./cmd/eval -llm mock -mode suite -adapter bankchurn \
  -schema examples/bankchurn/schema.yaml -tasks examples/bankchurn/eval/tasks_ab_reasoning
go run ./cmd/eval -llm mock -mode suite -adapter bankchurn \
  -schema examples/bankchurn/schema.yaml -tasks examples/bankchurn/eval/tasks_ab_reasoning_heldout
```
每条应 `data_correctness 1.00 ✓` + `GATE ... : PASS ✓`。
（mock 道下 `reasoning_quality` / `answer_grounding` 显示 `⚠below-min` 属预期，那两个 judge 仅在真 LLM A/B 道生效。）

### 真 LLM A/B（off-gate）

建 related 库灌 seed，读 held-out；对照纯空库 empty 臂。`<LLM_CONFIG>` 指向带 provider 凭证的 YAML。

```bash
go run ./cmd/memory -init -memory-db /tmp/bc_related.db
go run ./cmd/memory -memory-db /tmp/bc_related.db -adapter bankchurn \
  -manual examples/bankchurn/eval/memory_seed/lessons.yaml
go run ./cmd/eval -mode ab -llm minimax -runs 10 -reflexion-attempts 1 \
  -adapter bankchurn -schema examples/bankchurn/schema.yaml \
  -tasks examples/bankchurn/eval/tasks_ab_reasoning_heldout \
  -memory-db /tmp/bc_related.db -memory-limit 1 -out /tmp/bc_related_out -config <LLM_CONFIG>
```
（A/B 永不进 CI 退出码；真 LLM 跑批建议串行、`-reflexion-attempts 1`。）
