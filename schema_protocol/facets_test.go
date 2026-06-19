package schema_protocol

import (
	"reflect"
	"testing"
)

func TestDeriveFacets(t *testing.T) {
	tests := []struct {
		name string
		q    AnalysisQuery
		want []string
	}{
		{
			name: "mean",
			q: AnalysisQuery{
				GroupBy:    []string{"server_id"},
				Aggregates: []Aggregate{{Fn: "avg", Column: "virtual_money", As: "m"}},
			},
			want: []string{"agg:avg", "dim:1", "shape:mean"},
		},
		{
			name: "sentinel",
			q: AnalysisQuery{
				Filters:    []Filter{{Field: "last_online_time", Op: "IS NOT NULL"}},
				Aggregates: []Aggregate{{Fn: "count", As: "c"}},
			},
			want: []string{"agg:count", "dim:0", "filter:null", "shape:sentinel"},
		},
		{
			name: "threshold",
			q: AnalysisQuery{
				GroupBy:    []string{"server_id"},
				Aggregates: []Aggregate{{Fn: "count", As: "c"}},
				Having:     []HavingCond{{Alias: "c", Op: ">", Value: 100}},
			},
			want: []string{"agg:count", "cmp:threshold", "dim:1", "shape:threshold"},
		},
		{
			name: "composite",
			q: AnalysisQuery{
				GroupBy:    []string{"server_id", "level"},
				Aggregates: []Aggregate{{Fn: "avg", Column: "x", As: "m"}},
			},
			want: []string{"agg:avg", "dim:2", "shape:composite"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveFacets(tc.q)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DeriveFacets(%q):\n  got  %v\n  want %v", tc.name, got, tc.want)
			}
		})
	}
}
