package forwarder

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeChannel 是 channelSender 的测试替身：前 failTimes 次 Send 返回错误，其后成功；
// 成功送达的 body 记入 delivered，供断言降级提示是否经该渠道发出。
type fakeChannel struct {
	failTimes int
	calls     int
	delivered []string
}

func (c *fakeChannel) Send(_ /*title*/, body string) error {
	c.calls++
	if c.calls <= c.failTimes {
		return errors.New("send failed")
	}
	c.delivered = append(c.delivered, body)
	return nil
}

// gotDegrade 判断该渠道收到的消息里是否含降级提示。
func (c *fakeChannel) gotDegrade() bool {
	for _, b := range c.delivered {
		if strings.Contains(b, "多次发送失败") {
			return true
		}
	}
	return false
}

// newReliableForwarder 构造仅用于投递决策测试的 Forwarder：注入 fake 渠道，重试退避调到极小以加速。
func newReliableForwarder(retry bool, chs ...*fakeChannel) *Forwarder {
	channels := make([]*notifyChannel, len(chs))
	for i, c := range chs {
		channels[i] = &notifyChannel{name: fmt.Sprintf("渠道%d", i+1), sender: c}
	}
	return &Forwarder{
		channels:         channels,
		lastDegradeAlert: make(map[int]time.Time),
		cycleTripped:     make(map[int]bool),
		retryFailed:      retry,
		retryBackoff:     time.Millisecond,
	}
}

// withFallback 为测试 Forwarder 挂上备用渠道。
func withFallback(f *Forwarder, c *fakeChannel, forward bool) *Forwarder {
	f.fallback = &notifyChannel{name: "备用渠道", sender: c}
	f.fallbackForward = forward
	return f
}

// permanentFail 是“永久失败”的发送次数阈值，远大于任何用例的发送次数。
const permanentFail = 1 << 30

// TestSendStrictAllSucceed 默认模式（未开启可靠投递）全成功返回 nil，各渠道各发一次。
func TestSendStrictAllSucceed(t *testing.T) {
	a, b := &fakeChannel{}, &fakeChannel{}
	f := newReliableForwarder(false, a, b)
	if err := f.deliver("t", "body", false); err != nil {
		t.Fatalf("全成功应返回 nil，实际 %v", err)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("每个渠道应各发 1 次，实际 a=%d b=%d", a.calls, b.calls)
	}
}

// TestSendStrictAnyFailErrors 默认模式任一渠道失败即返回错误，且不重试。
func TestSendStrictAnyFailErrors(t *testing.T) {
	a := &fakeChannel{}
	bad := &fakeChannel{failTimes: permanentFail}
	f := newReliableForwarder(false, a, bad)
	if err := f.deliver("t", "body", false); err == nil {
		t.Fatal("任一渠道失败应返回错误")
	}
	if a.calls != 1 || bad.calls != 1 {
		t.Errorf("默认模式不重试，应各发 1 次，实际 a=%d bad=%d", a.calls, bad.calls)
	}
}

// TestReliableAllFailNoRetry 可靠投递下全渠道失败：直接返回错误、不重试、不降级（无可用渠道）。
func TestReliableAllFailNoRetry(t *testing.T) {
	a := &fakeChannel{failTimes: permanentFail}
	b := &fakeChannel{failTimes: permanentFail}
	f := newReliableForwarder(true, a, b)
	if err := f.deliver("t", "body", true); err == nil {
		t.Fatal("全渠道失败应返回错误")
	}
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("全失败不应重试，各发 1 次，实际 a=%d b=%d", a.calls, b.calls)
	}
}

// TestReliablePartialRetryRecovers 部分成功时重试失败渠道；失败渠道在重试内恢复则无降级提示。
func TestReliablePartialRetryRecovers(t *testing.T) {
	ok := &fakeChannel{}
	flaky := &fakeChannel{failTimes: 2} // 首发 + 第 1 次重试失败，第 2 次重试成功
	f := newReliableForwarder(true, ok, flaky)
	if err := f.deliver("t", "msg", true); err != nil {
		t.Fatalf("部分成功应返回 nil，实际 %v", err)
	}
	if flaky.calls != 3 {
		t.Errorf("flaky 应被发 3 次（1 首发 + 2 重试），实际 %d", flaky.calls)
	}
	if ok.gotDegrade() {
		t.Error("重试内恢复不应发降级提示")
	}
}

