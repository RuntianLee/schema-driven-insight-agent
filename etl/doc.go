// Package etl 提供 adapter 无关的通用 ETL 基建：生产 PG 只读抽取、PII 脱敏 hash、
// 行数安全闸门、Layer2 SQLite 全量替换、备份轮转。adapter 差异（盐、行数阈值、表名、
// 索引列、货币列映射、schema version）全部作为参数传入，framework 内零业务硬编码。
package etl
