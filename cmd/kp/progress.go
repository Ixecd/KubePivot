package main

import (
	"fmt"
	"time"
)

// step 记录一个步骤的开始时间
type step struct {
	startAt time.Time
}

// Progress 统一进度输出，带时间戳和耗时
type Progress struct {
	current *step
}

// P 全局进度输出实例
var P = &Progress{}

// Start 开始一个步骤，打印时间戳 + icon + 消息
func (p *Progress) Start(icon, msg string) {
	p.current = &step{startAt: time.Now()}
	fmt.Printf("[%s] %s %s\n", ts(), icon, msg)
}

// Done 完成当前步骤，打印耗时
func (p *Progress) Done(msg string) {
	if p.current == nil {
		return
	}
	elapsed := time.Since(p.current.startAt)
	fmt.Printf("[%s] ✓  %s（%.1fs）\n", ts(), msg, elapsed.Seconds())
	p.current = nil
}

// Fail 当前步骤失败，打印耗时
func (p *Progress) Fail(msg string) {
	if p.current == nil {
		return
	}
	elapsed := time.Since(p.current.startAt)
	fmt.Printf("[%s] ✗  %s（%.1fs）\n", ts(), msg, elapsed.Seconds())
	p.current = nil
}

// Info 打印一条普通信息，不计时
func (p *Progress) Info(icon, msg string) {
	fmt.Printf("[%s] %s %s\n", ts(), icon, msg)
}

func ts() string {
	return time.Now().Format("15:04:05")
}
