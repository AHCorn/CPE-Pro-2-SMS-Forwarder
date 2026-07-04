package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotatingWriterRotates 验证超过上限后轮转为 .1 备份并从空文件续写，
// 且磁盘占用受上限约束（不会无限增长）。
func TestRotatingWriterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	w, err := NewRotatingWriter(path, 100)
	if err != nil {
		t.Fatalf("创建轮转器失败: %v", err)
	}
	defer w.Close()

	line := strings.Repeat("x", 40) + "\n" // 41 字节
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("期望生成备份文件 %s.1，实际: %v", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取主日志失败: %v", err)
	}
	if fi.Size() > 100 {
		t.Fatalf("主日志大小 %d 应不超过上限 100", fi.Size())
	}
}

// TestRotatingWriterAppends 验证重开同一路径时按 append 续写、保留已有内容。
func TestRotatingWriterAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	w1, err := NewRotatingWriter(path, 1<<20)
	if err != nil {
		t.Fatalf("创建轮转器失败: %v", err)
	}
	if _, err := w1.Write([]byte("first\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	w1.Close()

	w2, err := NewRotatingWriter(path, 1<<20)
	if err != nil {
		t.Fatalf("重开轮转器失败: %v", err)
	}
	defer w2.Close()
	if _, err := w2.Write([]byte("second\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if got := string(data); got != "first\nsecond\n" {
		t.Fatalf("期望 append 续写保留旧内容，实际: %q", got)
	}
}
