// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
	"github.com/Ixecd/kubepivot/internal/route"
	"github.com/Ixecd/kubepivot/internal/sharding"
	"github.com/Ixecd/kubepivot/internal/state"
	"gopkg.in/yaml.v3"
)

// ════════════════════════════════════════════════════════════════════════════
// VerifiedTrafficWriter — v2.6.1 多环境流量传播链「生产者」
//
// 周期扫描所有 RUNNING + 5min 稳态的项目，把 K8s 实际 traffic 状态写入
// kubepivot-verified-traffic ConfigMap。下游 env 通过 kp deploy --from-env
// 读取这个 ConfigMap 完成"已验证 traffic 配置"的传播。
//
// 跟 orphanSweeper 同模式：
//   - 每 pod 跑（不是 leader-only）
//   - shardSet 过滤本 pod 持有的 namespace
//   - warn-only 失败（不阻断后续 ns 处理）
//
// 详细设计：docs/design/traffic-multi-env-impl-draft.md §5
// ════════════════════════════════════════════════════════════════════════════

const (
	// 5 min RUNNING 稳态阈值（Q2=B 拍板）
	verifiedTrafficStableThreshold = 5 * time.Minute

	// 1 min 周期扫描间隔（跟 sweeper 60s / orphanSweeper 30s 同量级）
	verifiedTrafficScanInterval = 1 * time.Minute

	// 写入的 ConfigMap 名（每个 ns 一个，固定名）
	verifiedTrafficConfigMapName = "kubepivot-verified-traffic"
)

// 函数变量注入式 mock（与 v2.7 readTokenFile / kubectl func 同模式）
//
// 测试时可替换这 5 个变量做隔离测试，避免依赖真实 K8s API
var (
	loadStateRecord            = defaultLoadStateRecord
	readResourcesConfigMap     = defaultReadResourcesConfigMap
	readActualRoutes           = defaultReadActualRoutes
	needsTrafficUpdate         = defaultNeedsTrafficUpdate
	writeVerifiedTrafficCM     = defaultWriteVerifiedTrafficCM
)

// runVerifiedTrafficWriter 周期扫描循环。跟 orphanSweeper 风格 1:1 对齐。
//
// gate 链（任一不通过即跳过本 ns）：
//   1. shardMgr.Owns(ns)              shard 过滤
//   2. record.State == RUNNING         state 过滤
//   3. RunningSinceFromHistory ≥ 5min  稳态过滤
//   4. cfg.HasBlueGreen()              蓝绿启用过滤
//   5. !needsTrafficUpdate             重复写过滤（Q2 校准点 B）
func runVerifiedTrafficWriter(
	ctx context.Context,
	gs *GlobalState,
	shardMgr *sharding.MultiLeaseManager,
	totalShards int,
	kubeconfig string,
) {
	interval := verifiedTrafficScanInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("🔄 VerifiedTrafficWriter 启动 (v2.6.1)",
		"interval", interval,
		"stable_threshold", verifiedTrafficStableThreshold,
	)

	for {
		select {
		case <-ctx.Done():
			slog.Info("🔄 VerifiedTrafficWriter 退出")
			return
		case <-ticker.C:
			scanOnceForVerifiedTraffic(ctx, gs, shardMgr, totalShards, kubeconfig)
		}
	}
}

// scanOnceForVerifiedTraffic 单轮扫描所有 namespace。
// 错误全部 warn-only，不阻断后续 ns 处理。
func scanOnceForVerifiedTraffic(
	ctx context.Context,
	gs *GlobalState,
	shardMgr *sharding.MultiLeaseManager,
	totalShards int,
	kubeconfig string,
) {
	namespaces := gs.ListProjects()

	written := 0
	for _, ns := range namespaces {
		if processNamespaceForVerifiedTraffic(ctx, ns, shardMgr, totalShards, kubeconfig) {
			written++
		}
	}

	if written > 0 {
		slog.Info("🔄 VerifiedTrafficWriter 本轮写入",
			"count", written, "scanned", len(namespaces))
	}
}

