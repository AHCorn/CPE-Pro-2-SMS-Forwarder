// Package config 负责加载和校验应用配置，支持 YAML 配置文件与环境变量。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// 转发短信的标题 / 正文默认模板，占位符 {phone} {content} {time}。
const (
	defaultSMSTitle = "CPE短信 - {phone}"
	defaultSMSBody  = "{content}\n\n{time}"
)

type Config struct {
	CPE    CPEConfig    `yaml:"cpe"`
	Notify NotifyConfig `yaml:"notify"`
	Poll   PollConfig   `yaml:"poll"`
	State  StateConfig  `yaml:"state"`
	Log    LogConfig    `yaml:"log"`
}

type CPEConfig struct {
	Host     string `yaml:"host"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// NotifyConfig 通知渠道设置。所有推送经 shoutrrr 统一发送，渠道与参数都由 URL 决定。
type NotifyConfig struct {
	// URLs shoutrrr 服务 URL 列表，一条一个渠道。例如：
	//   bark://:devicekey@api.day.app
	//   telegram://<bot-token>@telegram?chats=@channel
	//   ntfy://ntfy.sh/topic
	URLs []string `yaml:"urls"`
	// NotifyYield 是否在“因后台被占用而进入退避 / 退避结束恢复”时推送提醒。
	// 用指针区分“未设置”（nil→默认开启）与显式关闭（false）；不影响上下线/异常等其他通知。
	NotifyYield *bool `yaml:"notify_yield"`
	// RetryFailed 开启“可靠投递”：短信逐个渠道发送，部分渠道失败而另有渠道送达时，自动重试失败
	// 渠道；重试仍失败则通过已送达渠道发一条降级提示，同一渠道在恢复前只提示一次。任一渠道送达即
	// 视为已转发（不重复推送已成功渠道）。用指针区分未设置（nil→关闭，保持“全渠道一次性发送、
	// 全部成功才标记已读”的简单行为）与显式开启。
	RetryFailed *bool `yaml:"retry_failed"`
	// SMSTitle / SMSBody 自定义转发短信的标题与正文模板，支持占位符 {phone}（号码）、
	// {content}（短信正文）、{time}（时间）。留空各取默认值；模板里的字面 \n 会转成换行。
	// 分组 / 铃声等渠道特性由各 URL 的查询参数决定（如 bark://...?group=xxx&sound=yyy）。
	SMSTitle string `yaml:"sms_title"`
	SMSBody  string `yaml:"sms_body"`
	// Fallback 备用通知渠道，仅当所有主渠道对同一条消息全部推送失败时启用。
	Fallback FallbackConfig `yaml:"fallback"`
}

// FallbackConfig 备用通知渠道设置。主渠道部分失败不触发（那是 retry_failed 的职责）；
// 只有主渠道全军覆没时才动用，避免主渠道故障期间用户对一切失联无感。
type FallbackConfig struct {
	// URL 备用渠道的 shoutrrr URL（与 urls 同格式）；留空表示禁用备用渠道。
	URL string `yaml:"url"`
	// Forward true=主渠道全失败时经备用渠道转发消息本体（短信与系统通知），送达即视为已转发；
	// false=仅经备用渠道发限流的“主渠道离线”提示，短信本体留待主渠道恢复后自动补发。
	Forward bool `yaml:"forward"`
}

type PollConfig struct {
	IntervalSeconds int `yaml:"interval_seconds"`
	// ReloginMinutes 定期强制重新登录的间隔（分钟），<=0 时用默认值。
	// 用于规避长连接会话僵死（心跳仍通但拉不到新数据）导致的静默漏推。
	ReloginMinutes int `yaml:"relogin_minutes"`
	// YieldMinutes 当 Web 后台被其他设备登录占用时，本程序最多退避多少分钟后再强制登录（顶号），
	// 以便给手动登录后台的用户留出操作时间。0/未设置 → 默认 5；负数 → 禁用退避
	// （每轮直接登录，可能顶掉正在使用后台的用户）。
	YieldMinutes int `yaml:"yield_minutes"`
}

// StateConfig 去重状态持久化设置。
type StateConfig struct {
	// File 去重状态文件路径；为空时默认取配置文件同目录下的 seen.json。
	File string `yaml:"file"`
}

// LogConfig 日志设置。
type LogConfig struct {
	// File 日志文件路径。为空时：交互运行输出到标准错误；以服务方式运行时，
	// 类 Unix 由 journald/launchd/logd 捕获，Windows 默认落地到可执行文件同目录。
	File string `yaml:"file"`
}

// Load 从文件加载配置，然后用环境变量覆盖
// 优先级：环境变量 > 配置文件 > 默认值
func Load(path string) (*Config, error) {
	cfg := &Config{}

	data, err := os.ReadFile(path) // #nosec G304 -- path 来自命令行 -config 或默认值，由运维控制，非外部不可信输入
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置: %w", err)
		}
	}

	applyEnvOverrides(cfg)

	if cfg.Poll.IntervalSeconds <= 0 {
		cfg.Poll.IntervalSeconds = 60
	}
	if cfg.Poll.ReloginMinutes <= 0 {
		cfg.Poll.ReloginMinutes = 30
	}
	// 0/未设置 → 默认 5 分钟退避上限；负数保留原值表示显式禁用退避。
	if cfg.Poll.YieldMinutes == 0 {
		cfg.Poll.YieldMinutes = 5
	}
	// 退避通知默认开启（未设置时）。
	if cfg.Notify.NotifyYield == nil {
		enabled := true
		cfg.Notify.NotifyYield = &enabled
	}
	cfg.Notify.URLs = cleanURLs(cfg.Notify.URLs)
	cfg.Notify.Fallback.URL = strings.TrimSpace(cfg.Notify.Fallback.URL)
	if cfg.Notify.SMSTitle == "" {
		cfg.Notify.SMSTitle = defaultSMSTitle
	}
	if cfg.Notify.SMSBody == "" {
		cfg.Notify.SMSBody = defaultSMSBody
	}
	// 允许在配置文件 / 环境变量里用字面 \n 表示换行，统一转成真实换行。
	cfg.Notify.SMSTitle = strings.ReplaceAll(cfg.Notify.SMSTitle, `\n`, "\n")
	cfg.Notify.SMSBody = strings.ReplaceAll(cfg.Notify.SMSBody, `\n`, "\n")
	if cfg.State.File == "" {
		cfg.State.File = filepath.Join(filepath.Dir(path), "seen.json")
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("CPE_HOST"); v != "" {
		cfg.CPE.Host = v
	}
	if v := os.Getenv("CPE_USERNAME"); v != "" {
		cfg.CPE.Username = v
	}
	if v := os.Getenv("CPE_PASSWORD"); v != "" {
		cfg.CPE.Password = v
	}
	// NOTIFY_URLS 按空白字符（空格 / 换行）分隔，避免与 URL 中的逗号（如 telegram chats）冲突。
	if v := os.Getenv("NOTIFY_URLS"); v != "" {
		cfg.Notify.URLs = strings.Fields(v)
	}
	if v := os.Getenv("NOTIFY_YIELD"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Notify.NotifyYield = &b
		}
	}
	if v := os.Getenv("NOTIFY_RETRY_FAILED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Notify.RetryFailed = &b
		}
	}
	if v := os.Getenv("NOTIFY_FALLBACK_URL"); v != "" {
		cfg.Notify.Fallback.URL = v
	}
	if v := os.Getenv("NOTIFY_FALLBACK_FORWARD"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Notify.Fallback.Forward = b
		}
	}
	if v := os.Getenv("NOTIFY_SMS_TITLE"); v != "" {
		cfg.Notify.SMSTitle = v
	}
	if v := os.Getenv("NOTIFY_SMS_BODY"); v != "" {
		cfg.Notify.SMSBody = v
	}
	if v := os.Getenv("CPE_POLL_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Poll.IntervalSeconds = n
		}
	}
	if v := os.Getenv("CPE_RELOGIN_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Poll.ReloginMinutes = n
		}
	}
	// 允许负数（显式禁用退避），故只要能解析即采用
	if v := os.Getenv("CPE_YIELD_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Poll.YieldMinutes = n
		}
	}
	if v := os.Getenv("CPE_STATE_FILE"); v != "" {
		cfg.State.File = v
	}
	if v := os.Getenv("LOG_FILE"); v != "" {
		cfg.Log.File = v
	}
}

func validate(cfg *Config) error {
	if cfg.CPE.Host == "" {
		return fmt.Errorf("cpe.host 不能为空 (设置 CPE_HOST 环境变量或在配置文件中指定)")
	}
	if cfg.CPE.Username == "" {
		return fmt.Errorf("cpe.username 不能为空 (设置 CPE_USERNAME 或在配置文件中指定)")
	}
	if cfg.CPE.Password == "" {
		return fmt.Errorf("cpe.password 不能为空 (设置 CPE_PASSWORD 或在配置文件中指定)")
	}
	if len(cfg.Notify.URLs) == 0 {
		return fmt.Errorf("notify.urls 不能为空 (设置 NOTIFY_URLS 或在配置文件中至少配置一个通知渠道 URL，如 bark://:devicekey@api.day.app)")
	}
	return nil
}

// cleanURLs 去除空白与空项，避免 YAML 中的空行被当作无效渠道传入 shoutrrr。
func cleanURLs(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if s := strings.TrimSpace(u); s != "" {
			out = append(out, s)
		}
	}
	return out
}
