package contract

import (
	"encoding/json"
	"math"
	"testing"
)

func TestClaimedNumber_Unmarshal(t *testing.T) {
	cases := []struct {
		name string
		json string
		want float64 // NaN 表示期望不可解析
	}{
		{"number", `20`, 20},
		{"number_float", `0.6`, 0.6},
		{"plain_digit_string", `"20"`, 20},
		{"unit_word", `"20人"`, 20},
		{"unit_yuan", `"1234元"`, 1234},
		{"percent_halfwidth", `"60%"`, 0.6},
		{"percent_fullwidth", `"60％"`, 0.6},
		{"thousands_currency", `"$1,234"`, 1234},
		{"thousands_cn_yuan", `"1,234元"`, 1234},
		{"ratio_bei", `"3倍"`, 3},
		{"ratio_x", `"2.3x"`, 2.3},
		{"multiplier_wan_unsupported", `"20万"`, math.NaN()},
		{"multiplier_yi_unsupported", `"1.2亿"`, math.NaN()},
		{"garbage", `"垃圾"`, math.NaN()},
		{"empty_string", `""`, math.NaN()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var cn ClaimedNumber
			if err := json.Unmarshal([]byte(c.json), &cn); err != nil {
				t.Fatalf("UnmarshalJSON 不应报错（永不返回 error），got %v", err)
			}
			got := float64(cn)
			if math.IsNaN(c.want) {
				if !math.IsNaN(got) {
					t.Fatalf("期望 NaN（不可解析），got %v", got)
				}
				return
			}
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestClaimedNumber_MarshalNaNSafe(t *testing.T) {
	cn := ClaimedNumber(math.NaN())
	b, err := json.Marshal(cn)
	if err != nil {
		t.Fatalf("NaN 不应导致 Marshal 报错，got %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("NaN 应序列化为 null，got %s", b)
	}
	b2, _ := json.Marshal(ClaimedNumber(20))
	if string(b2) != "20" {
		t.Fatalf("正常值应正常序列化，got %s", b2)
	}
}
