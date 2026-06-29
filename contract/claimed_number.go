// Package contract：ClaimedNumber 是 ClaimAnchor.ClaimedValue 的容错数值类型。
package contract

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// ClaimedNumber 容忍 agent 把定量主张写成带单位字符串（"20人"/"60%"/"$1,234"/"3倍"）。
// 解码时剥装饰单位 + % 按 /100（小数口径，design spec §3）转 float；
// 倍率词（万/千/亿/k/M/B）或不可解析 → 置 NaN → 下游判 unresolvable（显式暴露，不静默误丢、不冒充 mismatch）。
// UnmarshalJSON 永不返回 error：保证整块归因 JSON 必定解码成功，单字段坏不炸整块。
type ClaimedNumber float64

// decorativeSuffixes 是"数字即字面值"的装饰后缀（剥掉即可，数字照用）。
// 注意「倍」/「x」属"标签型倍数"：数字即字面值（"3倍"→3、"2.3x"→2.3），不改变数量级，故归为装饰后缀。
// 这与"缩放倍率词"（万/千/亿/k/M/B）是两个不同类别——后者需把数字放大数量级，刻意不支持、置 NaN
// 显式暴露（见 parseDecoratedNumber）；二者切勿混淆。
var decorativeSuffixes = []string{"人", "元", "个", "次", "天", "名", "位", "户", "倍", "x", "X"}

// currencyPrefixes 是可剥的货币符前缀。
var currencyPrefixes = []string{"$", "¥", "￥", "€"}

// UnmarshalJSON 容错解码：number 直解；string 剥装饰；二者皆非 → NaN。永不报错。
func (c *ClaimedNumber) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*c = ClaimedNumber(f)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		*c = ClaimedNumber(math.NaN()) // 既非 number 又非 string
		return nil
	}
	*c = ClaimedNumber(parseDecoratedNumber(s))
	return nil
}

// MarshalJSON 把 NaN/Inf 安全序列化为 null（标准 json 对 NaN 报错）。
func (c ClaimedNumber) MarshalJSON() ([]byte, error) {
	f := float64(c)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	return json.Marshal(f) // f 是 float64 而非 ClaimedNumber——不会递归
}

// parseDecoratedNumber 剥装饰后缀/货币符/千分位 + % 处理；含倍率词或残留非数字 → NaN。
func parseDecoratedNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return math.NaN()
	}
	for _, p := range currencyPrefixes {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimSpace(s)

	pct := false
	for _, p := range []string{"%", "％"} {
		if strings.HasSuffix(s, p) {
			pct = true
			s = strings.TrimSuffix(s, p)
			break // 只剥一个百分号："60%%" 残留 "60%" → ParseFloat 失败 → NaN（守恒）
		}
	}
	for _, suf := range decorativeSuffixes {
		s = strings.TrimSuffix(s, suf)
	}
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "，", "")
	if s == "" {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN() // 倍率词/其它残留 → 显式不可解析
	}
	if pct {
		v /= 100
	}
	return v
}
