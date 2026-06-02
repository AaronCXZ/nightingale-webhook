package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"webhook/internal/api"
	"webhook/internal/config"
	"webhook/internal/notifier"

	"gopkg.in/natefinch/lumberjack.v2"
	"webhook/internal/store"
	"webhook/internal/webhook"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	dryRunFlag := flag.Bool("dry-run", false, "全局 dry run 模式（覆盖配置文件）")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 命令行 dry-run 覆盖配置
	if *dryRunFlag {
		cfg.DryRun = true
	}

	// 初始化日志
	setupLogging(cfg.Logging)

	slog.Info("starting webhook service",
		"port", cfg.Server.Port,
		"dry_run", cfg.DryRun,
		"db_path", cfg.Storage.DBPath,
	)

	// 初始化 SQLite store
	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		slog.Error("初始化数据库失败", "error", err)
		os.Exit(1)
	}
	defer s.Close()

	// 惰性创建 caller（dry_run 模式下用 mock，否则用真实阿里云 client）
	var caller notifier.Caller
	if cfg.DryRun {
		slog.Info("dry run mode: using mock caller")
		caller = &notifier.MockCaller{}
	} else {
		caller, err = notifier.NewAliyunCaller(
			cfg.Aliyun.AccessKeyID,
			cfg.Aliyun.AccessKeySecret,
			cfg.Aliyun.CalledShowNumber,
			cfg.Aliyun.TTSCode,
			cfg.Aliyun.PlayTimes,
			cfg.Retry.MaxAttempts,
			cfg.Retry.BackoffBase,
		)
		if err != nil {
			slog.Error("创建阿里云 client 失败（检查密钥配置）", "error", err)
			os.Exit(1)
		}
	}

	// 创建 webhook handler
	handler := webhook.New(s, caller, cfg)

	// 创建 HTTP server
	server := api.NewServer(cfg, s, handler)

	// 启动后台 poller（非 dry_run 模式）
	if !cfg.DryRun {
		poller := notifier.NewPoller(s, caller)
		poller.Start(context.Background())
	}

	// 启动后台清理 goroutine
	go startCleanup(s, cfg.Storage.CallRetention)

	// 监听信号
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 在 goroutine 中启动 HTTP 服务
	go func() {
		if err := server.Start(); err != nil {
			slog.Error("HTTP server 错误", "error", err)
			stop()
		}
	}()

	// 等待信号
	<-ctx.Done()

	// 优雅关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("关闭 HTTP server 失败", "error", err)
	}

	slog.Info("webhook service stopped")
}

func setupLogging(cfg config.LoggingConfig) {
	level := slog.LevelInfo
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	// 双输出：stdout + 日志文件（轮转）
	var writer io.Writer
	if cfg.Output != "" {
		lj := &lumberjack.Logger{
			Filename:   cfg.Output,
			MaxSize:    cfg.MaxSize,
			MaxAge:     cfg.MaxAge,
			MaxBackups: cfg.MaxBackups,
			Compress:   false,
			LocalTime:  true,
		}
		writer = io.MultiWriter(os.Stdout, lj)
	} else {
		writer = os.Stdout
	}

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(writer, opts)
	} else {
		handler = slog.NewJSONHandler(writer, opts)
	}

	slog.SetDefault(slog.New(handler))
}

func startCleanup(s store.Store, retention time.Duration) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		if err := s.CleanupOldRecords(retention); err != nil {
			slog.Error("清理过期记录失败", "error", err)
		} else {
			slog.Debug("清理过期记录完成")
		}
	}
}
