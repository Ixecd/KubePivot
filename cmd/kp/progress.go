package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

// ANSI 颜色码
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
)

func isTTY() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := os.Getenv("TERM")
	return term != "" && term != "dumb"
}

func colorize(color, s string) string {
	if !isTTY() {
		return s
	}
	return color + s + colorReset
}

type step struct {
	startAt time.Time
	msg     string
}

// Progress 统一进度输出，带时间戳和耗时
// 同时写 slog（LOG_FORMAT=json 时输出结构化日志）
type Progress struct {
	current *step
}

var P = &Progress{}

func (p *Progress) Start(icon, msg string) {
	p.current = &step{startAt: time.Now(), msg: msg}
	fmt.Printf("%s %s %s\n",
		colorize(colorGray, "["+ts()+"]"),
		icon,
		msg,
	)
	slog.Debug("deploy.start", "msg", msg)
}

func (p *Progress) Done(msg string) {
	if p.current == nil {
		return
	}
	elapsed := time.Since(p.current.startAt)
	fmt.Printf("%s %s %s\n",
		colorize(colorGray, "["+ts()+"]"),
		colorize(colorGreen, "✓ "),
		fmt.Sprintf("%s（%.1fs）", msg, elapsed.Seconds()),
	)
	slog.Info("deploy.done",
		"msg", msg,
		"elapsed_ms", elapsed.Milliseconds(),
	)
	p.current = nil
}

func (p *Progress) Fail(msg string) {
	if p.current == nil {
		p.current = &step{startAt: time.Now()}
	}
	elapsed := time.Since(p.current.startAt)
	fmt.Printf("%s %s %s\n",
		colorize(colorGray, "["+ts()+"]"),
		colorize(colorRed, "✗ "),
		fmt.Sprintf("%s（%.1fs）", msg, elapsed.Seconds()),
	)
	slog.Error("deploy.fail",
		"msg", msg,
		"elapsed_ms", elapsed.Milliseconds(),
	)
	p.current = nil
}

func (p *Progress) Info(icon, msg string) {
	fmt.Printf("%s %s %s\n",
		colorize(colorGray, "["+ts()+"]"),
		icon,
		msg,
	)
	slog.Info("deploy.info", "msg", msg)
}

func ts() string {
	return time.Now().Format("15:04:05")
}
