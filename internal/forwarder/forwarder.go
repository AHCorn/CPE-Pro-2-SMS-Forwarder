// Package forwarder 实现短信轮询与通知推送的核心业务逻辑。
package forwarder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nicholas-fedor/shoutrrr"

	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/config"
	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/cpe"
)

const (
	// failureAlertThreshold 连续轮询失败多少次后推送异常告警
	failureAlertThreshold = 3
	// watchdogMinTick watchdog 最小检查周期
	watchdogMinTick = 30 * time.Second
	// watchdogMinThreshold watchdog 卡死判定最小阈值，
	// 避免轮询间隔极短时频繁误报
	watchdogMinThreshold = 3 * time.Minute
	// watchdogStaleFactor 卡死阈值 = 轮询间隔 × 该倍数
	watchdogStaleFactor = 3
)

// errYielding 表示本轮因检测到后台正被其他设备占用而主动退避（不顶号），
// 属于预期行为而非故障；poll 据此跳过本轮且不计入失败/告警统计。
var errYielding = errors.New("yielding to active backend user")

// loginAction 是 decideLoginAction 的决策结果。
type loginAction int

const (
	// actionLogin 正常登录：无人占用，或占用者其实是自身（陈旧会话 / 定期重登）。
	actionLogin loginAction = iota
	// actionForceLogin 强制登录（顶号）：后台被他人占用且退避已达上限。
	actionForceLogin
	// actionYield 本轮退避不登录，避免顶掉正在使用后台的其他设备。
	actionYield
)

// decideLoginAction 依据 other_logged 探测结果与退避计时，决定本轮是正常登录、强制顶号还是退避。
//   - occupied/holderIP：路由器报告的会话占用状态与持有者源 IP；
//   - localIP：本程序连接的本地源 IP，用于识别"占用者其实是自己"（自身陈旧会话或定期重登场景）；
//   - yieldingSince：本次退避起点（零值表示当前未退避）；now：当前时间；forceInterval：退避上限。
//
// 返回决策动作与更新后的 yieldingSince（actionLogin/actionForceLogin 时恒为零值，表示退避结束/未开始）。
// 纯函数，无副作用，便于单测覆盖各分支。
func decideLoginAction(occupied bool, holderIP, localIP string, yieldingSince, now time.Time, forceInterval time.Duration) (loginAction, time.Time) {
	// 仅当占用者 IP 与本地 IP 都已知且相等时，才判定为"自身会话"，避免空 IP 误判为自己。
	selfHeld := occupied && localIP != "" && holderIP == localIP
	if !occupied || selfHeld {
		return actionLogin, time.Time{}
	}
	if yieldingSince.IsZero() {
		yieldingSince = now
	}
	if now.Sub(yieldingSince) >= forceInterval {
		return actionForceLogin, time.Time{}
	}
	return actionYield, yieldingSince
}

