package csvload

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// readCSV 读整份 CSV，返回表头 + 数据行（首行为表头，至少需 1 数据行）。
func readCSV(path string) (header []string, records [][]string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open csv %s: %w", path, err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	all, err := r.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("read csv %s: %w", path, err)
	}
	if len(all) < 2 {
		return nil, nil, fmt.Errorf("csv %s 至少需表头+1 行，得 %d 行", path, len(all))
	}
	return all[0], all[1:], nil
}

// CSVPathFromSchema 找 type=csv 的数据源 path（相对 schema 目录解析为可用路径）。
func CSVPathFromSchema(s *schema_protocol.Schema, schemaPath string) (string, error) {
	for _, src := range s.DataSources {
		if src.Type == "csv" {
			if src.Path == "" {
				return "", fmt.Errorf("data_sources type=csv 缺 path")
			}
			return filepath.Join(filepath.Dir(schemaPath), src.Path), nil
		}
	}
	return "", fmt.Errorf("schema data_sources 无 type=csv 源")
}