// TestReliablePartialDegradeAlert 部分成功且重试仍失败：经送达渠道发降级提示，整体仍返回 nil。
func TestReliablePartialDegradeAlert(t *testing.T) {
	ok := &fakeChannel{}
	bad := &fakeChannel{failTimes: permanentFail}
	f := newReliableForwarder(true, ok, bad)
	if err := f.deliver("t", "msg", true); err != nil {
		t.Fatalf("有渠道送达应返回 nil，实际 %v", err)
	}
	if bad.calls != 1+channelRetryMax {
		t.Errorf("bad 应被发 %d 次（1 首发 + %d 重试），实际 %d", 1+channelRetryMax, channelRetryMax, bad.calls)
	}
	if !ok.gotDegrade() {
		t.Error("重试仍失败应经送达渠道发降级提示")
	}
	if len(ok.delivered) != 2 { // 短信 + 降级提示
		t.Errorf("送达渠道应收到短信+降级提示共 2 条，实际 %d: %v", len(ok.delivered), ok.delivered)
	}
}

// TestDegradeAlertThrottled 同一渠道持续失败时，降级提示在节流窗口内不重复，避免刷屏。
func TestDegradeAlertThrottled(t *testing.T) {
	ok := &fakeChannel{}
	bad := &fakeChannel{failTimes: permanentFail}
	f := newReliableForwarder(true, ok, bad)
	for i := 0; i < 3; i++ {
		if err := f.deliver("t", "msg", true); err != nil {
			t.Fatalf("第 %d 次应返回 nil，实际 %v", i, err)
		}
	}
	degrade := 0
	for _, b := range ok.delivered {
		if strings.Contains(b, "多次发送失败") {
			degrade++
		}
	}
	if degrade != 1 {
		t.Errorf("节流窗口内降级提示应只发 1 次，实际 %d", degrade)
	}
}

// TestDegradeAlertSuppressedForSystemNotify 系统通知（allowAlert=false）部分失败不触发降级提示。
func TestDegradeAlertSuppressedForSystemNotify(t *testing.T) {
	ok := &fakeChannel{}
	bad := &fakeChannel{failTimes: permanentFail}
	f := newReliableForwarder(true, ok, bad)
	if err := f.deliver("t", "msg", false); err != nil {
		t.Fatalf("有渠道送达应返回 nil，实际 %v", err)
	}
	if ok.gotDegrade() {
		t.Error("系统通知不应触发降级提示")
	}
	if len(ok.delivered) != 1 {
		t.Errorf("系统通知应只发 1 条，实际 %d", len(ok.delivered))
	}
}

// TestCycleTripSkipsFailedChannel 同一轮内失败渠道被熔断：后续消息不再对其发起请求，
// 新一轮（beginPollCycle）后恢复探测。
func TestCycleTripSkipsFailedChannel(t *testing.T) {
	bad := &fakeChannel{failTimes: permanentFail}
	f := newReliableForwarder(false, bad)

	_ = f.deliver("t", "m1", false)
	_ = f.deliver("t", "m2", false)
	_ = f.deliver("t", "m3", false)
	if bad.calls != 1 {
		t.Errorf("熔断后同轮不应再尝试，期望共 1 次请求，实际 %d", bad.calls)
	}

	f.beginPollCycle()
	_ = f.deliver("t", "m4", false)
	if bad.calls != 2 {
		t.Errorf("新一轮应恢复探测，期望共 2 次请求，实际 %d", bad.calls)
	}
}

// TestCycleTripSkipsInReliableMode 可靠投递下已熔断渠道不参与部分成功的重试，
// 避免每条积压短信都对死渠道做退避重试拖长轮次。
func TestCycleTripSkipsInReliableMode(t *testing.T) {
	ok := &fakeChannel{}
	bad := &fakeChannel{failTimes: permanentFail}
	f := newReliableForwarder(true, ok, bad)

	_ = f.deliver("t", "m1", true) // 首发+2 重试=3 次后熔断
	callsAfterFirst := bad.calls
	if callsAfterFirst != 1+channelRetryMax {
		t.Fatalf("首条消息应尝试 %d 次，实际 %d", 1+channelRetryMax, callsAfterFirst)
	}
	_ = f.deliver("t", "m2", true)
	_ = f.deliver("t", "m3", true)
	if bad.calls != callsAfterFirst {
		t.Errorf("熔断后后续消息不应再尝试该渠道，期望 %d 次，实际 %d", callsAfterFirst, bad.calls)
	}
	if len(ok.delivered) < 3 {
		t.Errorf("正常渠道应持续送达，实际 %d 条", len(ok.delivered))
	}
}

