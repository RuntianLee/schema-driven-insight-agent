package etl

import "fmt"

// ValidateRowCount 行数安全闸门：低于 threshold 拒绝 commit（防空/半截抽取覆盖好库）。
func ValidateRowCount(n, threshold int) error {
	if n < threshold {
		return fmt.Errorf("ETL: row count %d below safety threshold %d, refusing to commit", n, threshold)
	}
	return nil
}
