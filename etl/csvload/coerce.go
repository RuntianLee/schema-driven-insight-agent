// Package csvload 把 CSV 文件构建成 de-identified Layer-2 快照（镜像 etl/seedgen 的合成路径）。
// CSV 是 Layer-1 原始源（含 PII），走和 PG 完全相同的脱敏管线落 Layer-2。
package csvload

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// coerce 把 CSV 字符串单元格转成 Layer2 类型。
// TEXT → 原样 string；INTEGER → ParseInt，失败回退 ParseFloat 四舍五入（货币小数列）。
// 空单元格 / 非数值 → 报错（fail-fast，带调用方补行列上下文）。
func coerce(cell, sqliteType string) (any, error) {
	cell = strings.TrimSpace(cell)
	if sqliteType == "TEXT" {
		return cell, nil
	}
	if cell == "" {
		return nil, fmt.Errorf("空单元格无法转 INTEGER")
	}
	if n, err := strconv.ParseInt(cell, 10, 64); err == nil {
		return n, nil
	}
	f, err := strconv.ParseFloat(cell, 64)
	if err != nil {
		return nil, fmt.Errorf("既非整数也非浮点: %q", cell)
	}
	return int64(math.Round(f)), nil
}
