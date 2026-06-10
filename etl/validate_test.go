package etl

import "testing"

func TestValidateRowCount(t *testing.T) {
	if err := ValidateRowCount(100, 100); err != nil {
		t.Errorf("count==threshold should pass, got %v", err)
	}
	if err := ValidateRowCount(99, 100); err == nil {
		t.Errorf("count<threshold should fail")
	}
}
