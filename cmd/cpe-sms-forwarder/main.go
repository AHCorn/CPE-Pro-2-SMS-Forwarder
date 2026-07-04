package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kardianos/service"

	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/config"
	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/forwarder"
	"github.com/AHCorn/CPE-Pro-2-SMS-Forwarder/internal/logging"
)

// version 由构建期 -ldflags "-X main.version=..." 注入（见 Makefile / release.yml，取自 git tag）；
// 源码直接 go run 时为 dev。勿手改此默认值。
var version = "dev"

// program 适配 kardianos/service 生命周期。Start 必须快速返回，故主循环放在 goroutine；
// Stop 取消 context 并等待循环退出，保证服务管理器能干净地停止进程。
type program struct {
	configPath string
	logger     service.Logger
	cancel     context.CancelFunc
	done       chan struct{}
}

func (p *program) Start(s service.Service) error {
	cfg, err := config.Load(p.configPath)
	if err != nil {
		return fmt.Errorf("加载配置 %s: %w", p.configPath, err)
	}
	setupLogging(cfg)
	logStartup(cfg)

	fwd, err := forwarder.New(cfg)
	if err != nil {
		return fmt.Errorf("初始化转发器: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		fwd.Run(ctx)
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(15 * time.Second):
		}
	}
	return nil
}

func main() {
	svcAction := flag.String("service", "", "服务控制命令: install | uninstall | start | stop | restart | status")
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cpe-sms-forwarder %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return
	}

	// 安装后服务以系统工作目录运行，相对路径会失效，故固化为绝对路径。
	absConfig := *configPath
	if abs, err := filepath.Abs(*configPath); err == nil {
		absConfig = abs
	}

	prg := &program{configPath: absConfig}
	svcConfig := &service.Config{
		Name:        "cpe-sms-forwarder",
		DisplayName: "CPE Pro 2 SMS Forwarder",
		Description: "烽火 CPE Pro 2 短信转发服务",
		Arguments:   []string{"-config", absConfig},
		Option: service.KeyValue{
			"RunAtLoad":              true,      // launchd：加载/开机即启动
			"OnFailure":              "restart", // Windows：异常退出后自动重启
			"OnFailureDelayDuration": "10s",
			"OnFailureResetPeriod":   10,
		},
	}

	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}
	prg.logger, _ = s.Logger(nil)

	if *svcAction != "" {
		if err := control(s, *svcAction); err != nil {
			log.Fatalf("服务命令 %q 失败: %v", *svcAction, err)
		}
		return
	}

	if err := s.Run(); err != nil {
		if prg.logger != nil {
			_ = prg.logger.Error(err)
		}
		log.Fatalf("运行失败: %v", err)
	}
}

// control 执行服务控制命令。status 不在 kardianos 的 Control 动作集中，单独处理。
func control(s service.Service, action string) error {
	if action == "status" {
		st, err := s.Status()
		if errors.Is(err, service.ErrNotInstalled) {
			fmt.Println("状态: 未安装")
			return nil
		}
		if err != nil {
			return err
		}
		switch st {
		case service.StatusRunning:
			fmt.Println("状态: 运行中")
		case service.StatusStopped:
			fmt.Println("状态: 已停止")
		default:
			fmt.Println("状态: 未知（可能尚未安装）")
		}
		return nil
	}
	if err := service.Control(s, action); err != nil {
		return fmt.Errorf("%w（可用命令: install, uninstall, start, stop, restart, status）", err)
	}
	fmt.Printf("已执行服务命令: %s\n", action)
	return nil
}

// maxLogBytes 单个日志文件大小上限，超过即轮转，避免长跑服务日志无限增长。
const maxLogBytes = 5 << 20 // 5 MiB

// setupLogging 决定日志输出位置。类 Unix 服务的标准输出由 journald/launchd/logd 捕获，无需另写文件；
// Windows 服务没有控制台，故在未显式配置且非交互运行时，落地到首个可写候选目录（轮转写入）。
func setupLogging(cfg *config.Config) {
	if cfg.Log.File != "" {
		w, err := logging.NewRotatingWriter(cfg.Log.File, maxLogBytes)
		if err != nil {
			log.Printf("打开日志文件 %s 失败，沿用标准错误输出: %v", cfg.Log.File, err)
			return
		}
		setLogOutput(w)
		return
	}
	// Windows 服务无控制台，未显式配置时依次尝试可写位置，避免日志静默丢失。
	if !service.Interactive() && runtime.GOOS == "windows" {
		for _, p := range windowsLogCandidates() {
			if w, err := logging.NewRotatingWriter(p, maxLogBytes); err == nil {
				log.SetOutput(w)
				log.Printf("日志输出到 %s", p)
				return
			}
		}
	}
}

// setLogOutput 交互运行时同时写标准错误与文件，服务运行时只写文件。
func setLogOutput(w io.Writer) {
	if service.Interactive() {
		log.SetOutput(io.MultiWriter(os.Stderr, w))
	} else {
		log.SetOutput(w)
	}
}

// windowsLogCandidates 返回 Windows 服务日志的候选路径，按优先级排列：
// 可执行文件同目录（如 Program Files 不可写则退至）ProgramData 子目录，最后退到临时目录。
func windowsLogCandidates() []string {
	const name = "cpe-sms-forwarder.log"
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), name))
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		paths = append(paths, filepath.Join(pd, "cpe-sms-forwarder", name))
	}
	paths = append(paths, filepath.Join(os.TempDir(), name))
	return paths
}

func logStartup(cfg *config.Config) {
	log.Printf("CPE SMS Forwarder %s 启动", version)
	log.Printf("  CPE: %s", cfg.CPE.Host)
	log.Printf("  轮询间隔: %d 秒", cfg.Poll.IntervalSeconds)
	log.Printf("  通知渠道: %s", notifySchemes(cfg.Notify.URLs))
}

// notifySchemes 仅提取各通知 URL 的 scheme（如 bark、telegram），避免在日志中泄露 token / key。
func notifySchemes(urls []string) string {
	if len(urls) == 0 {
		return "(无)"
	}
	schemes := make([]string, 0, len(urls))
	for _, u := range urls {
		if i := strings.Index(u, "://"); i > 0 {
			schemes = append(schemes, u[:i])
		} else {
			schemes = append(schemes, "?")
		}
	}
	return fmt.Sprintf("%d 个 (%s)", len(urls), strings.Join(schemes, ", "))
}
