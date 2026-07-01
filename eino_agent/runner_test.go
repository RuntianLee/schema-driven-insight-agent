package eino_agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/prompts"
)

// stubDispatcher 按 tool 名返回预设 Response，并记录收到的 args。
type stubDispatcher struct {
	resp     map[string]contract.Response
	lastArgs map[string]any
}

func (d *stubDispatcher) Dispatch(_ context.Context, name string, args map[string]any) (contract.Response, error) {
	d.lastArgs = args
	if r, ok := d.resp[name]; ok {
		return r, nil
	}
	return contract.Response{Status: contract.StatusSchemaError, Hint: "unknown"}, nil
}

func newTestRunner(m *fakeModel, disp agent.ToolDispatcher, rec *fakeRecorder) *Runner {
	opener := func(context.Context, string, string) (agent.TrajectoryStore, error) { return rec, nil }
	return New(m, "MiniMax-M2.7", disp, opener, "", NonInteractiveClarifier{})
}

func TestRun_SingleToolThenAnswer(t *testing.T) {
	m := &fakeModel{responses: []*schema.Message{
		asMsg(10, 5, tc("call_1", "query_distribution", `{"table":"player_currencies","column":"balance"}`)),
		finalMsg("最终洞察：分布如下。"),
	}}
	disp := &stubDispatcher{resp: map[string]contract.Response{"query_distribution": {Status: contract.StatusOK}}}
	rec := &fakeRecorder{}
	ans, err := newTestRunner(m, disp, rec).Run(context.Background(), "余额怎么分布？")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans != "最终洞察：分布如下。" {
		t.Fatalf("answer=%q", ans)
	}
	if rec.outcome != "success" {
		t.Fatalf("outcome=%q want success", rec.outcome)
	}
	if len(rec.toolCalls) != 1 || rec.toolCalls[0] != "query_distribution" {
		t.Fatalf("toolCalls=%v", rec.toolCalls)
	}
}

