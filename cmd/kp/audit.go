package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/state"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// AuditEvent 统一审计事件格式（SOC2/ISO27001 友好）
type AuditEvent struct {
	Timestamp string `json:"timestamp"`
	Source    string `json:"source"`   // deploy / secret / drift
	Action    string `json:"action"`   // deploy.start / secret.rotate / drift.force-sync
	Actor     string `json:"actor"`    // whoami 或 controller
	Resource  string `json:"resource"` // 项目名/服务名
	Namespace string `json:"namespace"`
	From      string `json:"from,omitempty"` // 状态转换：from
	To        string `json:"to,omitempty"`   // 状态转换：to
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Outcome   string `json:"outcome"` // success / failure / warning
}

func runAudit(args []string) {
	flags := flag.NewFlagSet("audit", flag.ExitOnError)
	namespace := flags.String("namespace", "", "kubernetes namespace")
	format := flags.String("format", "jsonl", "输出格式：jsonl | csv | table")
	output := flags.String("output", "", "输出文件路径（留空=打印到 stdout）")
	since := flags.String("since", "", "起始时间（如 2026-04-01，留空=全部）")
	source := flags.String("source", "", "过滤来源：deploy | secret | drift（留空=全部）")
	flags.Parse(args)

	root, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, "找不到项目根目录:", err)
		os.Exit(1)
	}
	env, _ := readEnvFile(filepath.Join(root, "configs", "project.env"))
	projectName := envOrDefault(env, "PROJECT_NAME", filepath.Base(root))
	ns := *namespace
	if ns == "" {
		ns = envOrDefault(env, "KUBE_NAMESPACE", projectName)
	}

	var sinceTime time.Time
	if *since != "" {
		sinceTime, _ = time.Parse("2006-01-02", *since)
	}

	// 收集审计事件
	var events []AuditEvent

	// 1. 状态机历史（deploy/rollback/upgrade）
	if *source == "" || *source == "deploy" {
		deployEvents := collectDeployAudit(projectName, ns, env)
		events = append(events, deployEvents...)
	}

	// 2. Secret 审计日志
	if *source == "" || *source == "secret" {
		secretEvents := collectSecretAudit()
		events = append(events, secretEvents...)
	}

	// 3. Drift 审计日志（slog 文件暂不可查，从 etcd 读）
	if *source == "" || *source == "drift" {
		driftEvents := collectDriftAudit(projectName, ns, env)
		events = append(events, driftEvents...)
	}

	// 时间过滤
	if !sinceTime.IsZero() {
		var filtered []AuditEvent
		for _, e := range events {
			t, err := time.Parse(time.RFC3339, e.Timestamp)
			if err == nil && t.After(sinceTime) {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}

	// 按时间排序
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})

	if len(events) == 0 {
		P.Info("✅", "无审计记录")
		return
	}

	// 输出
	var out *os.File
	if *output != "" {
		out, err = os.Create(*output)
		if err != nil {
			fmt.Fprintln(os.Stderr, "创建输出文件失败:", err)
			os.Exit(1)
		}
		defer out.Close()
	} else {
		out = os.Stdout
	}

	switch *format {
	case "csv":
		writeAuditCSV(out, events)
	case "table":
		writeAuditTable(events)
	default: // jsonl
		writeAuditJSONL(out, events)
	}

	if *output != "" {
		P.Done(fmt.Sprintf("已导出 %d 条审计记录到 %s", len(events), *output))
	}
}

// collectDeployAudit 从状态机历史收集部署审计事件
func collectDeployAudit(project, namespace string, env map[string]string) []AuditEvent {
	store := state.NewAutoStore(env["ETCD_ENDPOINTS"])
	sm, err := state.New(store, project, namespace, "")
	if err != nil {
		return nil
	}

	var events []AuditEvent
	for _, h := range sm.Record().History {
		action := "deploy.transition"
		if string(h.To) == "DEPLOYING" {
			action = "deploy.start"
		} else if string(h.To) == "RUNNING" {
			action = "deploy.complete"
		} else if string(h.To) == "ROLLING_BACK" {
			action = "deploy.rollback"
		} else if string(h.To) == "LOCKED" {
			action = "sandbox.lock"
		}

		outcome := "success"
		if string(h.To) == "ROLLING_BACK" || string(h.To) == "RESTORING" {
			outcome = "failure"
		}

		events = append(events, AuditEvent{
			Timestamp: h.Timestamp.Format(time.RFC3339),
			Source:    "deploy",
			Action:    action,
			Actor:     "kp-cli",
			Resource:  project,
			Namespace: namespace,
			From:      string(h.From),
			To:        string(h.To),
			Version:   h.Version,
			Reason:    h.Reason,
			Outcome:   outcome,
		})
	}
	return events
}

