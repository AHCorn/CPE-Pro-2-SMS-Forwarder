package forwarder

import "testing"

// 验证手机号脱敏：仅保留末 4 位，避免完整 PII 落入日志。
func TestMaskPhone(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"123":            "***",
		"1234":           "****",
		"13812345678":    "*******5678",
		"+8613812345678": "**********5678",
	}
	for in, want := range cases {
		if got := maskPhone(in); got != want {
			t.Errorf("maskPhone(%q)=%q, want %q", in, got, want)
		}
	}
}