// processNamespaceForVerifiedTraffic 处理单个 namespace。
// 返回 true 表示本轮写入了 ConfigMap，false 表示跳过（任一 gate 不通过）或失败。
func processNamespaceForVerifiedTraffic(
	ctx context.Context,
	ns string,
	shardMgr *sharding.MultiLeaseManager,
	totalShards int,
	kubeconfig string,
) bool {
	// Gate 1: shard 过滤
	if shardMgr != nil && !shardMgr.Shards().OwnsNamespace(ns, totalShards) {
		return false
	}

	// Gate 2 + 3: state RUNNING + 5min 稳态
	record, err := loadStateRecord(ns)
	if err != nil {
		// 静默：项目可能没初始化 state（首次部署 IDLE 阶段）
		return false
	}
	runningSince := state.RunningSinceFromHistory(record)
	if runningSince == nil {
		return false // 当前不在 RUNNING
	}
	if time.Since(*runningSince) < verifiedTrafficStableThreshold {
		return false // 不到 5min 稳态
	}

	// Gate 4: 读 resources.yaml + HasBlueGreen 过滤
	cfg, err := readResourcesConfigMap(ctx, kubeconfig, ns)
	if err != nil {
		slog.Warn("VerifiedTrafficWriter: 读 resources ConfigMap 失败",
			"ns", ns, "err", err)
		return false
	}
	if !cfg.HasBlueGreen() {
		return false // 项目没启用蓝绿，不需要 verified-traffic
	}

	// 读 K8s 实际 traffic 路由
	actualRoutes, err := readActualRoutes(ctx, kubeconfig, ns, cfg.Traffic)
	if err != nil {
		slog.Warn("VerifiedTrafficWriter: 读 K8s 实际路由失败",
			"ns", ns, "err", err)
		return false
	}

	// Gate 5: needsUpdate（diff vs 当前 ConfigMap）
	if !needsTrafficUpdate(ctx, kubeconfig, ns, actualRoutes) {
		return false // 已是最新状态
	}

	// 写 ConfigMap
	if err := writeVerifiedTrafficCM(ctx, kubeconfig, ns,
		cfg.Traffic, actualRoutes, record, *runningSince); err != nil {
		slog.Warn("VerifiedTrafficWriter: 写 ConfigMap 失败",
			"ns", ns, "err", err)
		return false
	}

	slog.Info("🔄 VerifiedTrafficWriter 已写入",
		"ns", ns,
		"running_since", runningSince.Format(time.RFC3339),
		"elapsed", time.Since(*runningSince).Round(time.Second),
	)
	return true
}

// ════════════════════════════════════════════════════════════════════════════
// 默认实现（生产环境用，测试时通过函数变量替换）
// ════════════════════════════════════════════════════════════════════════════

// defaultLoadStateRecord 从 store 读取 state record。
// v2.4 约定：project ≡ namespace（GlobalState.GetOrCreateMachine 同语义）
func defaultLoadStateRecord(ns string) (*state.DeployRecord, error) {
	store := state.NewAutoStore(os.Getenv("ETCD_ENDPOINTS"))
	return store.Load(ns, ns)
}

// defaultReadResourcesConfigMap 读 kubepivot-resources ConfigMap + yaml.Unmarshal。
//
// 跟 v2.5 既有 loadResourcesConfigMap 同模式，但返回完整 ResourcesConfig 而非
// 只填 GlobalState（v2.6.1 verifiedTrafficWriter 需要 traffic 字段，不在 GlobalState）。
//
// B 路径选择（不扩展 GlobalState 加 traffic 字段）：
//   - GlobalState.GetProject 签名不变，既有 reconcile 路径不动
//   - 跟 v2.7 LabelGetter 选独立接口的工程教训对齐
//   - 重复解析 yaml 的开销可忽略（1min 一轮 + needsUpdate 进一步过滤）
func defaultReadResourcesConfigMap(ctx context.Context, kubeconfig, ns string) (*ResourcesConfig, error) {
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"get", "configmap", "kubepivot-resources",
		"-n", ns,
		"-o", "jsonpath={.data.resources\\.yaml}",
		"--ignore-not-found",
	)
	if err != nil {
		return nil, fmt.Errorf("kubectl get configmap: %w", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, fmt.Errorf("kubepivot-resources ConfigMap 为空或不存在")
	}

	var cfg ResourcesConfig
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		return nil, fmt.Errorf("yaml.Unmarshal resources: %w", err)
	}
	return &cfg, nil
}

// defaultReadActualRoutes 通过 route.Provider 读 K8s 实际路由。
// 用 ProviderForKind（traffic.Kind 为空时自动检测）。
func defaultReadActualRoutes(ctx context.Context, kubeconfig, ns string, traffic *Traffic) ([]route.Route, error) {
	provider, err := route.ProviderForKind(ctx, traffic.Kind)
	if err != nil {
		return nil, fmt.Errorf("route.ProviderForKind: %w", err)
	}
	return provider.GetCurrentRoutes(ctx, ns, traffic.Refs.Name)
}

// defaultNeedsTrafficUpdate 对比 K8s 实际路由 vs 现有 ConfigMap data。
//
// 返回 true 表示需要写入：
//   - ConfigMap 不存在（首次创建）
//   - 现有 traffic.yaml 解析失败（损坏，重写覆盖）
//   - actual routes 跟 ConfigMap 里的 routes 不一致（service+weight 字段级 diff）
//
// 返回 false 表示已是最新状态，跳过本轮写入（避免每分钟无谓 update）。
func defaultNeedsTrafficUpdate(ctx context.Context, kubeconfig, ns string, actual []route.Route) bool {
	out, err := executor.GetExecutor().Kubectl(ctx, kubeconfig,
		"get", "configmap", verifiedTrafficConfigMapName,
		"-n", ns,
		"-o", "jsonpath={.data.traffic\\.yaml}",
		"--ignore-not-found",
	)
	if err != nil {
		return true // 读失败保守判定为需要更新
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return true // ConfigMap 不存在 → 首次创建
	}

	var existing Traffic
	if err := yaml.Unmarshal(out, &existing); err != nil {
		return true // 解析失败 → 重写覆盖
	}

	// 字段级 diff：actual 跟 existing.Routes 比较 service+weight
	if len(existing.Routes) != len(actual) {
		return true
	}
	actualMap := make(map[string]int32, len(actual))
	for _, r := range actual {
		actualMap[r.Service] = r.Weight
	}
	for _, r := range existing.Routes {
		w, ok := actualMap[r.Service]
		if !ok || w != r.Weight {
			return true
		}
	}
	return false
}

