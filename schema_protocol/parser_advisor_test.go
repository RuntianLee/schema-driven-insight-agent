package schema_protocol

import (
	"strings"
	"testing"
)

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

func TestParseAdvisorBareKeyRejected(t *testing.T) {
	// 裸 `advisor:`（YAML null）不得静默绕过——必须明确报错，与 etl_policy 对称。
	bare := []byte("version: 1\ndomain: demo\nadvisor:\n")
	if _, err := Parse(bare); err == nil || !strings.Contains(err.Error(), "advisor") {
		t.Errorf("裸 advisor: 应拒绝且错误含 advisor, got %v", err)
	}
}
