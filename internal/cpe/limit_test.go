package cpe

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 验证：路由器返回超大响应时，doRequest 按 maxResponseBytes 上限中止读取，防内存 DoS。
func TestDoRequestLimitsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("a", 1<<20) // 1 MiB
		for i := 0; i < 10; i++ {           // 共 10 MiB，超过 8 MiB 上限
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()

	c := NewClient(strings.TrimPrefix(srv.URL, "http://"), "u", "p")
	if _, _, err := c.doRequest("GET", "/", "", "", ""); err == nil {
		t.Fatal("超大响应应返回错误，实际为 nil")
	} else if !strings.Contains(err.Error(), "上限") {
		t.Fatalf("期望超限错误，实得: %v", err)
	}
}

// 正常大小响应不受影响。
func TestDoRequestAllowsNormalBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":1}`)
	}))
	defer srv.Close()

	c := NewClient(strings.TrimPrefix(srv.URL, "http://"), "u", "p")
	status, body, err := c.doRequest("GET", "/", "", "", "")
	if err != nil || status != 200 || body != `{"ok":1}` {
		t.Fatalf("正常响应读取失败: status=%d body=%q err=%v", status, body, err)
	}
}