// Forwarder 负责轮询 CPE 短信并经 shoutrrr 推送到各通知渠道。
// 除短信转发外，还提供服务级告警（上线 / 下线 / 连续失败 / 长时间卡死 / 恢复），
// 并通过 panic 捕获 + 连接重建实现自恢复，避免单次异常导致进程假死。
type Forwarder struct {
	cfg      *config.Config
	client   *cpe.Client
	channels []*notifyChannel
	// seen 短信指纹去重集合；seenOrder 记录指纹的插入顺序，供 pruneSeen 做 FIFO 淘汰
	// （只淘汰最旧的，保留最近指纹），避免整表清空后把收件箱现存短信全部当新短信重推。
	// 两者由 seenMu 保护，且恒满足 len(seen)==len(seenOrder)（仅经 markSeenLocked 插入）。
	seen        map[string]bool
	seenOrder   []string
	seenMu      sync.Mutex
	loggedIn    bool
	initialized bool // 首次拉取完成后置为 true，首次只记录不推送

	// sendMu 串行化推送：轮询 goroutine 与 watchdog goroutine 都会触发通知，加锁避免对各渠道
	// 发送器的并发调用；同时保护 lastDegradeAlert、cycleTripped、fallbackTripped、
	// lastFallbackAlert（仅在持有 sendMu 时读写）。
	sendMu sync.Mutex
	// lastDegradeAlert 记录各渠道（按 channels 下标）上次降级提示的时刻，用于硬节流防刷屏。
	lastDegradeAlert map[int]time.Time

	// fallback 备用通知渠道；nil 表示未配置。仅当所有主渠道对同一条消息全部失败时启用。
	fallback *notifyChannel
	// fallbackForward true=主渠道全失败时经备用渠道转发消息本体；false=仅发“主渠道离线”提示。
	fallbackForward bool
	// lastFallbackAlert 上次经备用渠道发“主渠道离线”提示的时刻，按 fallbackAlertInterval 节流。
	lastFallbackAlert time.Time

	// cycleTripped 本轮询周期内已熔断的主渠道下标集合：渠道在本周期内确认失败后不再反复尝试，
	// 避免积压短信 × 渠道超时把单轮拉长到分钟级（会连带拖死 CPE 会话、误触 watchdog）。
	// 每轮 poll 开始时重置，使渠道恢复能在下一轮立即被发现。
	cycleTripped map[int]bool
	// fallbackTripped 备用渠道在本轮询周期内的熔断标记，语义同 cycleTripped。
	fallbackTripped bool

	// statePath 去重状态持久化文件路径；为空则禁用持久化。
	statePath string
	// reloginInterval 定期强制重新登录的间隔；<=0 表示禁用，仅靠心跳维持会话。
	reloginInterval time.Duration
	// lastLoginAt 最近一次完整登录成功的时刻，供定期强制重登判定（仅轮询 goroutine 访问）。
	lastLoginAt time.Time

	// yieldForceInterval 后台被他人占用时，最多退避多久后强制登录（顶号）；<=0 表示禁用退避。
	yieldForceInterval time.Duration
	// yieldingSince 本次退避起点（零值表示当前未退避）；仅轮询 goroutine 访问。
	yieldingSince time.Time
	// yieldNotified 本次退避是否已推送过暂停通知，避免每轮重复推送；仅轮询 goroutine 访问。
	yieldNotified bool
	// notifyYield 是否在进入退避 / 恢复时推送提醒（来自 notify.notify_yield，默认开启）。
	notifyYield bool
	// retryFailed 是否启用可靠投递（来自 notify.retry_failed，默认关闭）。
	retryFailed bool
	// retryBackoff 可靠投递重试的基础退避，提取为字段便于单测调小；New 时取默认值。
	retryBackoff time.Duration
	// smsTitle / smsBody 转发短信的标题与正文模板（来自 notify.sms_title / notify.sms_body），
	// 推送时替换占位符 {phone} {content} {time}。
	smsTitle string
	smsBody  string

	// lastSuccessAt 最近一次轮询成功的 Unix 秒，供 watchdog 判定卡死
	lastSuccessAt atomic.Int64
	// failCount 当前连续失败次数，成功后清零
	failCount atomic.Int32
	// alerting 是否已推送过"连续失败"告警（未恢复前不重复推送）
	alerting atomic.Bool
	// stallAlerting 是否已推送过 watchdog 卡死告警（未恢复前不重复推送）
	stallAlerting atomic.Bool
}

