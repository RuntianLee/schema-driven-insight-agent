package etl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HashPID 稳定脱敏：同 salt+pid 同 output，取 sha256 前 16 hex 字符。
// salt 由 adapter 提供（非加密脱敏，仅防偶然回查）。
func HashPID(salt string, pid int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", salt, pid)))
	return hex.EncodeToString(h[:])[:16]
}
