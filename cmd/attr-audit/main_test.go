package main

import "testing"

func TestDetectToolCallLeak(t *testing.T) {
	cases := []struct {
		name, fo, wantSig string
		wantLeak          bool
	}{
		{"minimax wrapper", `<minimax:tool_call><invoke name="analyze">...`, "<minimax:tool_call>", true},
		{"bare invoke", `好的，<invoke name="query_distribution"><parameter name="table">x</parameter></invoke>`, "<invoke", true},
		{"hermes tag", `<tool_call>{"name":"analyze"}</tool_call>`, "<tool_call>", true},
		{"mistral marker", `[TOOL_CALLS][{"name":"analyze"}]`, "[TOOL_CALLS]", true},
		{"clean final answer", `头部 0.35% 持有 21.62%，建议加召回。`, "", false},
		{"normal attribution block", `{"attribution":[{"claim":"x"}]}\n报告...`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig, leaked := detectToolCallLeak(tc.fo)
			if leaked != tc.wantLeak || sig != tc.wantSig {
				t.Fatalf("got (%q,%v), want (%q,%v)", sig, leaked, tc.wantSig, tc.wantLeak)
			}
		})
	}
}