func New(cfg *config.Config) (*Forwarder, error) {
	channels := make([]*notifyChannel, 0, len(cfg.Notify.URLs))
	for i, u := range cfg.Notify.URLs {
		sender, err := shoutrrr.CreateSender(u)
		if err != nil {
			return nil, fmt.Errorf("初始化通知渠道 %s: %w", channelLabel(i, u), err)
		}
		channels = append(channels, &notifyChannel{name: channelLabel(i, u), sender: &shoutrrrChannel{router: sender}})
	}
	f := &Forwarder{
		cfg:                cfg,
		client:             cpe.NewClient(cfg.CPE.Host, cfg.CPE.Username, cfg.CPE.Password),
		channels:           channels,
		lastDegradeAlert:   make(map[int]time.Time),
		cycleTripped:       make(map[int]bool),
		seen:               make(map[string]bool),
		statePath:          cfg.State.File,
		reloginInterval:    time.Duration(cfg.Poll.ReloginMinutes) * time.Minute,
		yieldForceInterval: time.Duration(cfg.Poll.YieldMinutes) * time.Minute,
		notifyYield:        cfg.Notify.NotifyYield == nil || *cfg.Notify.NotifyYield,
		retryFailed:        cfg.Notify.RetryFailed != nil && *cfg.Notify.RetryFailed,
		retryBackoff:       defaultChannelRetryBackoff,
		smsTitle:           cfg.Notify.SMSTitle,
		smsBody:            cfg.Notify.SMSBody,
	}
	if u := cfg.Notify.Fallback.URL; u != "" {
		sender, err := shoutrrr.CreateSender(u)
		if err != nil {
			return nil, fmt.Errorf("初始化%s: %w", fallbackLabel(u), err)
		}
		f.fallback = &notifyChannel{name: fallbackLabel(u), sender: &shoutrrrChannel{router: sender}}
		f.fallbackForward = cfg.Notify.Fallback.Forward
	}
	f.loadState()
	return f, nil
}

// loadState 从磁盘加载去重状态。状态文件存在（即便为空）即视为非首次启动，
// 跳过基线化，使停机期间到达的短信仍会被推送，而不是被当作历史短信吞掉。
func (f *Forwarder) loadState() {
	if f.statePath == "" {
		return
	}
	data, err := os.ReadFile(f.statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("读取去重状态 %s 失败（按首次启动处理）: %v", f.statePath, err)
		}
		return
	}
	var st struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		log.Printf("解析去重状态 %s 失败（按首次启动处理）: %v", f.statePath, err)
		return
	}
	f.seenMu.Lock()
	for _, k := range st.Keys {
		f.markSeenLocked(k)
	}
	loaded := len(f.seenOrder)
	f.seenMu.Unlock()
	// 状态文件存在即说明此前已建立过基线，无需再次基线化。
	f.initialized = true
	log.Printf("已加载去重状态: %d 条 (%s)", loaded, f.statePath)
}

// markSeenLocked 记录一个指纹（去重 + 维护插入顺序）；已存在则不重复记，
// 返回是否为新指纹。调用方必须持有 seenMu。
func (f *Forwarder) markSeenLocked(key string) bool {
	if f.seen[key] {
		return false
	}
	f.seen[key] = true
	f.seenOrder = append(f.seenOrder, key)
	return true
}

// saveState 原子写入去重状态（temp + rename）；失败仅记日志，不影响转发主流程。
// 按插入顺序（seenOrder）落盘，使 FIFO 淘汰顺序跨重启保持一致。
func (f *Forwarder) saveState() {
	if f.statePath == "" {
		return
	}
	f.seenMu.Lock()
	keys := make([]string, len(f.seenOrder))
	copy(keys, f.seenOrder)
	f.seenMu.Unlock()

	data, err := json.Marshal(struct {
		Keys []string `json:"keys"`
	}{Keys: keys})
	if err != nil {
		log.Printf("序列化去重状态失败: %v", err)
		return
	}
	if dir := filepath.Dir(f.statePath); dir != "" {
		_ = os.MkdirAll(dir, 0o750)
	}
	tmp := f.statePath + ".tmp"
	// 0o600：去重指纹按用户私有数据对待，遵循最小权限，不让同机其他用户读取。
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("写入去重状态临时文件失败: %v", err)
		return
	}
	if err := os.Rename(tmp, f.statePath); err != nil {
		log.Printf("替换去重状态文件失败: %v", err)
		_ = os.Remove(tmp)
	}
}