// defaultWriteVerifiedTrafficCM 写 verified-traffic ConfigMap 到 K8s。
//
// 跟 cmd/kp/controller_enroll.go syncResourcesConfigMap 同风格：
//   - 手拼 yaml + indent helper（block scalar 处理 traffic.yaml / source.yaml 内容）
//   - kubectl apply -f - 喂 stdin
//   - sha256 annotation 用于 diff 加速（未来 v2.7+ 候选）
func defaultWriteVerifiedTrafficCM(
	ctx context.Context, kubeconfig, ns string,
	traffic *Traffic, actualRoutes []route.Route,
	record *state.DeployRecord, runningSince time.Time,
) error {
	// 构造 traffic 快照：用 K8s 实际状态（actualRoutes），不用 cfg.Traffic.Routes
	// 这是核心论断："verified-traffic 是 K8s 原生状态广播"，写实际不写声明
	snapshot := *traffic
	snapshot.Routes = make([]TrafficRoute, len(actualRoutes))
	for i, r := range actualRoutes {
		snapshot.Routes[i] = TrafficRoute{Service: r.Service, Weight: r.Weight}
	}

	trafficYAML, err := yaml.Marshal(&snapshot)
	if err != nil {
		return fmt.Errorf("yaml.Marshal traffic: %w", err)
	}

	source := buildSourceMetadata(ns, record, runningSince)
	sourceYAML, err := yaml.Marshal(source)
	if err != nil {
		return fmt.Errorf("yaml.Marshal source: %w", err)
	}

	hash := sha256HexBytes(trafficYAML)
	cmYAML := buildVerifiedTrafficCMYAML(ns, string(trafficYAML), string(sourceYAML),
		runningSince, hash, record.Version)

	cmd := executor.GetExecutor().CmdKubectl(ctx, kubeconfig,
		"apply", "-f", "-", "--namespace", ns,
	)
	cmd.Stdin = strings.NewReader(cmYAML)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply: %w (out: %s)", err, string(out))
	}
	return nil
}

// ════════════════════════════════════════════════════════════════════════════
// 辅助函数
// ════════════════════════════════════════════════════════════════════════════

// sourceMetadata verified-traffic ConfigMap 的 source.yaml 数据结构。
// 双文件设计：traffic.yaml 是业务数据，source.yaml 是出处溯源（审计用）
type sourceMetadata struct {
	Project      string `yaml:"project"`
	Namespace    string `yaml:"namespace"`
	Version      string `yaml:"version"`
	RunningSince string `yaml:"runningSince"`
	VerifiedAt   string `yaml:"verifiedAt"`
	KpVersion    string `yaml:"kpVersion"`
}

func buildSourceMetadata(ns string, record *state.DeployRecord, runningSince time.Time) *sourceMetadata {
	return &sourceMetadata{
		Project:      record.Project,
		Namespace:    ns,
		Version:      record.Version,
		RunningSince: runningSince.UTC().Format(time.RFC3339),
		VerifiedAt:   time.Now().UTC().Format(time.RFC3339),
		KpVersion:    "v2.6.1", // TODO v2.7+: 从 kp 版本读取
	}
}

// buildVerifiedTrafficCMYAML 手拼 ConfigMap yaml（跟 syncResourcesConfigMap 风格一致）
func buildVerifiedTrafficCMYAML(
	ns, trafficYAML, sourceYAML string,
	runningSince time.Time, hash, version string,
) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  labels:
    kubepivot.io/verified-traffic: "true"
    app.kubernetes.io/managed-by: kp
    app.kubernetes.io/component: traffic-layer
  annotations:
    kubepivot.io/last-verified: %q
    kubepivot.io/source-state: "RUNNING"
    kubepivot.io/running-since: %q
    kubepivot.io/source-version: %q
    kubepivot.io/traffic-sha256: %q
data:
  traffic.yaml: |
%s
  source.yaml: |
%s
`,
		verifiedTrafficConfigMapName,
		ns,
		time.Now().UTC().Format(time.RFC3339),
		runningSince.UTC().Format(time.RFC3339),
		version,
		hash,
		indentYAML(trafficYAML, "    "),
		indentYAML(sourceYAML, "    "),
	)
}

// indentYAML 给每行加缩进前缀（跟 cmd/kp/controller_enroll.go indent 同实现，
// 跨包私有不能复用，本地复制一份）
func indentYAML(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// sha256HexBytes 计算 sha256 hex（跟 cmd/kp/controller_enroll.go sha256Hex 同实现，
// 跨包私有不能复用，本地复制一份）
func sha256HexBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
