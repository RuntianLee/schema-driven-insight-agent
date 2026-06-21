package schema_protocol

import "testing"

func TestParseAdvisorPlaybook(t *testing.T) {
	withAdvisor := []byte(`
version: 1
domain: demo
advisor:
  playbook: |
    高价值客户单独运营。
`)
	s, err := Parse(withAdvisor)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Advisor == nil {
		t.Fatal("Advisor nil，期望解析出 playbook")
	}
	if want := "高价值客户单独运营。\n"; s.Advisor.Playbook != want {
		t.Errorf("Playbook=%q want %q", s.Advisor.Playbook, want)
	}

	noAdvisor := []byte("version: 1\ndomain: demo\n")
	s2, err := Parse(noAdvisor)
	if err != nil {
		t.Fatalf("parse no-advisor: %v", err)
	}
	if s2.Advisor != nil {
		t.Errorf("无 advisor 块时 Advisor 应为 nil，实得 %+v", s2.Advisor)
	}
}