// Run 启动轮询循环，入口推送上线、出口推送下线，
// 后台 watchdog 独立监测卡死。Run 在 ctx 取消后返回。
func (f *Forwarder) Run(ctx context.Context) {
	f.notifyStartup()
	defer f.notifyShutdown()

	// watchdog 与主循环共享 ctx，ctx 取消时一并退出
	go f.watchdog(ctx)

	f.safePoll()

	ticker := time.NewTicker(time.Duration(f.cfg.Poll.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("轮询已停止")
			return
		case <-ticker.C:
			f.safePoll()
		}
	}
}

// safePoll 包装 poll，捕获 panic 以防止 goroutine 崩溃，
// 并主动重置登录态，使下一次轮询能重新建立连接。
func (f *Forwarder) safePoll() {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			log.Printf("轮询发生 panic: %v\n%s", r, stack)
			// 强制下次重新登录，避免在坏连接上继续
			f.loggedIn = false
			// panic 视为一次失败，计入告警统计
			f.onPollFailure(fmt.Sprintf("panic: %v", r))
			f.notify("CPE 服务 panic 已自动恢复",
				fmt.Sprintf("轮询过程中捕获到 panic，进程未退出，下一轮将重新登录。\n\n%v", r))
		}
	}()
	f.poll()
}

func (f *Forwarder) ensureLogin() error {
	if f.loggedIn {
		// 定期强制重登：长连接会话可能"心跳仍通但拉不到新数据"地僵死，
		// 到达重登间隔后主动丢弃会话、走完整登录，规避静默漏推。
		switch {
		case f.reloginInterval > 0 && time.Since(f.lastLoginAt) >= f.reloginInterval:
			log.Printf("达到重登间隔（%s），强制重新登录", f.reloginInterval)
			f.loggedIn = false
		case f.client.Heartbeat() == nil:
			return nil
		default:
			log.Printf("心跳失败，重新登录...")
			f.loggedIn = false
		}
	}

	// 重新构造 client 以丢弃可能处于异常状态的 TCP 连接
	f.client = cpe.NewClient(f.cfg.CPE.Host, f.cfg.CPE.Username, f.cfg.CPE.Password)

	// 登录前的自动退避判定：若后台正被其他设备占用，本轮不登录（不顶号），
	// 直到对方退出或退避时长超过 yieldForceInterval 才强制登录一次。
	if f.yieldForceInterval > 0 {
		if err := f.applyYieldPolicy(); err != nil {
			return err
		}
	}

	if err := f.client.Login(); err != nil {
		return err
	}
	f.loggedIn = true
	f.lastLoginAt = time.Now()
	if f.yieldNotified {
		f.yieldNotified = false
		f.notify("CPE 短信转发已恢复",
			fmt.Sprintf("检测到后台已退出登录（或暂停已达 %.0f 分钟上限），短信转发已恢复正常。",
				f.yieldForceInterval.Minutes()))
	}
	f.yieldingSince = time.Time{}
	return nil
}

// applyYieldPolicy 在登录前探测后台占用状态，并据此决定退避或放行。
// 返回 errYielding 表示本轮主动退避（不登录）；返回 nil 表示可继续登录（含退避超时后的强制登录）。
// 探测失败时按"放行登录"处理，以优先保障短信转发，不在网络异常时无谓暂停。
func (f *Forwarder) applyYieldPolicy() error {
	occupied, holderIP, err := f.client.OtherLogged()
	if err != nil {
		log.Printf("查询后台占用状态失败，按直接登录处理: %v", err)
		return nil
	}
	// 成功探测到路由器即视为一次健康交互，刷新存活时间戳，避免退避期间 watchdog 误报卡死。
	f.lastSuccessAt.Store(time.Now().Unix())

	action, since := decideLoginAction(occupied, holderIP, f.client.LocalIP(), f.yieldingSince, time.Now(), f.yieldForceInterval)
	f.yieldingSince = since

	switch action {
	case actionYield:
		elapsed := time.Since(f.yieldingSince).Truncate(time.Second)
		log.Printf("检测到后台被 %s 占用，短信轮询退避中（已退避 %s，最长 %s 后强制登录）",
			holderIP, elapsed, f.yieldForceInterval)
		// 仅在开启退避通知时推送，并保证整段退避只推一次（yieldNotified）。
		// 关闭时 yieldNotified 不会置真，恢复登录时的“已恢复”通知也随之不推。
		if f.notifyYield && !f.yieldNotified {
			f.yieldNotified = true
			f.notify("CPE 短信转发已暂停",
				fmt.Sprintf("检测到 %s 正在登录路由器后台。后台同一时间仅允许一个用户在线，为避免影响您的操作，短信转发已暂停；最长 %.0f 分钟后自动恢复，期间收到的短信将在恢复后补发。",
					holderIP, f.yieldForceInterval.Minutes()))
		}
		return errYielding
	case actionForceLogin:
		log.Printf("退避已达上限 %s，强制登录（将顶掉 %s）", f.yieldForceInterval, holderIP)
	case actionLogin:
		// 无人占用或占用者是自身，正常登录，无需额外日志。
	}
	return nil
}

