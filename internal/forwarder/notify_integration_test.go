package forwarder

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/config"
)

// TestSendBarkPayload 用本地 httptest 模拟 Bark /push，验证 bark:// URL 经 shoutrrr
// 真实发出的 HTTP 请求路径与 JSON 负载（title / body / device_key）均正确。
func TestSendBarkPayload(t *testing.T) {
	var (
		mu       sync.Mutex
		gotPath  string
		gotBody  map[string]any
		gotCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotCount++
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success","timestamp":0}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	cfg := &config.Config{}
	cfg.Notify.URLs = []string{"bark://:testdevicekey@" + host + "/?scheme=http"}

	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.deliver("标题A", "正文B", false); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("期望 1 次请求，实际 %d", gotCount)
	}
	if gotPath != "/push" {
		t.Fatalf("期望路径 /push，实际 %q", gotPath)
	}
	if gotBody["device_key"] != "testdevicekey" {
		t.Fatalf("device_key 不符: %v", gotBody["device_key"])
	}
	if gotBody["title"] != "标题A" {
		t.Fatalf("title 不符: %v", gotBody["title"])
	}
	if gotBody["body"] != "正文B" {
		t.Fatalf("body 不符: %v", gotBody["body"])
	}
}

// TestSendMultiChannelAggregatesErrors 验证多渠道时，可达渠道照常发送、
// 不可达渠道的错误被汇总返回（不会因一个渠道失败而吞掉其余）。
func TestSendMultiChannelAggregatesErrors(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"code":200,"message":"ok"}`))
	}))
	defer srv.Close()
	okHost := strings.TrimPrefix(srv.URL, "http://")

	cfg := &config.Config{}
	cfg.Notify.URLs = []string{
		"bark://:k1@" + okHost + "/?scheme=http",
		"bark://:k2@127.0.0.1:1/?scheme=http", // 故意不可达端口
	}
	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = f.deliver("t", "b", false)
	if err == nil {
		t.Fatalf("期望返回不可达渠道的聚合错误，实际为 nil")
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("期望可达渠道仍被请求 1 次，实际 %d", hits)
	}
}

// TestPushSMSTemplateAndGroup 端到端验证：自定义标题 / 正文模板的占位符被正确渲染，
// 且 Bark 分组（URL ?group= 参数）随推送一并下发。
func TestPushSMSTemplateAndGroup(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	cfg := &config.Config{}
	cfg.Notify.URLs = []string{"bark://:k@" + host + "/?scheme=http&group=cpe-sms"}
	cfg.Notify.SMSTitle = "[CPE] {phone}"
	cfg.Notify.SMSBody = "{content}\n时间 {time}"

	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.pushSMS("10086", "您的验证码是 1234", "2026-06-24 22:00"); err != nil {
		t.Fatalf("pushSMS: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotBody["title"] != "[CPE] 10086" {
		t.Errorf("title 模板渲染不符: %v", gotBody["title"])
	}
	if gotBody["body"] != "您的验证码是 1234\n时间 2026-06-24 22:00" {
		t.Errorf("body 模板渲染不符: %v", gotBody["body"])
	}
	if gotBody["group"] != "cpe-sms" {
		t.Errorf("group 未随推送下发: %v", gotBody["group"])
	}
}
