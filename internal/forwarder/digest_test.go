package forwarder

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/cpe"
)

// makePending 构造 n 条时间递增的待推短信。
func makePending(n int) []pendingSMS {
	out := make([]pendingSMS, 0, n)
	for i := 0; i < n; i++ {
		t := fmt.Sprintf("2026/07/01 10:%02d:00", i)
		out = append(out, pendingSMS{
			key:     smsKey("10086", fmt.Sprintf("msg-%02d", i), t),
			phone:   "10086",
			content: fmt.Sprintf("msg-%02d", i),
			time:    t,
		})
	}
	return out
}

// newDigestForwarder 构造带 seen 集合的投递测试 Forwarder。
func newDigestForwarder(chs ...*fakeChannel) *Forwarder {
	f := newReliableForwarder(false, chs...)
	f.seen = make(map[string]bool)
	return f
}

// TestScanNewDedupAndFilter 验证扫描阶段：过滤已见指纹与发送方向、同轮内重复指纹只保留一条。
func TestScanNewDedupAndFilter(t *testing.T) {
	f := newDigestForwarder(&fakeChannel{})
	seenKey := smsKey("10086", "old", "2026/07/01 09:00:00")
	f.seen[seenKey] = true

	sessions := []cpe.SMSSession{{
		Phone: "10086",
		Messages: []cpe.SMSMsg{
			{RcvOrSend: "recv", Content: "old", Time: "2026/07/01 09:00:00"},  // 已推送过
			{RcvOrSend: "send", Content: "out", Time: "2026/07/01 09:01:00"},  // 本机发出
			{RcvOrSend: "recv", Content: "new", Time: "2026/07/01 09:02:00"},  // 新短信
			{RcvOrSend: "recv", Content: "new", Time: "2026/07/01 09:02:00"},  // 同轮重复
		},
	}}

	pending := f.scanNew(sessions)
	if len(pending) != 1 {
		t.Fatalf("期望筛出 1 条新短信，实际 %d: %v", len(pending), pending)
	}
	if pending[0].content != "new" {
		t.Errorf("筛出的短信内容不符: %q", pending[0].content)
	}
}

// TestFlushEachUnderThreshold 不超过阈值时逐条推送，每条独立成一次通知。
func TestFlushEachUnderThreshold(t *testing.T) {
	ch := &fakeChannel{}
	f := newDigestForwarder(ch)
	pending := makePending(digestThreshold)

	pushed, exhausted := f.flushPending(pending)
	if pushed != digestThreshold || exhausted {
		t.Fatalf("期望送达 %d 条且不止损，实际 pushed=%d exhausted=%v", digestThreshold, pushed, exhausted)
	}
	if len(ch.delivered) != digestThreshold {
		t.Errorf("阈值内应逐条推送 %d 次，实际 %d", digestThreshold, len(ch.delivered))
	}
	for _, p := range pending {
		if !f.seen[p.key] {
			t.Fatalf("送达短信应标记已读: %s", p.content)
		}
	}
}

// TestFlushDigestBatching 超过阈值时合并推送：按条数分批、整批标记已读、正文含各条内容。
func TestFlushDigestBatching(t *testing.T) {
	ch := &fakeChannel{}
	f := newDigestForwarder(ch)
	pending := makePending(digestBatchMaxCount + 2) // 12 条 → 10+2 两批

	pushed, exhausted := f.flushPending(pending)
	if pushed != len(pending) || exhausted {
		t.Fatalf("期望送达 %d 条且不止损，实际 pushed=%d exhausted=%v", len(pending), pushed, exhausted)
	}
	if len(ch.delivered) != 2 {
		t.Fatalf("12 条应合并为 2 批推送，实际 %d 次: %v", len(ch.delivered), ch.delivered)
	}
	if !strings.Contains(ch.delivered[0], "msg-00") || !strings.Contains(ch.delivered[0], "msg-09") {
		t.Errorf("第一批应含前 10 条，实际 %q", ch.delivered[0])
	}
	if !strings.Contains(ch.delivered[1], "msg-10") || !strings.Contains(ch.delivered[1], "msg-11") {
		t.Errorf("第二批应含后 2 条，实际 %q", ch.delivered[1])
	}
	if strings.Contains(ch.delivered[1], "msg-09") {
		t.Errorf("第二批不应包含第一批内容: %q", ch.delivered[1])
	}
	for _, p := range pending {
		if !f.seen[p.key] {
			t.Fatalf("送达短信应标记已读: %s", p.content)
		}
	}
}

// TestFlushDigestSortsByTime 摘要按时间升序排列，与拉取顺序无关。
func TestFlushDigestSortsByTime(t *testing.T) {
	ch := &fakeChannel{}
	f := newDigestForwarder(ch)
	pending := makePending(digestThreshold + 1)
	// 逆序打乱输入
	for i, j := 0, len(pending)-1; i < j; i, j = i+1, j-1 {
		pending[i], pending[j] = pending[j], pending[i]
	}

	if pushed, _ := f.flushPending(pending); pushed != len(pending) {
		t.Fatalf("应全部送达，实际 %d", pushed)
	}
	body := ch.delivered[0]
	if strings.Index(body, "msg-00") > strings.Index(body, "msg-05") {
		t.Errorf("摘要应按时间升序，实际 %q", body)
	}
}

// TestFlushDigestByteBudget 单批正文超出字节预算时提前分批；单条超预算的短信独占一批不死循环。
func TestFlushDigestByteBudget(t *testing.T) {
	big := strings.Repeat("长", digestBatchMaxBytes) // 单条即超预算
	pending := []pendingSMS{
		{key: "k1", phone: "10086", content: big, time: "2026/07/01 10:00:00"},
		{key: "k2", phone: "10086", content: big, time: "2026/07/01 10:01:00"},
		{key: "k3", phone: "10086", content: "small", time: "2026/07/01 10:02:00"},
	}
	batches := splitDigestBatches(pending)
	if len(batches) != 3 {
		t.Fatalf("两条超预算短信应各自独占一批，期望 3 批，实际 %d", len(batches))
	}
	for i, b := range batches[:2] {
		if len(b) != 1 {
			t.Errorf("第 %d 批应只含 1 条，实际 %d", i+1, len(b))
		}
	}
}

// TestFlushDigestFailureKeepsUnseen 摘要批次失败时整批不标记已读（留待下轮），
// 且通道全灭时提前止损。
func TestFlushDigestFailureKeepsUnseen(t *testing.T) {
	bad := &fakeChannel{failTimes: permanentFail}
	f := newDigestForwarder(bad)
	pending := makePending(digestBatchMaxCount + 2)

	pushed, exhausted := f.flushPending(pending)
	if pushed != 0 {
		t.Fatalf("全部失败不应有已送达，实际 %d", pushed)
	}
	if !exhausted {
		t.Fatal("唯一渠道熔断后应报告通道耗尽提前止损")
	}
	if len(f.seen) != 0 {
		t.Fatalf("失败批次不应标记已读，实际 %d 条", len(f.seen))
	}
}
