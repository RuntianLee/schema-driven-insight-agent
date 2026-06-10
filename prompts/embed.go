// Package prompts 提供 embed 的方法论 prompt（不含任何 baseline 数字，design-v3 §4 #4）。
package prompts

import _ "embed"

//go:embed system_v0.md
var SystemV0 string
