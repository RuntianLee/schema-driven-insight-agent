// framework/eval_harness/tasks/loader.go
// Package tasks 加载声明式 YAML benchmark 任务（spec §3）。
package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Task 是单个 benchmark 任务。Evaluators 的 value 留作 yaml.Node，
// 由各 evaluator 自行 Decode（spec §3.3）。
type Task struct {
	ID         string               `yaml:"id"`
	Title      string               `yaml:"title"`
	Question   string               `yaml:"question"`
	LLMTurns   []string             `yaml:"llm_turns"`
	Evaluators map[string]yaml.Node `yaml:"evaluators"`
}

// LoadDir 读 dir 下所有 *.yaml，按文件名排序解析（确定性），校验 id 非空。
func LoadDir(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out []Task
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var task Task
		if err := yaml.Unmarshal(data, &task); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if task.ID == "" {
			return nil, fmt.Errorf("%s: 缺少 id", name)
		}
		out = append(out, task)
	}
	return out, nil
}
