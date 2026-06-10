package schema_protocol

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// 中间结构：吸收 YAML 的两种 table 形态（state_tables 用 fields，derived 用 schema）。
type rawSchema struct {
	Version       int                  `yaml:"version"`
	Domain        string               `yaml:"domain"`
	DataSources   map[string]rawSource `yaml:"data_sources"`
	StateTables   map[string]rawTable  `yaml:"state_tables"`
	DerivedTables map[string]rawTable  `yaml:"derived_tables"`
	Glossary      rawGlossary          `yaml:"glossary"`
	Tuning        rawTuning            `yaml:"tuning"`
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
func (f *FieldDef) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Type         string `yaml:"type"`
		Role         string `yaml:"role"`
		PK           bool   `yaml:"pk"`
		PII          bool   `yaml:"pii"`
		OmitInLayer2 bool   `yaml:"omit_in_layer2"`
		CurrencyType string `yaml:"currency_type"`
		GlossaryKey  string `yaml:"glossary_key"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*f = FieldDef(raw)
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
	return s, nil
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
