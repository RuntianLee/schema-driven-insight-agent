// Package introspect 内省生产 PG 的表结构，渲染零代码接入草稿（cmd/init 的核心）。
// 机械部分（列名/类型）自动生成；业务知识（role/pii/buckets）渲染为 TODO 占位——
// schema_protocol.Parse 拒绝 TODO，强制人工标注后才可运行（安全闸）。
package introspect

import (
	"fmt"
	"sort"
	"strings"
)

// Column 是内省得到的一列。
type Column struct {
	Name   string
	PGType string // information_schema.columns.data_type
	IsPK   bool
}

// TableInfo 是内省得到的一张表。
type TableInfo struct {
	Name    string
	Columns []Column
}

// pgTypeMap：PG data_type → schema 协议 type。不在表内 = 不支持（草稿中注释列出）。
var pgTypeMap = map[string]string{
	"bigint":            "int64",
	"integer":           "int32",
	"smallint":          "int32",
	"text":              "string",
	"character varying": "string",
}

// TypeMap 返回 PG 类型对应的 schema 类型；ok=false 表示 v0.2 不支持。
func TypeMap(pgType string) (string, bool) {
	t, ok := pgTypeMap[pgType]
	return t, ok
}

// RenderSchema 渲染 schema.yaml 草稿。role/pii 一律 TODO（Parse 会拒绝，强制人工标注）。
func RenderSchema(domain, dsnEnv string, tables []TableInfo) []byte {
	var b strings.Builder
	b.WriteString("# schema.yaml 草稿 —— 由 cmd/init 内省生成。\n")
	b.WriteString("# 必须完成所有 TODO（role / pii）后才能使用：解析器会拒绝 TODO 占位。\n")
	b.WriteString("# role 白名单示例：actor_id / dimension / level / balance / last_seen /\n")
	b.WriteString("#   last_login / created_at / power / stage_progress / exp …（见 ADAPTER_GUIDE）\n")
	b.WriteString("# 涉及个人身份的列（ID/昵称/邮箱/设备号…）必须标 pii: true。\n")
	fmt.Fprintf(&b, "version: 1\ndomain: %s\n\n", domain)
	fmt.Fprintf(&b, "data_sources:\n  game_db: {type: postgres, dsn_env: %s, access: read_only}\n", dsnEnv)
	fmt.Fprintf(&b, "  layer2:  {type: sqlite, path: ./data/%s.db}\n\n", domain)
	fmt.Fprintf(&b, "etl_policy:\n  hash_salt: %s_v0   # TODO: 项目专属盐（或改用 hash_salt_env）\n", domain)
	b.WriteString("  min_rows: 1          # TODO: 行数安全闸门（约为预期行数的 50%）\n\n")
	b.WriteString("state_tables:\n")
	for _, t := range tables {
		fmt.Fprintf(&b, "  %s:\n    nature: snapshot\n", t.Name)
		var pks, unsupported []string
		for _, c := range t.Columns {
			if c.IsPK {
				pks = append(pks, c.Name)
			}
		}
		if len(pks) > 0 {
			fmt.Fprintf(&b, "    primary_key: [%s]\n", strings.Join(pks, ", "))
		}
		b.WriteString("    fields:\n")
		for _, c := range t.Columns {
			st, ok := TypeMap(c.PGType)
			if !ok {
				unsupported = append(unsupported, fmt.Sprintf("%s (%s)", c.Name, c.PGType))
				continue
			}
			pk := ""
			if c.IsPK {
				pk = ", pk: true"
			}
			fmt.Fprintf(&b, "      %s: {type: %s, role: TODO, pii: TODO%s}\n", c.Name, st, pk)
		}
		if len(unsupported) > 0 {
			sort.Strings(unsupported)
			fmt.Fprintf(&b, "      # 不支持的类型（未纳入，需手工处理或忽略）: %s\n",
				strings.Join(unsupported, ", "))
		}
	}
	b.WriteString("\n# TODO（可选）: derived_tables（货币 pivot）、glossary（术语/buckets）、scope（范围过滤）\n")
	return []byte(b.String())
}

// RenderDBConfigExample 渲染 db-config 模板（密码走 env，文件可提交）。
func RenderDBConfigExample() []byte {
	return []byte(`# 复制为 db.yaml 并填写（db.yaml 已被 .gitignore 排除，绝不提交真实凭据）。
host: 127.0.0.1
port: 5432
user: readonly_user
dbname: mydb
sslmode: disable
password_env: MY_PG_PASSWORD   # 密码放环境变量；或临时用 password: 内联（仅限 gitignore 文件）
`)
}

// RenderTaskSkeleton 渲染任务 YAML 骨架（含内联 fixture 示例）。
func RenderTaskSkeleton() []byte {
	return []byte(`# eval 任务骨架 —— 改写 question/llm_turns/fixture/evaluators 后放入 eval/tasks/。
id: example_distribution
title: "示例：某列分布"
question: "TODO: 运营会怎么问？"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"TODO_table","column":"TODO_column"}}'
  - "TODO: 模拟 agent 的洞察叙述（mock 道回放用）"
fixture:
  tables:
    TODO_table:
      groups:
        - {count: 100, values: {TODO_column: 1}}
evaluators:
  data_correctness:
    tool: query_distribution
    expect_status: OK
    profile: {count: 100}
    rows:
      - match: {bucket: "1"}
        expect: {player_count: 100}
`)
}

// RenderSeedExample 渲染 seed.yaml 骨架（每个支持列一个待填生成器）。
func RenderSeedExample(tables []TableInfo) []byte {
	var b strings.Builder
	b.WriteString("# seed.yaml 草稿 —— dev/demo 合成数据声明（cmd/seed 用）。\n")
	b.WriteString("# 每个物化列必须有生成器：const / enum(加权) / buckets(加权区间, skew: cube|recent)。\n")
	b.WriteString("tables:\n")
	for _, t := range tables {
		fmt.Fprintf(&b, "  %s:\n    rows: 1000\n    columns:\n", t.Name)
		for _, c := range t.Columns {
			if _, ok := TypeMap(c.PGType); !ok {
				continue
			}
			fmt.Fprintf(&b, "      %s: {buckets: [{min: 0, max: 100}]}   # TODO: 按业务分布调整\n", c.Name)
		}
	}
	return []byte(b.String())
}

// RenderGitignore 渲染接入目录的 .gitignore（真实凭据与产物绝不入库）。
func RenderGitignore() []byte {
	return []byte(`db.yaml
data/
*.db
etl_health*.json
`)
}
