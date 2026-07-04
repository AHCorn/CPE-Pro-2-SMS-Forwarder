package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig 写一个最小可通过校验的配置文件并返回其路径。
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	return path
}

const baseConfig = `cpe:
  host: "192.168.2.1"
  username: "admin"
  password: "x"
notify:
  urls:
    - "bark://:devicekey@example.com"
`

// TestYieldAndNotifyDefaults 验证未设置时 yield_minutes 取默认 5、退避通知默认开启。
func TestYieldAndNotifyDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Poll.YieldMinutes != 5 {
		t.Errorf("YieldMinutes 默认应为 5，实际 %d", cfg.Poll.YieldMinutes)
	}
	if cfg.Notify.NotifyYield == nil || !*cfg.Notify.NotifyYield {
		t.Errorf("NotifyYield 默认应为 true，实际 %v", cfg.Notify.NotifyYield)
	}
}

// TestNotifyYieldExplicitFalse 验证 notify_yield: false 被尊重（不被默认覆盖）。
func TestNotifyYieldExplicitFalse(t *testing.T) {
	cfg, err := Load(writeConfig(t, baseConfig+"  notify_yield: false\n"))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.NotifyYield == nil || *cfg.Notify.NotifyYield {
		t.Errorf("notify_yield 显式 false 应保留为 false，实际 %v", cfg.Notify.NotifyYield)
	}
}

// TestYieldMinutesNegativeDisabled 验证负数表示禁用退避、不被默认值覆盖。
func TestYieldMinutesNegativeDisabled(t *testing.T) {
	cfg, err := Load(writeConfig(t, baseConfig+"poll:\n  yield_minutes: -1\n"))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Poll.YieldMinutes != -1 {
		t.Errorf("yield_minutes=-1 应保留为 -1（禁用），实际 %d", cfg.Poll.YieldMinutes)
	}
}

