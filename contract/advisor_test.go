package contract

import "testing"

func TestAnalystResultID(t *testing.T) {
	cases := []struct {
		i    int
		want string
	}{{0, "q1"}, {1, "q2"}, {9, "q10"}}
	for _, c := range cases {
		if got := AnalystResultID(c.i); got != c.want {
			t.Errorf("AnalystResultID(%d)=%q want %q", c.i, got, c.want)
		}
	}
}

func TestOKCalls_FiltersNonOK(t *testing.T) {
	calls := []ToolCall{
		{Name: "analyze", Response: Response{Status: StatusSchemaError}},
		{Name: "first_ok", Response: Response{Status: StatusOK}},
		{Name: "analyze", Response: Response{Status: StatusInsufficient}},
		{Name: "second_ok", Response: Response{Status: StatusOK}},
	}
	ok := OKCalls(calls)
	if len(ok) != 2 {
		t.Fatalf("OKCalls 应只留 2 个成功调用，得到 %d", len(ok))
	}
	if ok[0].Name != "first_ok" || ok[1].Name != "second_ok" {
		t.Fatalf("OKCalls 应保留 OK 原始顺序，得到 [%q, %q]", ok[0].Name, ok[1].Name)
	}
}
