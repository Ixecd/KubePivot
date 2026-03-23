package logger

import (
	"log/slog"
	"os"
)

// Init 初始化全局 slog。
//
// CLI 工具与服务端的区别：
//   - 默认 Text 格式（终端可读），而不是 JSON
//   - 始终写 stderr，不干扰 stdout 的用户输出
//   - LOG_LEVEL=debug 开启内部运行轨迹
//   - LOG_FORMAT=json  切换为 JSON（接入日志收集时用）
func Init() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if os.Getenv("LOG_FORMAT") == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	slog.SetDefault(slog.New(handler))
}
