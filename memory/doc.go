// Package memory 是 V2 长期记忆子系统。
//
// Memory 使用独立 SQLite+FTS5 memory.db 保存可重建、已脱敏的经验案例，
// 不保存原始 prompt 或 tool payload。
package memory