// collectSecretAudit 从 ~/.kp/audit/secret.jsonl 收集 secret 操作审计
func collectSecretAudit() []AuditEvent {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".kp", "audit", "secret.jsonl")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var events []AuditEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		ts, _ := raw["ts"].(string)
		action, _ := raw["action"].(string)
		resource, _ := raw["resource"].(string)
		ns, _ := raw["namespace"].(string)
		// v2.8 B.7.1: 优先读 raw["actor"] (新数据), fallback "kp-cli" (旧数据兼容)
		actor, _ := raw["actor"].(string)
		if actor == "" {
			actor = "kp-cli"
		}

		events = append(events, AuditEvent{
			Timestamp: ts,
			Source:    "secret",
			Action:    "secret." + action,
			Actor:     actor,
			Resource:  resource,
			Namespace: ns,
			Outcome:   "success",
		})
	}
	return events
}

// collectDriftAudit drift 审计（当前 slog only，etcd 路径预留）
func collectDriftAudit(project, namespace string, env map[string]string) []AuditEvent {
	endpoints := env["ETCD_ENDPOINTS"]
	if endpoints == "" {
		return nil
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split(endpoints, ","),
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		return nil
	}
	defer cli.Close()

	prefix := fmt.Sprintf("/kubepivot/%s/%s/drift/", project, namespace)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := cli.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil || resp == nil {
		return nil
	}

	var events []AuditEvent
	for _, kv := range resp.Kvs {
		var raw map[string]string
		if err := json.Unmarshal(kv.Value, &raw); err != nil {
			continue
		}
		ts := raw["ts"]
		resource := raw["resource"]
		diffs := raw["diffs"]
		// v2.8 B.7.1: 优先读 raw["actor"] (新数据), fallback (旧数据兼容)
		actor := raw["actor"]
		if actor == "" {
			actor = "kubepivot-controller"
		}

		events = append(events, AuditEvent{
			Timestamp: ts,
			Source:    "drift",
			Action:    "drift.force-sync",
			Actor:     actor,
			Resource:  resource,
			Namespace: namespace,
			Reason:    diffs,
			Outcome:   "success",
		})
	}
	return events
}
func writeAuditJSONL(out *os.File, events []AuditEvent) {
	for _, e := range events {
		data, _ := json.Marshal(e)
		fmt.Fprintln(out, string(data))
	}
}

func writeAuditCSV(out *os.File, events []AuditEvent) {
	fmt.Fprintln(out, "timestamp,source,action,actor,resource,namespace,from,to,version,reason,outcome")
	for _, e := range events {
		fmt.Fprintf(out, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%q,%s\n",
			e.Timestamp, e.Source, e.Action, e.Actor,
			e.Resource, e.Namespace, e.From, e.To,
			e.Version, e.Reason, e.Outcome)
	}
}

func writeAuditTable(events []AuditEvent) {
	fmt.Printf("  %-25s  %-8s  %-20s  %-10s  %-8s  %s\n",
		"时间", "来源", "操作", "资源", "结果", "原因")
	fmt.Printf("  %s\n", strings.Repeat("─", 90))
	for _, e := range events {
		ts := e.Timestamp
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			ts = t.Local().Format("01-02 15:04:05")
		}
		outcome := colorize(colorGreen, e.Outcome)
		if e.Outcome == "failure" {
			outcome = colorize(colorRed, e.Outcome)
		}
		reason := e.Reason
		if len(reason) > 30 {
			reason = reason[:27] + "..."
		}
		fmt.Printf("  %-25s  %-8s  %-20s  %-10s  %-8s  %s\n",
			ts, e.Source, e.Action, e.Resource, outcome, reason)
	}
}