func TestRun_ArgsReachDispatcher(t *testing.T) {
	m := &fakeModel{responses: []*schema.Message{
		asMsg(1, 1, tc("c", "analyze", `{"table":"t1","limit":5}`)),
		finalMsg("done"),
	}}
	disp := &stubDispatcher{resp: map[string]contract.Response{"analyze": {Status: contract.StatusOK}}}
	_, err := newTestRunner(m, disp, &fakeRecorder{}).Run(context.Background(), "q")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if disp.lastArgs["table"] != "t1" {
		t.Fatalf("args did not plumb through: %v", disp.lastArgs)
	}
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestParseToolCall verifies that parseToolCall is tolerant of trailing content
// after the first JSON object (fences, markers, prose) while still rejecting
// pure natural-language answers and stray non-tool objects.
func TestParseToolCall(t *testing.T) {
	type want struct {
		isTool bool
		tool   string // checked only when isTool==true
	}
	cases := []struct {
		name  string
		input string
		want  want
	}{
		{
			name:  "plain one-line JSON",
			input: `{"tool":"query_distribution","args":{"table":"player_currencies"}}`,
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "wrapped in [TOOL_CALL] markers",
			input: "[TOOL_CALL]\n{\"tool\":\"query_distribution\",\"args\":{}}\n[/TOOL_CALL]",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "wrapped non-strict tool call keys",
			input: "[TOOL_CALL]\n{tool: \"analyze\", args: {\"table\": \"player_basics\", \"aggregates\": [{\"fn\": \"count\", \"as\": \"n\"}]}}\n[/TOOL_CALL]",
			want:  want{isTool: true, tool: "analyze"},
		},
		{
			name: "minimax xml invoke args",
			input: `<minimax:tool_call>
<invoke name="analyze">
<parameter name="args">{"aggregates":[{"as":"n","fn":"count"}],"group_by":["server_id"],"table":"player_basics"}</parameter>
</invoke>
</minimax:tool_call>`,
			want: want{isTool: true, tool: "analyze"},
		},
		{
			name:  "markdown fenced block",
			input: "```json\n{\"tool\":\"query_distribution\",\"args\":{}}\n```",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "leading prose then JSON",
			input: "我来查询。\n{\"tool\":\"query_distribution\",\"args\":{}}",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "JSON then trailing prose",
			input: "{\"tool\":\"query_distribution\",\"args\":{}}\n这是我的查询。",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "pure NL final answer (no brace)",
			input: "## 报告\n头部 0.35% 持有 21.62%",
			want:  want{isTool: false},
		},
		{
			name:  "NL containing a stray non-tool object",
			input: "参考 {\"bucket\":\"0~1w\"} 的数据",
			want:  want{isTool: false},
		},
		{
			name:  "nested args object parses fully",
			input: `{"tool":"query_distribution","args":{"filter":{"currency_type":"coins"},"bucket_key":"coins_balance"}}`,
			want:  want{isTool: true, tool: "query_distribution"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, isTool := parseToolCall(tc.input)
			if isTool != tc.want.isTool {
				t.Fatalf("isTool = %v, want %v (input: %q)", isTool, tc.want.isTool, tc.input)
			}
			if tc.want.isTool && got.Tool != tc.want.tool {
				t.Fatalf("tool = %q, want %q", got.Tool, tc.want.tool)
			}
			// Case 8: additionally verify nested args parsed
			if tc.name == "nested args object parses fully" {
				filter, ok := got.Args["filter"].(map[string]any)
				if !ok {
					t.Fatalf("args[filter] is not a map: %T", got.Args["filter"])
				}
				if filter["currency_type"] != "coins" {
					t.Fatalf("args[filter][currency_type] = %v, want coins", filter["currency_type"])
				}
			}
		})
	}
}

// Verify the llm.NewMock-based substring routing works for standalone use (not the cumulative runner).
func TestMockLLM_SubstringRouting_Standalone(t *testing.T) {
	mock := llm.NewMock(map[string]string{
		"运营问题":         `{"tool":"query_distribution","args":{}}`,
		"player_count": "最终报告：ROI 信号显著。",
	})
	ctx := context.Background()
	// Call with only one matching key at a time — no ambiguity.
	r1, _, _, _, _ := mock.Call(ctx, "运营问题：当前货币分布如何？")
	if !strings.Contains(r1, "query_distribution") {
		t.Fatalf("turn1 routing: %s", r1)
	}
	r2, _, _, _, _ := mock.Call(ctx, `工具返回 [{"player_count":100}]`)
	if !strings.Contains(r2, "ROI") {
		t.Fatalf("turn2 routing: %s", r2)
	}
}


// TestSystemPrompt_QIDCopyGuidance：归因规范须指引「抄印出的结果 id、不要自己数」，
// 并说明数字列下标可用。(b') 修复的 prompt 兜底。
func TestSystemPrompt_QIDCopyGuidance(t *testing.T) {
	p := prompts.SystemV0
	for _, want := range []string{"抄", "结果 id", "数字下标"} {
		if !strings.Contains(p, want) {
			t.Errorf("system prompt 归因规范缺 %q", want)
		}
	}
}

// TestParseMinimaxPerParameter 验证 MiniMax 原生逐参数 XML（无 name="args" 整块）被解析，
// 复杂类型值按 JSON 解码，多 invoke 取第一个，args-blob 既有形态回归。
func TestParseMinimaxPerParameter(t *testing.T) {
	t.Run("per-parameter typed values", func(t *testing.T) {
		input := `<minimax:tool_call>
<invoke name="analyze">
<parameter name="table">player_basics</parameter>
<parameter name="group_by">["server_id"]</parameter>
<parameter name="aggregates">[{"as":"n","fn":"count"}]</parameter>
</invoke>
</minimax:tool_call>`
		got, ok := parseToolCall(input)
		if !ok {
			t.Fatal("expected tool call, got none")
		}
		if got.Tool != "analyze" {
			t.Fatalf("tool = %q, want analyze", got.Tool)
		}
		if got.Args["table"] != "player_basics" {
			t.Fatalf("table = %v, want player_basics (string scalar)", got.Args["table"])
		}
		gb, ok := got.Args["group_by"].([]any)
		if !ok || len(gb) != 1 || gb[0] != "server_id" {
			t.Fatalf("group_by = %v, want [server_id] ([]any)", got.Args["group_by"])
		}
		aggs, ok := got.Args["aggregates"].([]any)
		if !ok || len(aggs) != 1 {
			t.Fatalf("aggregates not parsed as array: %v", got.Args["aggregates"])
		}
	})

	t.Run("multiple invokes takes first", func(t *testing.T) {
		input := `<minimax:tool_call>
<invoke name="query_distribution">
<parameter name="table">player_currencies</parameter>
</invoke>
<invoke name="analyze">
<parameter name="table">player_basics</parameter>
</invoke>
</minimax:tool_call>`
		got, ok := parseToolCall(input)
		if !ok || got.Tool != "query_distribution" {
			t.Fatalf("got (%q,%v), want first invoke query_distribution", got.Tool, ok)
		}
	})

	t.Run("args-blob regression still works", func(t *testing.T) {
		input := `<minimax:tool_call>
<invoke name="analyze">
<parameter name="args">{"table":"player_basics","aggregates":[{"as":"n","fn":"count"}]}</parameter>
</invoke>
</minimax:tool_call>`
		got, ok := parseToolCall(input)
		if !ok || got.Tool != "analyze" {
			t.Fatalf("got (%q,%v), want analyze via args-blob", got.Tool, ok)
		}
		if got.Args["table"] != "player_basics" {
			t.Fatalf("args-blob table = %v, want player_basics", got.Args["table"])
		}
	})

	t.Run("invoke without parameters is not a tool call", func(t *testing.T) {
		input := `<minimax:tool_call><invoke name="analyze"></invoke></minimax:tool_call>`
		if _, ok := parseToolCall(input); ok {
			t.Fatal("invoke with no <parameter> should not parse as tool call")
		}
	})

	t.Run("numeric-looking string not silently truncated", func(t *testing.T) {
		input := `<minimax:tool_call><invoke name="analyze"><parameter name="note">42abc</parameter><parameter name="n">42</parameter></invoke></minimax:tool_call>`
		got, ok := parseToolCall(input)
		if !ok {
			t.Fatal("expected tool call")
		}
		if got.Args["note"] != "42abc" {
			t.Fatalf("note = %v (%T), want string \"42abc\" (no silent truncation)", got.Args["note"], got.Args["note"])
		}
		if got.Args["n"] != float64(42) {
			t.Fatalf("n = %v (%T), want float64(42)", got.Args["n"], got.Args["n"])
		}
	})
}

// TestParseOpenAIJSON 验证 OpenAI 式 {name, arguments/parameters/input} 被解析，
// arguments 支持对象与 JSON 字符串两种形态，含 tool 键则让位给项目格式。
func TestParseOpenAIJSON(t *testing.T) {
	cases := []struct {
		name, input, wantTool string
		wantTable             string
	}{
		{"name+arguments object", `{"name":"analyze","arguments":{"table":"player_basics"}}`, "analyze", "player_basics"},
		{"name+parameters object", `{"name":"analyze","parameters":{"table":"pb"}}`, "analyze", "pb"},
		{"name+arguments string-encoded", `{"name":"analyze","arguments":"{\"table\":\"pc\"}"}`, "analyze", "pc"},
		{"name+input object", `{"name":"analyze","input":{"table":"pi"}}`, "analyze", "pi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseToolCall(tc.input)
			if !ok || got.Tool != tc.wantTool {
				t.Fatalf("got (%q,%v), want %q", got.Tool, ok, tc.wantTool)
			}
			if got.Args["table"] != tc.wantTable {
				t.Fatalf("table = %v, want %v", got.Args["table"], tc.wantTable)
			}
		})
	}

	t.Run("project format defers to project detector", func(t *testing.T) {
		// {tool,args} 含 tool 键 → OpenAI 探测器让位、由项目探测器解。
		got, ok := parseToolCall(`{"tool":"query_distribution","args":{"table":"x"}}`)
		if !ok || got.Tool != "query_distribution" {
			t.Fatalf("got (%q,%v), want query_distribution", got.Tool, ok)
		}
	})

	t.Run("object without name or tool is not a tool call", func(t *testing.T) {
		if _, ok := parseToolCall(`参考 {"bucket":"0~1w"} 的数据`); ok {
			t.Fatal("stray object without name/tool should not parse")
		}
	})

	t.Run("name null or non-string is not a tool call", func(t *testing.T) {
		for _, in := range []string{`{"name":null,"arguments":{}}`, `{"name":42,"arguments":{}}`} {
			if _, ok := parseToolCall(in); ok {
				t.Fatalf("input %q: non-string name should not parse", in)
			}
		}
	})
	t.Run("arguments null yields empty args not nil", func(t *testing.T) {
		got, ok := parseToolCall(`{"name":"analyze","arguments":null}`)
		if !ok || got.Tool != "analyze" {
			t.Fatalf("got (%q,%v), want analyze", got.Tool, ok)
		}
		if got.Args == nil {
			t.Fatal("Args should be empty map, not nil")
		}
		got.Args["x"] = 1 // 不能 panic
	})
}

