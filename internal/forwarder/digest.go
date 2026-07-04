// 新短信扫描与洪峰合并：单次持锁批量筛出未推送短信；积压超过阈值时按批合并为
// 摘要推送，避免故障恢复补发或短信风暴时手机连续轰炸几十条通知。

package forwarder

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/cpe"
)

const (
	// digestThreshold 单轮新短信超过该条数时改为合并摘要推送；不超过则维持逐条推送。
	digestThreshold = 5
	// digestBatchMaxCount 每条摘要最多合并的短信条数。
	digestBatchMaxCount = 10
	// digestBatchMaxBytes 每条摘要正文的字节预算：APNs 单条通知负载上限 4KB，
	// 预留标题与推送服务自身的 JSON 包装开销。
	digestBatchMaxBytes = 3000
)

// pendingSMS 是一条已确认未推送过的接收短信及其去重指纹。
type pendingSMS struct {
	key     string
	phone   string
	content string
	time    string
}

// scanNew 从本轮拉取结果中筛出未推送过的接收短信。单次持锁完成全部查重
// （此前每条短信加解锁两次），并对同轮内指纹重复的消息就地去重。
func (f *Forwarder) scanNew(sessions []cpe.SMSSession) []pendingSMS {
	var pending []pendingSMS
	batchSeen := make(map[string]bool)

	f.seenMu.Lock()
	defer f.seenMu.Unlock()
	for _, session := range sessions {
		for _, msg := range session.Messages {
			if msg.RcvOrSend != "recv" {
				continue
			}
			key := smsKey(session.Phone, msg.Content, msg.Time)
			if f.seen[key] || batchSeen[key] {
				continue
			}
			batchSeen[key] = true
			pending = append(pending, pendingSMS{
				key:     key,
				phone:   session.Phone,
				content: msg.Content,
				time:    msg.Time,
			})
		}
	}
	return pending
}

// flushPending 推送扫描出的新短信并标记已读。不超过 digestThreshold 时逐条推送（沿用模板）；
// 超过则按时间排序后合并为摘要分批推送。返回成功送达的条数与是否因通道全灭而提前止损
// （未送达的不标记已读，下轮自动重试）。
func (f *Forwarder) flushPending(pending []pendingSMS) (pushed int, exhausted bool) {
	if len(pending) <= digestThreshold {
		return f.flushEach(pending)
	}
	return f.flushDigest(pending)
}

// flushEach 逐条推送，行为与合并功能引入前一致。
func (f *Forwarder) flushEach(pending []pendingSMS) (pushed int, exhausted bool) {
	for _, p := range pending {
		log.Printf("新短信: %s [%s]", maskPhone(p.phone), p.time)
		if err := f.pushSMS(p.phone, p.content, p.time); err != nil {
			log.Printf("短信推送失败: %v", err)
			// 通道全部不可用时立即止损：跳过本轮剩余短信，避免积压条数 × 渠道超时拖长单轮。
			if !f.canDeliverSMS() {
				return pushed, true
			}
			continue
		}
		f.markPushed(p)
		pushed++
		log.Printf("已推送: %s", maskPhone(p.phone))
	}
	return pushed, false
}

// flushDigest 按时间升序合并为摘要分批推送；每批送达后才整批标记已读，
// 失败的批次留待下轮（下轮若仍超阈值会重新分批）。
func (f *Forwarder) flushDigest(pending []pendingSMS) (pushed int, exhausted bool) {
	log.Printf("发现 %d 条新短信（超过 %d 条），合并为摘要推送", len(pending), digestThreshold)
	// 时间格式 "2026/06/28 11:19:21" 零填充，字典序即时间序；同秒消息保持扫描顺序。
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].time < pending[j].time })

	batches := splitDigestBatches(pending)
	for bi, batch := range batches {
		title := fmt.Sprintf("CPE短信 汇总（%d 条）", len(batch))
		if len(batches) > 1 {
			title = fmt.Sprintf("CPE短信 汇总 %d/%d（%d 条）", bi+1, len(batches), len(batch))
		}
		if err := f.deliver(title, digestBody(batch), true); err != nil {
			log.Printf("摘要推送失败（%d 条）: %v", len(batch), err)
			if !f.canDeliverSMS() {
				return pushed, true
			}
			continue
		}
		for _, p := range batch {
			f.markPushed(p)
		}
		pushed += len(batch)
		log.Printf("已推送摘要 %d/%d（%d 条）", bi+1, len(batches), len(batch))
	}
	return pushed, false
}

// markPushed 将一条已送达短信计入去重集合。
func (f *Forwarder) markPushed(p pendingSMS) {
	f.seenMu.Lock()
	f.markSeenLocked(p.key)
	f.seenMu.Unlock()
}

// splitDigestBatches 按条数与字节预算贪心分批。单条超预算的短信独占一批，保证不死循环。
func splitDigestBatches(pending []pendingSMS) [][]pendingSMS {
	var batches [][]pendingSMS
	var batch []pendingSMS
	batchBytes := 0
	for _, p := range pending {
		entryBytes := len(digestEntry(p))
		if len(batch) > 0 && (len(batch) >= digestBatchMaxCount || batchBytes+entryBytes > digestBatchMaxBytes) {
			batches = append(batches, batch)
			batch, batchBytes = nil, 0
		}
		batch = append(batch, p)
		batchBytes += entryBytes
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

// digestEntry 渲染摘要中的单条短信。
func digestEntry(p pendingSMS) string {
	return fmt.Sprintf("【%s】%s\n%s", p.phone, p.time, p.content)
}

// digestBody 渲染一批短信的摘要正文，条目间以空行分隔。
func digestBody(batch []pendingSMS) string {
	var b strings.Builder
	for i, p := range batch {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(digestEntry(p))
	}
	return b.String()
}
