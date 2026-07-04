// Package logging 提供带大小上限的日志写入器，避免长跑服务的日志文件无限增长。
package logging

import (
	"os"
	"path/filepath"
	"sync"
)

// defaultMaxBytes 单个日志文件的默认大小上限。
const defaultMaxBytes = 5 << 20 // 5 MiB

// RotatingWriter 是按大小轮转的日志写入器：当前文件超过 maxBytes 时，
// 轮转为 <path>.1（覆盖上一份备份）后从空文件继续写，磁盘占用上限约 2*maxBytes。
// Write 受互斥保护，可直接作为标准库 log 包的输出目标。
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	size     int64
	f        *os.File
}

// NewRotatingWriter 打开（必要时创建）日志文件并按 append 续写。
// maxBytes <= 0 时取默认上限。父目录不存在会尝试创建。
func NewRotatingWriter(path string, maxBytes int64) (*RotatingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- path 来自受信任的本地配置(log.file)/默认路径，由运维控制，非外部输入
	if err != nil {
		return nil, err
	}
	var size int64
	if fi, statErr := f.Stat(); statErr == nil {
		size = fi.Size()
	}
	return &RotatingWriter{path: path, maxBytes: maxBytes, size: size, f: f}, nil
}

// Write 写入一段日志，必要时先轮转。整段写入不跨文件切分，
// 故单条超过上限的日志仍会完整写入当前文件。
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// size > 0 保证空文件不会被反复轮转。
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		w.rotate()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate 关闭当前文件、改名为 .1 备份后重开空文件。任一步失败都尽力让 w.f 指向可写文件，
// 优先保证后续日志不丢，因此不向上返回错误。
func (w *RotatingWriter) rotate() {
	if err := w.f.Close(); err != nil {
		// 关闭失败时不强制改名，尝试继续用原句柄。
		if f, e := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); e == nil {
			w.f = f
		}
		return
	}
	_ = os.Remove(w.path + ".1")
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		// 改名失败则重开原文件续写（不轮转），避免丢日志。
		if f, e := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); e == nil {
			w.f = f
		}
		return
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		// 改名已成功但截断重开失败：退回 append（原文件已改走，append 会新建空文件），
		// 避免 w.f 停留在已关闭的旧句柄上，导致后续日志写入静默失败而丢失。
		if af, ae := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); ae == nil {
			w.f = af
			w.size = 0
		}
		return
	}
	w.f = f
	w.size = 0
}

// Close 关闭底层文件。
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
