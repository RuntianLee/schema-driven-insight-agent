package contract

import "testing"

func TestAnalystResultID(t *testing.T) {
	cases := []struct {
		i    int
		want string
	}{{0, "q1"}, {1, "q2"}, {9, "q10"}}
	for _, c := range cases {
		if got := AnalystResultID(c.i); got != c.want {
			t.Errorf("AnalystResultID(%d)=%q want %q", c.i, got, c.want)
		}
	}
}