// TestParseTaggedJSON 验证家族B：<tool_call>{json}</tool_call> 与 [TOOL_CALLS][{json}]，
// 内层单对象或数组取第一；散文里夹标记但内无合法工具对象不得误报。
func TestParseTaggedJSON(t *testing.T) {
	t.Run("hermes tool_call object", func(t *testing.T) {
		got, ok := parseToolCall(`<tool_call>{"name":"analyze","arguments":{"table":"pb"}}</tool_call>`)
		if !ok || got.Tool != "analyze" || got.Args["table"] != "pb" {
			t.Fatalf("got (%q,%v) args=%v", got.Tool, ok, got.Args)
		}
	})
	t.Run("mistral TOOL_CALLS array first", func(t *testing.T) {
		got, ok := parseToolCall(`[TOOL_CALLS][{"name":"query_distribution","arguments":{"table":"pc"}}]`)
		if !ok || got.Tool != "query_distribution" || got.Args["table"] != "pc" {
			t.Fatalf("got (%q,%v) args=%v", got.Tool, ok, got.Args)
		}
	})
	t.Run("mistral TOOL_CALLS array multi takes first", func(t *testing.T) {
		got, ok := parseToolCall(`[TOOL_CALLS][{"name":"first","arguments":{"table":"a"}},{"name":"second","arguments":{"table":"b"}}]`)
		if !ok || got.Tool != "first" || got.Args["table"] != "a" {
			t.Fatalf("got (%q,%v) args=%v, want first/a", got.Tool, ok, got.Args)
		}
	})
	t.Run("prose mentioning tool_call tag does not false-positive", func(t *testing.T) {
		if _, ok := parseToolCall("可以用 <tool_call> 这种标签来调用工具，但这只是说明。"); ok {
			t.Fatal("prose with empty/no-json <tool_call> mention should not parse")
		}
	})
}

