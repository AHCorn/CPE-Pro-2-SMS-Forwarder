// 通知投递层：渠道抽象、严格/可靠两种投递模式、按轮熔断、备用渠道兜底与降级提示。
// 与轮询逻辑（forwarder.go）解耦，所有可变投递状态由 Forwarder.sendMu 保护。

package forwarder

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

const (
	// channelRetryMax 可靠投递模式下，对“部分渠道成功”时仍失败渠道的最大额外重试次数。
	channelRetryMax = 2
	// defaultChannelRetryBackoff 重试基础退避：第 k 次重试前等待 k × 该值（线性递增），
	// 仅“部分渠道成功”时触发，故对单条消息的额外阻塞上限可控（约 1+2 倍基值）。
	defaultChannelRetryBackoff = 2 * time.Second
	// degradeAlertInterval 同一渠道两次降级提示的最小间隔，硬节流防止持续故障时刷屏。
	degradeAlertInterval = 10 * time.Minute
	// fallbackAlertInterval 备用渠道两次“主渠道离线”提示的最小间隔。主渠道故障期间每轮
	// 每条消息都会走到全失败分支，不节流会把备用渠道刷成新的噪音源。
	fallbackAlertInterval = 10 * time.Minute
)

// channelSender 抽象单个通知渠道的发送，便于可靠投递按渠道重试，并支持单测注入。
type channelSender interface {
	// Send 发送一条消息，返回该渠道的聚合错误（nil 表示送达）。
	Send(title, body string) error
}

// notifyChannel 绑定脱敏渠道标识与其发送器。name 不含 URL 中的密钥 / token，可安全写入日志与提示。
type notifyChannel struct {
	name   string
	sender channelSender
}

// shoutrrrChannel 用单条 shoutrrr URL 实现 channelSender。
type shoutrrrChannel struct {
	router *router.ServiceRouter
}

