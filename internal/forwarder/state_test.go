package forwarder

import (
	"path/filepath"
	"testing"
	"time"
)

// TestStateRoundTrip 验证去重状态落盘后能完整加载，且加载后视为非首次启动（跳过基线化）。
func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seen.json")

	src := &Forwarder{
		seen:      map[string]bool{"a": true, "b": true, "c": true},
		seenOrder: []string{"a", "b", "c"},
		statePath: path,
	}
	src.saveState()

	dst := &Forwarder{seen: make(map[string]bool), statePath: path}
	dst.loadState()

	if !dst.initialized {
		t.Fatal("加载已存在的状态后应视为非首次启动 (initialized=true)")
	}
	for _, k := range []string{"a", "b", "c"} {
		if !dst.seen[k] {
			t.Fatalf("期望去重集合包含 %q，实际缺失", k)
		}
	}
	if len(dst.seen) != 3 {
		t.Fatalf("期望加载 3 条去重记录，实际 %d", len(dst.seen))
	}
	// seenOrder 应按落盘顺序还原，且与 seen 规模一致（FIFO 不变式）。
	if got := dst.seenOrder; len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("期望 seenOrder=[a b c]，实际 %v", got)
	}
}

// TestPruneSeenFIFO 验证去重集合超限时只淘汰最旧指纹、保留最近的，
// 防止整表清空导致下一轮把收件箱现存短信全部当新短信重推。
func TestPruneSeenFIFO(t *testing.T) {
	f := &Forwarder{seen: make(map[string]bool)}
	const total = 10050 // 超过 pruneSeen 内部上限 10000
	f.seenMu.Lock()
	for i := 0; i < total; i++ {
		f.markSeenLocked(smsKey("p", "c", time.Unix(int64(i), 0).String()))
	}
	f.seenMu.Unlock()

	oldest := smsKey("p", "c", time.Unix(0, 0).String())
	newest := smsKey("p", "c", time.Unix(total-1, 0).String())

	f.pruneSeen()

	if len(f.seen) != 10000 || len(f.seenOrder) != 10000 {
		t.Fatalf("淘汰后期望保留 10000 条，实际 seen=%d seenOrder=%d", len(f.seen), len(f.seenOrder))
	}
	if f.seen[oldest] {
		t.Fatal("最旧指纹应被淘汰")
	}
	if !f.seen[newest] {
		t.Fatal("最近指纹应被保留（否则会重复推送收件箱现存短信）")
	}
}

// TestLoadStateMissingFile 验证状态文件不存在时按首次启动处理（initialized=false，需基线化）。
func TestLoadStateMissingFile(t *testing.T) {
	dir := t.TempDir()
	f := &Forwarder{
		seen:      make(map[string]bool),
		statePath: filepath.Join(dir, "does-not-exist.json"),
	}
	f.loadState()

	if f.initialized {
		t.Fatal("状态文件不存在时应按首次启动处理 (initialized=false)")
	}
	if len(f.seen) != 0 {
		t.Fatalf("首次启动去重集合应为空，实际 %d", len(f.seen))
	}
}

// TestEmptyStateFileSkipsBaseline 验证状态文件存在但为空时仍视为非首次启动，
// 从而停机期间到达的短信不会被基线化吞掉。
func TestEmptyStateFileSkipsBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seen.json")

	empty := &Forwarder{seen: make(map[string]bool), statePath: path}
	empty.saveState() // 写出 {"keys":[]}

	loaded := &Forwarder{seen: make(map[string]bool), statePath: path}
	loaded.loadState()

	if !loaded.initialized {
		t.Fatal("状态文件存在（即便为空）也应跳过基线化 (initialized=true)")
	}
}

// TestStateDisabledWhenPathEmpty 验证未配置状态路径时持久化为无操作、不报错。
func TestStateDisabledWhenPathEmpty(t *testing.T) {
	f := &Forwarder{seen: map[string]bool{"x": true}, statePath: ""}
	f.saveState()
	f.loadState()
	if f.initialized {
		t.Fatal("未启用持久化时不应改变 initialized")
	}
}