func (f *Forwarder) poll() {
	log.Println("开始轮询短信...")
	f.beginPollCycle()

	if err := f.ensureLogin(); err != nil {
		if errors.Is(err, errYielding) {
			// 主动退避，等待占用方退出或退避超时；非故障，不计入失败统计、不触发告警。
			return
		}
		log.Printf("登录失败: %v", err)
		f.onPollFailure(fmt.Sprintf("登录失败: %v", err))
		return
	}

	sessions, err := f.client.FetchSMS()
	if err != nil {
		log.Printf("获取短信失败: %v (将在下次轮询重试)", err)
		// 失败时主动丢弃登录态，下一轮重新走完整登录流程
		f.loggedIn = false
		f.onPollFailure(fmt.Sprintf("获取短信失败: %v", err))
		return
	}

	totalMsgs := 0
	for _, s := range sessions {
		totalMsgs += len(s.Messages)
	}
	log.Printf("获取到 %d 个联系人, %d 条短信", len(sessions), totalMsgs)

	// 首次拉取只记录已有短信，不推送，避免重启后重复推送历史短信
	if !f.initialized {
		baseline := f.scanNew(sessions)
		f.seenMu.Lock()
		for _, p := range baseline {
			f.markSeenLocked(p.key)
		}
		f.seenMu.Unlock()
		f.initialized = true
		f.saveState()
		log.Printf("首次启动，记录 %d 条已有短信（不推送），后续新短信将自动推送", len(baseline))
		f.onPollSuccess()
		return
	}

	pending := f.scanNew(sessions)
	pushed, exhausted := 0, false
	if len(pending) > 0 {
		pushed, exhausted = f.flushPending(pending)
	}

	switch {
	case len(pending) == 0:
		log.Println("没有新短信")
	case pushed == len(pending):
		log.Printf("本次推送 %d 条新短信", pushed)
	case exhausted:
		log.Printf("通知渠道本轮全部不可用：发现 %d 条新短信仅送达 %d 条，剩余留待下轮重试", len(pending), pushed)
	default:
		log.Printf("发现 %d 条新短信，送达 %d 条，其余留待下轮重试", len(pending), pushed)
	}
	// 仅在真的新增了已读指纹时才落盘，避免推送故障期间每轮原样重写 seen.json 磨损 flash。
	if pushed > 0 {
		f.saveState()
	}

	f.pruneSeen()
	f.onPollSuccess()
}

// onPollSuccess 记录一次成功轮询：刷新时间戳、清零失败计数，
// 若此前处于告警状态则额外推送一次"已恢复"通知。
func (f *Forwarder) onPollSuccess() {
	f.lastSuccessAt.Store(time.Now().Unix())
	prevFail := f.failCount.Swap(0)
	wasAlerting := f.alerting.Swap(false)
	wasStall := f.stallAlerting.Swap(false)
	if wasAlerting || wasStall {
		body := "CPE 短信转发已恢复正常。"
		if prevFail > 0 {
			body = fmt.Sprintf("CPE 短信转发已恢复正常（之前连续失败 %d 次）。", prevFail)
		}
		f.notify("CPE 服务已恢复", body)
	}
}