func (c *shoutrrrChannel) Send(title, body string) error {
	params := types.Params{}
	params.SetTitle(title)
	var failures []string
	for _, err := range c.router.Send(body, &params) {
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// channelLabel 从 shoutrrr URL 提取脱敏渠道标识（仅 scheme + 序号），避免 token / 密钥落入日志或通知。
// 仅在能取到 scheme 时附带显示；取不到（异常 URL）则只用序号，绝不回显可能含 token 的原始 URL。
func channelLabel(idx int, rawURL string) string {
	if i := strings.IndexByte(rawURL, ':'); i > 0 {
		return fmt.Sprintf("渠道%d(%s)", idx+1, rawURL[:i])
	}
	return fmt.Sprintf("渠道%d", idx+1)
}

// fallbackLabel 备用渠道的脱敏标识，规则同 channelLabel（仅 scheme，不回显原始 URL）。
func fallbackLabel(rawURL string) string {
	if i := strings.IndexByte(rawURL, ':'); i > 0 {
		return fmt.Sprintf("备用渠道(%s)", rawURL[:i])
	}
	return "备用渠道"
}

// pushSMS 按配置的标题 / 正文模板渲染一条新短信并推送，占位符 {phone} {content} {time}
// 来自 notify.sms_title / notify.sms_body。短信走可靠投递路径（allowAlert=true，允许降级提示）。
func (f *Forwarder) pushSMS(phone, content, smsTime string) error {
	r := strings.NewReplacer("{phone}", phone, "{content}", content, "{time}", smsTime)
	return f.deliver(r.Replace(f.smsTitle), r.Replace(f.smsBody), true)
}

// notify 推送系统类通知（上下线 / 异常 / 恢复 / 卡死 / 退避），失败仅记日志不阻断主流程。
// 系统通知不触发降级提示（allowAlert=false），避免“提示发送失败又发提示”的递归刷屏。
func (f *Forwarder) notify(title, body string) {
	if err := f.deliver(title, body, false); err != nil {
		log.Printf("通知推送失败 [%s]: %v", title, err)
	}
}

// deliver 统一发送入口：未开启可靠投递时维持“全渠道一次性发送、全部成功才算成功”的简单行为；
// 开启时按渠道重试，并在“部分成功”场景对持续失败的渠道发降级提示（任一主渠道送达即视为成功）。
// 两种模式下，若所有主渠道均未送达，则交由备用渠道策略兜底（applyFallbackLocked）。
// allowAlert 仅短信为 true。
func (f *Forwarder) deliver(title, body string, allowAlert bool) error {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()

	var failed []int
	if f.retryFailed {
		failed = f.sendReliableLocked(title, body, allowAlert)
	} else {
		failed = f.sendStrictLocked(title, body)
	}

	if len(failed) == len(f.channels) {
		return f.applyFallbackLocked(title, body)
	}
	if len(failed) == 0 || f.retryFailed {
		return nil
	}
	names := make([]string, len(failed))
	for i, idx := range failed {
		names[i] = f.channels[idx].name
	}
	return fmt.Errorf("以下渠道推送失败: %s", strings.Join(names, ", "))
}

// sendStrictLocked 向所有主渠道各发送一次（本轮已熔断的直接计失败），失败渠道计入本轮熔断。
// 返回未送达的渠道下标。调用方须持有 sendMu。
func (f *Forwarder) sendStrictLocked(title, body string) []int {
	failedTried, skipped := f.dispatchLocked(title, body, f.allChannelIdx())
	f.tripLocked(failedTried)
	return append(failedTried, skipped...)
}

// sendReliableLocked 可靠投递：先各发一次；全失败不重试（留待短信下一轮重推 / 系统通知失败计数）；
// 仅“部分渠道成功”时对本次实际尝试且失败的渠道做有限次线性退避重试（本轮已熔断的不参与），
// 重试仍失败则计入熔断并经送达渠道发降级提示（按渠道节流防刷屏）。
// 返回未送达的渠道下标。调用方须持有 sendMu。
func (f *Forwarder) sendReliableLocked(title, body string, allowAlert bool) []int {
	failedTried, skipped := f.dispatchLocked(title, body, f.allChannelIdx())
	if len(failedTried)+len(skipped) == 0 {
		return nil
	}
	if len(failedTried)+len(skipped) == len(f.channels) {
		f.tripLocked(failedTried)
		return append(failedTried, skipped...)
	}

	for attempt := 1; attempt <= channelRetryMax && len(failedTried) > 0; attempt++ {
		time.Sleep(time.Duration(attempt) * f.retryBackoff)
		failedTried, _ = f.dispatchLocked(title, body, failedTried)
	}
	f.tripLocked(failedTried)
	failed := append(failedTried, skipped...)
	if len(failed) > 0 && allowAlert {
		f.alertDegraded(failed)
	}
	return failed
}

// dispatchLocked 向 idxs 中的渠道各发送一次；本轮已熔断的渠道直接跳过（不发起请求、不重复记日志）。
// 返回本次实际尝试且失败的下标（failedTried）与因熔断被跳过的下标（skipped）。调用方须持有 sendMu。
func (f *Forwarder) dispatchLocked(title, body string, idxs []int) (failedTried, skipped []int) {
	for _, i := range idxs {
		if f.cycleTripped[i] {
			skipped = append(skipped, i)
			continue
		}
		if err := f.channels[i].sender.Send(title, body); err != nil {
			log.Printf("%s 推送失败: %v", f.channels[i].name, err)
			failedTried = append(failedTried, i)
		}
	}
	return failedTried, skipped
}

// tripLocked 将确认失败的渠道计入本轮熔断集合：同一轮询周期内不再对其发起请求，
// 防止积压消息 × 渠道超时把单轮拉长到分钟级（进而拖死 CPE 会话、误触 watchdog）。
// 调用方须持有 sendMu。
func (f *Forwarder) tripLocked(idxs []int) {
	for _, i := range idxs {
		if !f.cycleTripped[i] {
			f.cycleTripped[i] = true
			log.Printf("%s 本轮熔断，下轮重新探测", f.channels[i].name)
		}
	}
}

// beginPollCycle 在每轮轮询开始时重置熔断状态，使故障渠道的恢复能在新一轮立即被发现。
func (f *Forwarder) beginPollCycle() {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	if len(f.cycleTripped) > 0 {
		f.cycleTripped = make(map[int]bool)
	}
	f.fallbackTripped = false
}

// canDeliverSMS 判断本轮是否仍存在可能送达短信的通道：任一主渠道未熔断，
// 或备用渠道处于转发模式且未熔断。全部不可用时轮询应跳过剩余短信，留待下一轮。
func (f *Forwarder) canDeliverSMS() bool {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	for i := range f.channels {
		if !f.cycleTripped[i] {
			return true
		}
	}
	return f.fallback != nil && f.fallbackForward && !f.fallbackTripped
}

// applyFallbackLocked 处理“所有主渠道均未送达”：未配置备用渠道时返回全失败错误；
// 转发模式（forward=true）经备用渠道发原消息，送达即视为成功（短信据此标记已读，不再补发）；
// 提示模式（forward=false）仅按 fallbackAlertInterval 节流发一条“主渠道离线”，原消息仍按失败
// 处理，短信留在未读集合、主渠道恢复后自动补发。调用方须持有 sendMu。
func (f *Forwarder) applyFallbackLocked(title, body string) error {
	allFailed := fmt.Errorf("所有 %d 个通知渠道均推送失败", len(f.channels))
	if f.fallback == nil {
		return allFailed
	}

	if f.fallbackForward {
		if f.fallbackTripped {
			return fmt.Errorf("%w，%s本轮已熔断", allFailed, f.fallback.name)
		}
		if err := f.fallback.sender.Send(title, body); err != nil {
			log.Printf("%s 推送失败: %v", f.fallback.name, err)
			f.fallbackTripped = true
			return fmt.Errorf("%w，且%s发送失败", allFailed, f.fallback.name)
		}
		log.Printf("主渠道全部失败，已改经 %s 送达", f.fallback.name)
		return nil
	}

	if f.fallbackTripped || time.Since(f.lastFallbackAlert) < fallbackAlertInterval {
		return allFailed
	}
	alertBody := fmt.Sprintf("所有主通知渠道（%d 个）推送失败，最新未送达消息：%s。主渠道恢复前新短信将积压，恢复后自动补发。",
		len(f.channels), title)
	if err := f.fallback.sender.Send("CPE 通知主渠道离线", alertBody); err != nil {
		log.Printf("%s 发送主渠道离线提示失败: %v", f.fallback.name, err)
		f.fallbackTripped = true
	} else {
		f.lastFallbackAlert = time.Now()
	}
	return allFailed
}

// alertDegraded 经已送达渠道发一次降级提示，告知哪些渠道多次失败、可能漏收。
// 每个渠道按 degradeAlertInterval 节流，持续故障时不会每条短信都提示。调用方须持有 sendMu。
func (f *Forwarder) alertDegraded(failed []int) {
	up := f.complementIdx(failed)
	if len(up) == 0 {
		return
	}
	now := time.Now()
	var newly []string
	for _, i := range failed {
		if last, ok := f.lastDegradeAlert[i]; ok && now.Sub(last) < degradeAlertInterval {
			continue
		}
		f.lastDegradeAlert[i] = now
		newly = append(newly, f.channels[i].name)
	}
	if len(newly) == 0 {
		return
	}
	body := fmt.Sprintf("短信已通过其他渠道送达，但以下渠道多次发送失败、可能漏收：%s。请检查该渠道配置或网络。",
		strings.Join(newly, ", "))
	f.notifyVia(up, "CPE 通知渠道异常", body)
}

// notifyVia 经指定渠道直接发送一条提示，不重试、不再触发降级（防递归）。调用方须持有 sendMu。
func (f *Forwarder) notifyVia(idxs []int, title, body string) {
	for _, i := range idxs {
		if err := f.channels[i].sender.Send(title, body); err != nil {
			log.Printf("%s 发送渠道状态提示失败: %v", f.channels[i].name, err)
		}
	}
}

// allChannelIdx 返回全部渠道下标。
func (f *Forwarder) allChannelIdx() []int {
	idxs := make([]int, len(f.channels))
	for i := range f.channels {
		idxs[i] = i
	}
	return idxs
}

// complementIdx 返回不在 exclude 中的渠道下标（即本次最终送达的渠道）。
func (f *Forwarder) complementIdx(exclude []int) []int {
	ex := make(map[int]bool, len(exclude))
	for _, i := range exclude {
		ex[i] = true
	}
	var out []int
	for i := range f.channels {
		if !ex[i] {
			out = append(out, i)
		}
	}
	return out
}