// TestDetectorOrderingAndGuards 锁定探测器优先级与负向防误报，作回归守护。
func TestDetectorOrderingAndGuards(t *testing.T) {
	t.Run("xml preferred over inner json brace", func(t *testing.T) {
		// 逐参数 XML 内含 JSON 值，必须走家族C（tool=analyze），不被裸 { 抢成 JSON。
		input := `<minimax:tool_call><invoke name="analyze"><parameter name="group_by">["server_id"]</parameter></invoke></minimax:tool_call>`
		got, ok := parseToolCall(input)
		if !ok || got.Tool != "analyze" {
			t.Fatalf("got (%q,%v), want analyze via C", got.Tool, ok)
		}
	})
	t.Run("pure NL no brace stays final answer", func(t *testing.T) {
		if _, ok := parseToolCall("## 报告\n头部 0.35% 持有 21.62%"); ok {
			t.Fatal("pure NL should not parse as tool call")
		}
	})
	t.Run("project format unaffected by openai detector", func(t *testing.T) {
		got, ok := parseToolCall(`{"tool":"analyze","args":{"table":"pb"}}`)
		if !ok || got.Tool != "analyze" || got.Args["table"] != "pb" {
			t.Fatalf("got (%q,%v) args=%v, want project-format analyze", got.Tool, ok, got.Args)
		}
	})
}

func TestRun_DedupEmitsCachedToolResult(t *testing.T) {
	// 模型两轮发同一查询；第二次应被 dedup 拦截（dispatch 只发生一次），第三轮给答案。
	model := &fakeModel{responses: []*schema.Message{
		asMsg(10, 5, tc("c1", "query_distribution", `{"table":"t","column":"c"}`)),
		asMsg(10, 5, tc("c2", "query_distribution", `{"table":"t","column":"c"}`)),
		finalMsg("答案"),
	}}
	disp := &stubDispatcher{resp: map[string]contract.Response{"query_distribution": {Status: contract.StatusOK}}}
	rec := &fakeRecorder{}
	ans, err := newTestRunner(model, disp, rec).Run(context.Background(), "q")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans != "答案" {
		t.Fatalf("answer=%q", ans)
	}
	// dispatch 只发生一次（第二次被 dedup 拦截，不重跑不录）。
	if len(rec.toolCalls) != 1 {
		t.Fatalf("expect 1 real dispatch, got %d (%v)", len(rec.toolCalls), rec.toolCalls)
	}
}

func TestRun_MultiToolCallsPerTurn(t *testing.T) {
	// 一轮两个不同 tool_use，都 OK：两次 dispatch，各配 tool_result。
	model := &fakeModel{responses: []*schema.Message{
		asMsg(10, 5,
			tc("a", "query_distribution", `{"table":"t1","column":"c"}`),
			tc("b", "analyze", `{"table":"t2"}`),
		),
		finalMsg("done"),
	}}
	disp := &stubDispatcher{resp: map[string]contract.Response{
		"query_distribution": {Status: contract.StatusOK},
		"analyze":            {Status: contract.StatusOK},
	}}
	rec := &fakeRecorder{}
	_, err := newTestRunner(model, disp, rec).Run(context.Background(), "q")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.toolCalls) != 2 {
		t.Fatalf("want 2 dispatches, got %v", rec.toolCalls)
	}
}
