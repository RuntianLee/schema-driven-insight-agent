package schema_protocol

import (
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// reIdent：表名/列名必须是安全 SQL 标识符——它们会被 inline 进 SQL
// （sql_builder / profile_builder），白名单只保证「名字在 schema 里」，
// 这里保证「名字本身无害」（defense in depth）。
var reIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// 中间结构：吸收 YAML 的两种 table 形态（state_tables 用 fields，derived 用 schema）。
type rawSchema struct {
	Version       int                  `yaml:"version"`
	Domain        string               `yaml:"domain"`
	DataSources   map[string]rawSource `yaml:"data_sources"`
	StateTables   map[string]rawTable  `yaml:"state_tables"`
	DerivedTables map[string]rawTable  `yaml:"derived_tables"`
	Glossary      rawGlossary          `yaml:"glossary"`
	Tuning        rawTuning            `yaml:"tuning"`
	ETLPolicy     yaml.Node            `yaml:"etl_policy"` // 裸键(null) 与 {} 区分见 Parse
}

type rawETLPolicy struct {
	HashSalt      string `yaml:"hash_salt"`
	HashSaltEnv   string `yaml:"hash_salt_env"`
	MinRows       int    `yaml:"min_rows"`
	HealthMinRows int64  `yaml:"health_min_rows"`
	Frozen        bool   `yaml:"frozen"`
	HealthPath    string `yaml:"health_path"`
}

type rawTuning struct {
	RowsAttachThreshold         int `yaml:"rows_attach_threshold"`
	ValueTopN                   int `yaml:"value_top_n"`
	GroupsTopN                  int `yaml:"groups_top_n"`
	PerGroupRowsAttachThreshold int `yaml:"per_group_rows_attach_threshold"`
}

type rawSource struct {
	Type   string `yaml:"type"`
	DSNEnv string `yaml:"dsn_env"`
	Access string `yaml:"access"`
	Path   string `yaml:"path"`
}

type rawTable struct {
	Nature      string              `yaml:"nature"`
	PrimaryKey  []string            `yaml:"primary_key"`
	Fields      map[string]FieldDef `yaml:"fields"`
	DerivedFrom string              `yaml:"derived_from"`
	Method      string              `yaml:"method"`
	SchemaCols  map[string]FieldDef `yaml:"schema"` // derived_tables 用 schema: 声明列
}

type rawGlossary struct {
	CurrencyTypes map[string]string      `yaml:"currency_types"`
	Buckets       map[string][]rawBucket `yaml:"buckets"`
}

type rawBucket struct {
	Min   int64  `yaml:"min"`
	Max   *int64 `yaml:"max"` // null → +∞（Max==0 sentinel）；max: 0 reserved, must not be used for a genuine zero-max range in V0
	Label string `yaml:"label"`
}

// FieldDef 的 YAML 标签（与 §6 schema 规范对齐；未声明键被忽略）。
// role: TODO / pii: TODO 是 cmd/init 草稿的占位——拒绝解析，强制人工标注后才可运行
// （安全闸：杜绝「忘标 PII 就物化」）。
func (f *FieldDef) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Type         string    `yaml:"type"`
		Role         string    `yaml:"role"`
		PK           bool      `yaml:"pk"`
		PII          yaml.Node `yaml:"pii"`
		OmitInLayer2 bool      `yaml:"omit_in_layer2"`
		CurrencyType string    `yaml:"currency_type"`
		GlossaryKey  string    `yaml:"glossary_key"`
		Index        bool      `yaml:"index"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Role == "TODO" {
		return fmt.Errorf("role: TODO 未标注——cmd/init 草稿必须人工完成 role 标注后才可使用")
	}
	var pii bool
	if !raw.PII.IsZero() {
		if err := raw.PII.Decode(&pii); err != nil {
			return fmt.Errorf("pii 必须是 true/false（cmd/init 草稿需人工标注）: %w", err)
		}
	}
	// 新增 FieldDef 字段需同步此构造。
	*f = FieldDef{Type: raw.Type, Role: raw.Role, PK: raw.PK, PII: pii,
		OmitInLayer2: raw.OmitInLayer2, CurrencyType: raw.CurrencyType,
		GlossaryKey: raw.GlossaryKey, Index: raw.Index}
	return nil
}

// Parse 解析 YAML → Schema，并校验 bucket 单调性（U2）。
func Parse(yamlBytes []byte) (*Schema, error) {
	var r rawSchema
	if err := yaml.Unmarshal(yamlBytes, &r); err != nil {
		return nil, fmt.Errorf("yaml parse: %w", err)
	}

	s := &Schema{
		Version:       r.Version,
		Domain:        r.Domain,
		DataSources:   make(map[string]DataSource, len(r.DataSources)),
		StateTables:   make(map[string]Table, len(r.StateTables)),
		DerivedTables: make(map[string]Table, len(r.DerivedTables)),
		Glossary: Glossary{
			CurrencyTypes: r.Glossary.CurrencyTypes,
			Buckets:       make(map[string][]BucketDef, len(r.Glossary.Buckets)),
		},
		Tuning: Tuning{
			RowsAttachThreshold:         r.Tuning.RowsAttachThreshold,
			ValueTopN:                   r.Tuning.ValueTopN,
			GroupsTopN:                  r.Tuning.GroupsTopN,
			PerGroupRowsAttachThreshold: r.Tuning.PerGroupRowsAttachThreshold,
		},
	}
	// etl_policy 三态：未声明(IsZero)=旧 schema 兼容；裸键(null)=明确报错，
	// 不让 nil policy 静默绕过安全闸；否则解码后照旧转换。
	if !r.ETLPolicy.IsZero() {
		if r.ETLPolicy.Tag == "!!null" {
			return nil, fmt.Errorf("etl_policy 为空块——要么删除该键，要么填写 hash_salt/min_rows 等字段")
		}
		var raw rawETLPolicy
		if err := r.ETLPolicy.Decode(&raw); err != nil {
			return nil, fmt.Errorf("etl_policy 解码失败: %w", err)
		}
		s.ETLPolicy = &ETLPolicy{
			HashSalt: raw.HashSalt, HashSaltEnv: raw.HashSaltEnv,
			MinRows: raw.MinRows, HealthMinRows: raw.HealthMinRows,
			Frozen: raw.Frozen, HealthPath: raw.HealthPath,
		}
	}
	for k, v := range r.DataSources {
		s.DataSources[k] = DataSource{Type: v.Type, DSNEnv: v.DSNEnv, Access: v.Access, Path: v.Path}
	}
	for k, v := range r.StateTables {
		s.StateTables[k] = Table{Nature: v.Nature, PrimaryKey: v.PrimaryKey, Fields: v.Fields}
	}
	for k, v := range r.DerivedTables {
		fields := v.Fields
		if len(fields) == 0 {
			fields = v.SchemaCols // derived 用 schema:
		}
		s.DerivedTables[k] = Table{Nature: "derived", PrimaryKey: v.PrimaryKey, Fields: fields, DerivedFrom: v.DerivedFrom, Method: v.Method}
	}

	for key, buckets := range r.Glossary.Buckets {
		converted, err := convertBuckets(key, buckets)
		if err != nil {
			return nil, err
		}
		s.Glossary.Buckets[key] = converted
	}

	if err := validateIdentifiers(s); err != nil {
		return nil, err
	}
	if err := validateETLPolicy(s); err != nil {
		return nil, err
	}
	if err := validateIndexFlags(s); err != nil {
		return nil, err
	}
	return s, nil
}

// validateETLPolicy：声明了 etl_policy 时的参数边界（未声明=旧 schema，跳过）。
func validateETLPolicy(s *Schema) error {
	p := s.ETLPolicy
	if p == nil {
		return nil
	}
	if p.MinRows <= 0 {
		return fmt.Errorf("etl_policy.min_rows 必须 > 0（行数安全闸门必填）")
	}
	if p.HealthMinRows < 0 {
		return fmt.Errorf("etl_policy.health_min_rows 不可为负")
	}
	if len(s.DerivedTables) > 0 && p.HashSalt == "" && p.HashSaltEnv == "" {
		return fmt.Errorf("etl_policy: 声明了派生表时 hash_salt 与 hash_salt_env 至少设一个")
	}
	return nil
}

// validateIndexFlags：index: true 仅允许出现在将物化进 Layer2 的列上。
// 派生表同样物化进 Layer2，故 state ∪ derived 都要覆盖。
func validateIndexFlags(s *Schema) error {
	check := func(kind, name string, t Table) error {
		for col, fd := range t.Fields {
			if fd.Index && (fd.PII || fd.OmitInLayer2) {
				return fmt.Errorf("%s %s: 列 %q 标了 index 但不物化（pii/omit_in_layer2）", kind, name, col)
			}
		}
		return nil
	}
	for name, t := range s.StateTables {
		if err := check("state_tables", name, t); err != nil {
			return err
		}
	}
	for name, t := range s.DerivedTables {
		if err := check("derived_tables", name, t); err != nil {
			return err
		}
	}
	return nil
}

// validateIdentifiers 校验所有表名/列名为安全 SQL 标识符（见 reIdent 注释）。
func validateIdentifiers(s *Schema) error {
	checkTable := func(kind, name string, t Table) error {
		if !reIdent.MatchString(name) {
			return fmt.Errorf("%s %q: 非法表名（须匹配 %s）", kind, name, reIdent)
		}
		for col := range t.Fields {
			if !reIdent.MatchString(col) {
				return fmt.Errorf("%s %s: 非法列名 %q（须匹配 %s）", kind, name, col, reIdent)
			}
		}
		return nil
	}
	for name, t := range s.StateTables {
		if err := checkTable("state_tables", name, t); err != nil {
			return err
		}
	}
	for name, t := range s.DerivedTables {
		if err := checkTable("derived_tables", name, t); err != nil {
			return err
		}
	}
	return nil
}

func convertBuckets(key string, raw []rawBucket) ([]BucketDef, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("bucket %q: empty", key)
	}
	out := make([]BucketDef, len(raw))
	for i, b := range raw {
		max := int64(0)
		if b.Max != nil {
			max = *b.Max
		}
		out[i] = BucketDef{Min: b.Min, Max: max, Label: b.Label}
	}
	// 按 Min 升序，校验单调不重叠（仅末桶允许 Max==0 表 +∞）。
	// V0 不校验桶间 gap：开发者自控 schema，落空值由 SQL ELSE 兜底。
	sort.Slice(out, func(i, j int) bool { return out[i].Min < out[j].Min })
	for i := 0; i < len(out); i++ {
		isLast := i == len(out)-1
		if !isLast && out[i].Max == 0 {
			return nil, fmt.Errorf("bucket %q[%d] (%s): only last bucket may have +∞ max", key, i, out[i].Label)
		}
		if !isLast && out[i].Max < out[i].Min {
			return nil, fmt.Errorf("bucket %q[%d] (%s): max %d < min %d", key, i, out[i].Label, out[i].Max, out[i].Min)
		}
		if !isLast && out[i+1].Min <= out[i].Max {
			return nil, fmt.Errorf("bucket %q: %s and %s overlap", key, out[i].Label, out[i+1].Label)
		}
	}
	return out, nil
}

// ResolveColumn 返回字段定义；未声明 → error（U2）。table 在 state ∪ derived 中查。
func (s *Schema) ResolveColumn(table, column string) (FieldDef, error) {
	t, ok := s.lookupTable(table)
	if !ok {
		return FieldDef{}, fmt.Errorf("table %q not in schema", table)
	}
	f, ok := t.Fields[column]
	if !ok {
		return FieldDef{}, fmt.Errorf("column %q not in table %q", column, table)
	}
	return f, nil
}

func (s *Schema) lookupTable(name string) (Table, bool) {
	if t, ok := s.StateTables[name]; ok {
		return t, true
	}
	if t, ok := s.DerivedTables[name]; ok {
		return t, true
	}
	return Table{}, false
}