// TestEnvOverrides 验证环境变量覆盖：NOTIFY_YIELD 关闭通知、CPE_YIELD_MINUTES 改退避上限。
func TestEnvOverrides(t *testing.T) {
	t.Setenv("NOTIFY_YIELD", "false")
	t.Setenv("CPE_YIELD_MINUTES", "2")
	cfg, err := Load(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.NotifyYield == nil || *cfg.Notify.NotifyYield {
		t.Errorf("NOTIFY_YIELD=false 应使 NotifyYield 为 false，实际 %v", cfg.Notify.NotifyYield)
	}
	if cfg.Poll.YieldMinutes != 2 {
		t.Errorf("CPE_YIELD_MINUTES=2 应使 YieldMinutes 为 2，实际 %d", cfg.Poll.YieldMinutes)
	}
}

// TestNotifyURLsEnvAndValidation 验证 NOTIFY_URLS 以空白分隔覆盖、空 URL 列表校验失败。
func TestNotifyURLsEnvAndValidation(t *testing.T) {
	t.Setenv("NOTIFY_URLS", "bark://:k@example.com  ntfy://ntfy.sh/topic")
	cfg, err := Load(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(cfg.Notify.URLs) != 2 {
		t.Errorf("NOTIFY_URLS 应解析出 2 个渠道，实际 %d: %v", len(cfg.Notify.URLs), cfg.Notify.URLs)
	}

	// 清除上面设置的 NOTIFY_URLS（空值不参与覆盖），验证无任何渠道时校验失败。
	t.Setenv("NOTIFY_URLS", "")
	noNotify := `cpe:
  host: "192.168.2.1"
  username: "admin"
  password: "x"
`
	if _, err := Load(writeConfig(t, noNotify)); err == nil {
		t.Error("未配置任何通知渠道时应校验失败，但 Load 成功")
	}
}

// TestSMSTemplate 验证短信模板：默认值、配置文件里字面 \n 反转义、环境变量覆盖。
func TestSMSTemplate(t *testing.T) {
	cfg, err := Load(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.SMSTitle != "CPE短信 - {phone}" {
		t.Errorf("SMSTitle 默认不符: %q", cfg.Notify.SMSTitle)
	}
	if cfg.Notify.SMSBody != "{content}\n\n{time}" {
		t.Errorf("SMSBody 默认不符: %q", cfg.Notify.SMSBody)
	}

	// 单引号 YAML 不处理转义，写入的是字面 \n，Load 后应转成真实换行。
	custom := baseConfig + "  sms_title: '[验证码] {content}'\n  sms_body: '{phone}\\n{time}'\n"
	cfg, err = Load(writeConfig(t, custom))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.SMSTitle != "[验证码] {content}" {
		t.Errorf("SMSTitle 自定义不符: %q", cfg.Notify.SMSTitle)
	}
	if cfg.Notify.SMSBody != "{phone}\n{time}" {
		t.Errorf("SMSBody 字面 \\n 应转成换行: %q", cfg.Notify.SMSBody)
	}

	t.Setenv("NOTIFY_SMS_TITLE", "ENVT")
	t.Setenv("NOTIFY_SMS_BODY", "ENVB")
	cfg, err = Load(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.SMSTitle != "ENVT" || cfg.Notify.SMSBody != "ENVB" {
		t.Errorf("环境变量覆盖失败: title=%q body=%q", cfg.Notify.SMSTitle, cfg.Notify.SMSBody)
	}
}

// TestRetryFailedConfig 验证 notify.retry_failed：省略时为 nil（forwarder 视为关闭）、
// 显式 true 被解析、环境变量 NOTIFY_RETRY_FAILED 覆盖文件值。
func TestRetryFailedConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.RetryFailed != nil {
		t.Errorf("retry_failed 省略时应为 nil，实际 %v", *cfg.Notify.RetryFailed)
	}

	cfg, err = Load(writeConfig(t, baseConfig+"  retry_failed: true\n"))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.RetryFailed == nil || !*cfg.Notify.RetryFailed {
		t.Errorf("retry_failed: true 应解析为 true，实际 %v", cfg.Notify.RetryFailed)
	}

	t.Setenv("NOTIFY_RETRY_FAILED", "false")
	cfg, err = Load(writeConfig(t, baseConfig+"  retry_failed: true\n"))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.RetryFailed == nil || *cfg.Notify.RetryFailed {
		t.Errorf("NOTIFY_RETRY_FAILED=false 应覆盖为 false，实际 %v", cfg.Notify.RetryFailed)
	}
}

// TestFallbackConfig 验证 notify.fallback：省略时禁用（URL 为空）、文件配置可解析、
// URL 首尾空白被清理、环境变量覆盖文件值。
func TestFallbackConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.Fallback.URL != "" || cfg.Notify.Fallback.Forward {
		t.Errorf("fallback 省略时应禁用，实际 url=%q forward=%v", cfg.Notify.Fallback.URL, cfg.Notify.Fallback.Forward)
	}

	body := baseConfig + "  fallback:\n    url: ' ntfy://ntfy.sh/backup '\n    forward: true\n"
	cfg, err = Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.Fallback.URL != "ntfy://ntfy.sh/backup" {
		t.Errorf("fallback.url 应去除首尾空白，实际 %q", cfg.Notify.Fallback.URL)
	}
	if !cfg.Notify.Fallback.Forward {
		t.Error("fallback.forward: true 应被解析")
	}

	t.Setenv("NOTIFY_FALLBACK_URL", "telegram://tok@telegram?chats=1")
	t.Setenv("NOTIFY_FALLBACK_FORWARD", "false")
	cfg, err = Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.Notify.Fallback.URL != "telegram://tok@telegram?chats=1" {
		t.Errorf("NOTIFY_FALLBACK_URL 应覆盖文件值，实际 %q", cfg.Notify.Fallback.URL)
	}
	if cfg.Notify.Fallback.Forward {
		t.Error("NOTIFY_FALLBACK_FORWARD=false 应覆盖文件值")
	}
}

// TestInstallScriptConfigShape 验证安装脚本生成形态（多渠道 + retry_failed + log.file，
// 含被转义的特殊字符密码）能被完整加载，防止脚本写出的 YAML 与解析端漂移。
func TestInstallScriptConfigShape(t *testing.T) {
	body := `cpe:
  host: "192.168.2.1"
  username: "admin"
  password: "p@ss\"word"
notify:
  urls:
    - "bark://:k@api.day.app?group=cpe-sms"
    - "ntfy://ntfy.sh/topic"
  notify_yield: true
  retry_failed: true

poll:
  interval_seconds: 60
  yield_minutes: 5

log:
  file: "/var/log/cpe-sms-forwarder.log"
`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("脚本形态配置应能加载，实际错误: %v", err)
	}
	if len(cfg.Notify.URLs) != 2 {
		t.Errorf("应解析出 2 个渠道，实际 %d", len(cfg.Notify.URLs))
	}
	if cfg.Notify.RetryFailed == nil || !*cfg.Notify.RetryFailed {
		t.Errorf("retry_failed 应为 true，实际 %v", cfg.Notify.RetryFailed)
	}
	if cfg.CPE.Password != `p@ss"word` {
		t.Errorf("密码转义还原不符: %q", cfg.CPE.Password)
	}
	if cfg.Log.File != "/var/log/cpe-sms-forwarder.log" {
		t.Errorf("log.file 解析不符: %q", cfg.Log.File)
	}
}