// TestCanDeliverSMS 校验“本轮是否还有可送达短信的通道”判定：
// 主渠道全熔断后，仅转发模式且未熔断的备用渠道能维持可送达状态。
func TestCanDeliverSMS(t *testing.T) {
	bad := &fakeChannel{failTimes: permanentFail}

	f := newReliableForwarder(false, bad)
	if !f.canDeliverSMS() {
		t.Fatal("未熔断时应可送达")
	}
	_ = f.deliver("t", "m", false)
	if f.canDeliverSMS() {
		t.Error("唯一主渠道熔断且无备用渠道时应不可送达")
	}

	// 告警模式备用渠道不转发短信本体，不改变不可送达判定。
	f2 := withFallback(newReliableForwarder(false, &fakeChannel{failTimes: permanentFail}), &fakeChannel{}, false)
	_ = f2.deliver("t", "m", false)
	if f2.canDeliverSMS() {
		t.Error("备用渠道为告警模式时不应视为可送达短信")
	}

	// 转发模式备用渠道可承接短信。
	f3 := withFallback(newReliableForwarder(false, &fakeChannel{failTimes: permanentFail}), &fakeChannel{}, true)
	_ = f3.deliver("t", "m", false)
	if !f3.canDeliverSMS() {
		t.Error("转发模式备用渠道存活时应仍可送达")
	}

	// 转发模式备用渠道自身也熔断后，不可送达。
	f4 := withFallback(newReliableForwarder(false, &fakeChannel{failTimes: permanentFail}), &fakeChannel{failTimes: permanentFail}, true)
	_ = f4.deliver("t", "m", false)
	if f4.canDeliverSMS() {
		t.Error("备用渠道也熔断后应不可送达")
	}
}

// TestFallbackForwardDelivers 转发模式：主渠道全失败时消息本体经备用渠道送达，整体视为成功。
func TestFallbackForwardDelivers(t *testing.T) {
	bad := &fakeChannel{failTimes: permanentFail}
	fb := &fakeChannel{}
	f := withFallback(newReliableForwarder(false, bad), fb, true)

	if err := f.deliver("标题", "本体", true); err != nil {
		t.Fatalf("备用渠道送达应返回 nil，实际 %v", err)
	}
	if len(fb.delivered) != 1 || fb.delivered[0] != "本体" {
		t.Errorf("备用渠道应收到原消息本体，实际 %v", fb.delivered)
	}
}

// TestFallbackForwardFailure 转发模式下备用渠道也失败：返回错误（短信留待下轮），
// 且备用渠道同轮熔断、不再反复尝试。
func TestFallbackForwardFailure(t *testing.T) {
	bad := &fakeChannel{failTimes: permanentFail}
	fb := &fakeChannel{failTimes: permanentFail}
	f := withFallback(newReliableForwarder(false, bad), fb, true)

	if err := f.deliver("t", "m1", true); err == nil {
		t.Fatal("主备全失败应返回错误")
	}
	if err := f.deliver("t", "m2", true); err == nil {
		t.Fatal("主备全失败应返回错误")
	}
	if fb.calls != 1 {
		t.Errorf("备用渠道熔断后同轮不应再尝试，期望 1 次，实际 %d", fb.calls)
	}
}

// TestFallbackAlertMode 告警模式：主渠道全失败时备用渠道只收到一条限流的“主渠道离线”提示，
// 原消息仍按失败处理（保证短信不标记已读、主渠道恢复后补发）。
func TestFallbackAlertMode(t *testing.T) {
	bad := &fakeChannel{failTimes: permanentFail}
	fb := &fakeChannel{}
	f := withFallback(newReliableForwarder(false, bad), fb, false)

	if err := f.deliver("标题A", "本体", true); err == nil {
		t.Fatal("告警模式下原消息应仍返回失败")
	}
	f.beginPollCycle()
	if err := f.deliver("标题B", "本体", true); err == nil {
		t.Fatal("告警模式下原消息应仍返回失败")
	}

	if len(fb.delivered) != 1 {
		t.Fatalf("节流窗口内离线提示应只发 1 条，实际 %d: %v", len(fb.delivered), fb.delivered)
	}
	if !strings.Contains(fb.delivered[0], "主通知渠道") || !strings.Contains(fb.delivered[0], "标题A") {
		t.Errorf("离线提示应说明主渠道故障并附未送达消息标题，实际 %q", fb.delivered[0])
	}
	if strings.Contains(fb.delivered[0], "本体") {
		t.Errorf("告警模式不应转发消息本体，实际 %q", fb.delivered[0])
	}
}

// TestFallbackNotUsedOnPartialFailure 主渠道部分成功时不触发备用渠道（那是降级提示的职责）。
func TestFallbackNotUsedOnPartialFailure(t *testing.T) {
	ok := &fakeChannel{}
	bad := &fakeChannel{failTimes: permanentFail}
	fb := &fakeChannel{}
	f := withFallback(newReliableForwarder(true, ok, bad), fb, true)

	if err := f.deliver("t", "m", true); err != nil {
		t.Fatalf("部分成功应返回 nil，实际 %v", err)
	}
	if fb.calls != 0 {
		t.Errorf("部分成功不应动用备用渠道，实际被调用 %d 次", fb.calls)
	}
}
