package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseJSONRoundTrip(t *testing.T) {
	in := Response{
		Status: StatusOK,
		Data: []BucketRow{{
			Bucket:        "0~1w",
			PlayerCount:   100,
			PctPlayers:    0.74,
			PctValue:      0.0968,
			TotalValue:    609054343,
			AvgValue:      2283.45,
			CumPctPlayers: 1.0,
			CumPctValue:   1.0,
		}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Response
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != StatusOK || len(out.Data) != 1 || out.Data[0].Bucket != "0~1w" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.Data[0].TotalValue != 609054343 {
		t.Fatalf("total_value round-trip: got %d want 609054343", out.Data[0].TotalValue)
	}
	if out.Data[0].AvgValue != 2283.45 {
		t.Fatalf("avg_value round-trip: got %f want 2283.45", out.Data[0].AvgValue)
	}
	if out.Data[0].CumPctPlayers != 1.0 {
		t.Fatalf("cum_pct_players round-trip: got %f want 1.0", out.Data[0].CumPctPlayers)
	}
	if out.Data[0].CumPctValue != 1.0 {
		t.Fatalf("cum_pct_value round-trip: got %f want 1.0", out.Data[0].CumPctValue)
	}
}

func TestStatusConstants(t *testing.T) {
	for _, s := range []Status{StatusOK, StatusInsufficient, StatusDegenerate, StatusSchemaError} {
		if s == "" {
			t.Fatal("empty status constant")
		}
	}
}

func TestDistProfile_JSON_NonBalance(t *testing.T) {
	p := DistProfile{
		Count: 200, Distinct: 3, Min: 10, Max: 80,
		Mean: 25, Median: 10, P10: 10, P25: 10, P75: 35, P90: 80, P95: 80, P99: 80,
		StdDev:    28.5,
		TopN:      []TopRow{{Value: "10", PlayerCount: 120, PctPlayers: 0.6}},
		TailCount: 0, TailPct: 0,
		// Total 留 nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if strings.Contains(got, "\"total\"") {
		t.Fatalf("non-balance profile must omit total: %s", got)
	}
	for _, want := range []string{"\"count\":200", "\"p25\":10", "\"top_n\"", "\"stddev\":28.5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestDistProfile_JSON_BalanceWithTotal(t *testing.T) {
	total := int64(63_000_000_000)
	p := DistProfile{Count: 100, Distinct: 5, Total: &total}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "\"total\":63000000000") {
		t.Fatalf("balance profile must include total: %s", got)
	}
}

func TestResponse_JSON_GroupByShape(t *testing.T) {
	r := Response{
		Status: StatusOK,
		Groups: []GroupProfile{
			{Group: "1", Profile: DistProfile{Count: 100}, Data: []BucketRow{{Bucket: "10", PlayerCount: 80}}},
		},
		GroupsTail: &GroupsTail{GroupCount: 5, PlayerCount: 200, PctPlayers: 0.1},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{"\"groups\"", "\"groups_tail\"", "\"group\":\"1\"", "\"group_count\":5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

// TestResponse_JSON_NonGroupShape：非 group_by 主路径——Profile 出现，Groups/GroupsTail 不出现。
func TestResponse_JSON_NonGroupShape(t *testing.T) {
	r := Response{
		Status:  StatusOK,
		Profile: &DistProfile{Count: 200, Distinct: 3},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "\"profile\"") {
		t.Fatalf("non-group Response must include profile: %s", got)
	}
	for _, forbidden := range []string{"\"groups\"", "\"groups_tail\""} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("non-group Response must NOT include %q: %s", forbidden, got)
		}
	}
}