// onPollFailure 累计一次失败，达到阈值后推送一次告警（直至恢复前不重复推送）。
func (f *Forwarder) onPollFailure(reason string) {
	n := f.failCount.Add(1)
	if int(n) >= failureAlertThreshold && !f.alerting.Swap(true) {
		f.notify("CPE 服务异常",
			fmt.Sprintf("已连续 %d 次轮询失败，程序仍在运行并持续重试。\n\n最近原因: %s", n, reason))
	}
}

// watchdog 独立监督轮询健康度。
// 若距离上次成功轮询的时间超过"轮询间隔 × watchdogStaleFactor"
// （不低于 watchdogMinThreshold），推送一次卡死告警。
// 这种情形对应 HTTP 虽未超时但路由器侧一直返回异常等"进程活着却不干活"的场景。
func (f *Forwarder) watchdog(ctx context.Context) {
	interval := time.Duration(f.cfg.Poll.IntervalSeconds) * time.Second
	threshold := interval * watchdogStaleFactor
	if threshold < watchdogMinThreshold {
		threshold = watchdogMinThreshold
	}
	tickInterval := interval / 2
	if tickInterval < watchdogMinTick {
		tickInterval = watchdogMinTick
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last := f.lastSuccessAt.Load()
			if last == 0 {
				// 尚无成功轮询基线，不判定卡死
				continue
			}
			stale := time.Since(time.Unix(last, 0))
			if stale > threshold && !f.stallAlerting.Swap(true) {
				f.notify("CPE 服务长时间无响应",
					fmt.Sprintf("距离上次成功轮询已 %s（告警阈值 %s），服务可能已假死。程序仍在持续重试。",
						stale.Truncate(time.Second), threshold))
			}
		}
	}
}

// notifyStartup 服务上线通知
func (f *Forwarder) notifyStartup() {
	body := fmt.Sprintf("host=%s 轮询间隔=%ds", f.cfg.CPE.Host, f.cfg.Poll.IntervalSeconds)
	f.notify("CPE 服务已上线", body)
}

// notifyShutdown 服务下线通知（SIGTERM/SIGINT 触发 ctx 取消后执行）
func (f *Forwarder) notifyShutdown() {
	f.notify("CPE 服务已下线", "服务已正常停止。")
}

func smsKey(phone, content, timeStr string) string {
	h := sha256.New()
	h.Write([]byte(phone + "|" + content + "|" + timeStr))
	return hex.EncodeToString(h.Sum(nil))
}

// maskPhone 仅保留号码末 4 位、其余以 * 代替，避免完整手机号(PII)落入可能被同机其他用户
// 读到的日志（如 Windows ProgramData、systemd journald）。短于等于 4 位则整体打码。
func maskPhone(phone string) string {
	r := []rune(phone)
	if len(r) <= 4 {
		return strings.Repeat("*", len(r))
	}
	return strings.Repeat("*", len(r)-4) + string(r[len(r)-4:])
}

// pruneSeen 限制去重集合规模。超过上限时按 FIFO 只淘汰最旧的指纹、保留最近的，
// 而非整表清空——后者会让下一轮把路由器收件箱里现存的短信全部当作新短信重推。
// 最近指纹覆盖当前收件箱，淘汰的多是早已离开收件箱的历史短信，故不会造成重复推送。
func (f *Forwarder) pruneSeen() {
	const maxSeen = 10000
	f.seenMu.Lock()
	pruned := false
	if len(f.seenOrder) > maxSeen {
		drop := len(f.seenOrder) - maxSeen
		for _, k := range f.seenOrder[:drop] {
			delete(f.seen, k)
		}
		// 拷贝到新切片，释放旧底层数组、避免淘汰的指纹被长期持有。
		f.seenOrder = append([]string(nil), f.seenOrder[drop:]...)
		pruned = true
	}
	f.seenMu.Unlock()
	if pruned {
		f.saveState()
	}
}
