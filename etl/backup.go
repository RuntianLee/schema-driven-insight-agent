package etl

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const backupRetentionDays = 7

// RotateBackup copies sqlitePath -> sqlitePath+".bak."+YYYYMMDD, then deletes
// sibling backups older than 7 days. Backup failure must NOT fail the ETL.
func RotateBackup(sqlitePath string) error {
	today := time.Now().Format("20060102")
	destPath := sqlitePath + ".bak." + today

	if err := copyFile(sqlitePath, destPath); err != nil {
		return fmt.Errorf("copy to %s: %w", destPath, err)
	}

	// Delete sibling .bak.* files older than 7 days.
	// 按"日期"比较：cutoff 取本地今天的午夜再减 7 天，使"恰好 7 天前"的备份（午夜）
	// 不早于 cutoff → 保留（边界含 7 天）；只有严格更早（≥8 天前）才删。避免用当前时分
	// 导致"恰好 7 天前午夜 < 7 天前此刻"被误删的边界 bug。
	now := time.Now()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -backupRetentionDays)
	dir := filepath.Dir(sqlitePath)
	base := filepath.Base(sqlitePath)
	prefix := base + ".bak."

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		dateSuffix := strings.TrimPrefix(name, prefix)
		// Skip if it's the file we just wrote.
		if dateSuffix == today {
			continue
		}
		// 用本地时区解析，与文件名的本地 Format 及 cutoff 的本地午夜保持一致（避免 UTC/本地错位）。
		t, err := time.ParseInLocation("20060102", dateSuffix, now.Location())
		if err != nil {
			// Unparseable suffix — skip, don't delete.
			continue
		}
		if t.Before(cutoff) {
			fullPath := filepath.Join(dir, name)
			if rmErr := os.Remove(fullPath); rmErr != nil {
				return fmt.Errorf("remove %s: %w", fullPath, rmErr)
			}
		}
	}
	return nil
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
