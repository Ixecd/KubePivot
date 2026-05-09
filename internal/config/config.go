// Package config provides system configuration loading.
// Loads from configs/system.yaml (built-in defaults), /etc/kp/config.yaml (ConfigMap mount),
// and environment variable KUBEPIVOT_* overrides.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SystemConfig holds all configurable system parameters.
type SystemConfig struct {
	Controller ControllerConfig `json:"controller" yaml:"controller"`
	Leader     LeaderConfig     `json:"leader" yaml:"leader"`
	Scheduler  SchedulerConfig  `json:"scheduler" yaml:"scheduler"`
	Migration  MigrationConfig  `json:"migration" yaml:"migration"`
	KVCache    KVCacheConfig    `json:"kvcache" yaml:"kvcache"`
	Etcd       EtcdConfig       `json:"etcd" yaml:"etcd"`
}

type ControllerConfig struct {
	Shards                int           `json:"shards" yaml:"shards"`
	Replicas              int           `json:"replicas" yaml:"replicas"`
	MetricsPort           int           `json:"metricsPort" yaml:"metricsPort"`
	WorkerPoolSize        int           `json:"workerPoolSize" yaml:"workerPoolSize"`
	WorkerPoolRateLimit   float64       `json:"workerPoolRateLimit" yaml:"workerPoolRateLimit"`
	TaskTimeout           time.Duration `json:"taskTimeout" yaml:"taskTimeout"`
	ReconcileInterval     time.Duration `json:"reconcileInterval" yaml:"reconcileInterval"`
	OrphanSweeperInterval time.Duration `json:"orphanSweeperInterval" yaml:"orphanSweeperInterval"`
	DriftSyncInterval     time.Duration `json:"driftSyncInterval" yaml:"driftSyncInterval"`
	GracePeriod           time.Duration `json:"gracePeriod" yaml:"gracePeriod"`
	ProtectedNamespaces   []string      `json:"protectedNamespaces" yaml:"protectedNamespaces"`
}
type LeaderConfig struct {
	EtcdTTL            time.Duration `json:"etcdTTL" yaml:"etcdTTL"`
	RetryInterval      time.Duration `json:"retryInterval" yaml:"retryInterval"`
	LeaseDuration      time.Duration `json:"leaseDuration" yaml:"leaseDuration"`
	LeaseRenewInterval time.Duration `json:"leaseRenewInterval" yaml:"leaseRenewInterval"`
}
type SchedulerConfig struct {
	RescheduleInterval time.Duration `json:"rescheduleInterval" yaml:"rescheduleInterval"`
	MaxMigrations      int           `json:"maxMigrations" yaml:"maxMigrations"`
	JitterThreshold    float64       `json:"jitterThreshold" yaml:"jitterThreshold"`
	JitterWindow       time.Duration `json:"jitterWindow" yaml:"jitterWindow"`
	JitterSpikeCount   int           `json:"jitterSpikeCount" yaml:"jitterSpikeCount"`
	WebhookPort        int           `json:"webhookPort" yaml:"webhookPort"`
}
type MigrationConfig struct {
	EvictTimeout     time.Duration `json:"evictTimeout" yaml:"evictTimeout"`
	WaitReadyTimeout time.Duration `json:"waitReadyTimeout" yaml:"waitReadyTimeout"`
	PausedBackoff    time.Duration `json:"pausedBackoff" yaml:"pausedBackoff"`
	MaxRetries       int           `json:"maxRetries" yaml:"maxRetries"`
}
type KVCacheConfig struct {
	Enabled              bool          `json:"enabled" yaml:"enabled"`
	MergeThreshold       int           `json:"mergeThreshold" yaml:"mergeThreshold"`
	ShardCount           int           `json:"shardCount" yaml:"shardCount"`
	StaleWatchdogMaxStale time.Duration `json:"staleWatchdogMaxStale" yaml:"staleWatchdogMaxStale"`
}
type EtcdConfig struct {
	CompactInterval         time.Duration `json:"compactInterval" yaml:"compactInterval"`
	DefragInterval          time.Duration `json:"defragInterval" yaml:"defragInterval"`
	QuotaBackendBytes       int64         `json:"quotaBackendBytes" yaml:"quotaBackendBytes"`
	SnapshotCount           int64         `json:"snapshotCount" yaml:"snapshotCount"`
	MaxRequestBytes         int64         `json:"maxRequestBytes" yaml:"maxRequestBytes"`
	AutoCompactionRetention time.Duration `json:"autoCompactionRetention" yaml:"autoCompactionRetention"`
	HeartbeatInterval       time.Duration `json:"heartbeatInterval" yaml:"heartbeatInterval"`
	ElectionTimeout         time.Duration `json:"electionTimeout" yaml:"electionTimeout"`
	InitialCorruptCheck     bool          `json:"initialCorruptCheck" yaml:"initialCorruptCheck"`
	CorruptCheckTime        time.Duration `json:"corruptCheckTime" yaml:"corruptCheckTime"`
	Endpoints               string        `json:"endpoints" yaml:"endpoints"`
	DialTimeout             time.Duration `json:"dialTimeout" yaml:"dialTimeout"`
	DataDir                 string        `json:"dataDir" yaml:"dataDir"`
}

// defaults returns a SystemConfig with built-in defaults.
// These are used as fallback when the YAML file is missing or fields are empty.
func defaults() SystemConfig {
	return SystemConfig{
		Controller: ControllerConfig{
			Shards: 10, Replicas: 3, MetricsPort: 9090, WorkerPoolSize: 20, WorkerPoolRateLimit: 10,
			TaskTimeout: 90 * time.Second, ReconcileInterval: 8 * time.Second,
			OrphanSweeperInterval: 30 * time.Second, DriftSyncInterval: 30 * time.Second,
			GracePeriod: 5 * time.Second,
			ProtectedNamespaces: []string{"kube-system", "kube-public", "kubepivot-system"},
		},
		Leader: LeaderConfig{
			EtcdTTL: 15 * time.Second, RetryInterval: 3 * time.Second,
			LeaseDuration: 15 * time.Second, LeaseRenewInterval: 2 * time.Second,
		},
		Scheduler: SchedulerConfig{
			RescheduleInterval: 5 * time.Minute, MaxMigrations: 0,
			JitterThreshold: 0.95, JitterWindow: 5 * time.Minute, JitterSpikeCount: 3, WebhookPort: 443,
		},
		Migration: MigrationConfig{
			EvictTimeout: 10 * time.Minute, WaitReadyTimeout: 5 * time.Minute,
			PausedBackoff: 30 * time.Second, MaxRetries: 3,
		},
		KVCache: KVCacheConfig{
			Enabled: true, MergeThreshold: 200, ShardCount: 16,
			StaleWatchdogMaxStale: 5 * time.Minute,
		},
		Etcd: EtcdConfig{
			CompactInterval: 1 * time.Hour, DefragInterval: 24 * time.Hour,
			QuotaBackendBytes: 8 * 1024 * 1024 * 1024, SnapshotCount: 10000,
			MaxRequestBytes: 10 * 1024 * 1024, AutoCompactionRetention: 1 * time.Hour,
			HeartbeatInterval: 200 * time.Millisecond, ElectionTimeout: 2000 * time.Millisecond,
			InitialCorruptCheck: true, CorruptCheckTime: 10 * time.Minute,
			Endpoints: "", DialTimeout: 5 * time.Second, DataDir: "/var/lib/etcd",
		},
	}
}

// Load reads system.yaml from the first existing path:
//   1. /etc/kp/config.yaml (ConfigMap mount, K8s deployment)
//   2. configs/system.yaml (local development)
// Falls back to built-in defaults if neither exists.
func Load() SystemConfig {
	cfg := defaults()
	path := resolveConfigPath()
	if path == "" {
		slog.Debug("config: no config file found, using built-in defaults")
		return applyEnvOverrides(cfg)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("config: failed to read config file, using defaults", "path", path, "err", err)
		return applyEnvOverrides(cfg)
	}

	var fileCfg SystemConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		slog.Warn("config: failed to parse config, using defaults", "path", path, "err", err)
		return applyEnvOverrides(cfg)
	}

	cfg = mergeDefaults(cfg, fileCfg)
	slog.Info("config: loaded", "path", path)
	return applyEnvOverrides(cfg)
}

func resolveConfigPath() string {
	if _, err := os.Stat("/etc/kp/config.yaml"); err == nil {
		return "/etc/kp/config.yaml"
	}
	if _, err := os.Stat("configs/system.yaml"); err == nil {
		return "configs/system.yaml"
	}
	return ""
}

// mergeDefaults overlays non-zero file values onto defaults.
func mergeDefaults(def, file SystemConfig) SystemConfig {
	mergeInt := func(d *int, f int) { if f != 0 { *d = f } }
	mergeInt64 := func(d *int64, f int64) { if f != 0 { *d = f } }
	mergeDur := func(d *time.Duration, f time.Duration) { if f != 0 { *d = f } }
	mergeFloat := func(d *float64, f float64) { if f != 0 { *d = f } }
	mergeStrSlice := func(d *[]string, f []string) { if len(f) > 0 { *d = f } }

	c := &def.Controller
	fc := file.Controller
	mergeInt(&c.Shards, fc.Shards)
	mergeInt(&c.Replicas, fc.Replicas)
	mergeInt(&c.WorkerPoolSize, fc.WorkerPoolSize)
	mergeDur(&c.TaskTimeout, fc.TaskTimeout)
	mergeDur(&c.ReconcileInterval, fc.ReconcileInterval)
	mergeDur(&c.OrphanSweeperInterval, fc.OrphanSweeperInterval)
	mergeDur(&c.DriftSyncInterval, fc.DriftSyncInterval)
	mergeDur(&c.GracePeriod, fc.GracePeriod)
	mergeStrSlice(&c.ProtectedNamespaces, fc.ProtectedNamespaces)

	l := &def.Leader
	fl := file.Leader
	mergeDur(&l.EtcdTTL, fl.EtcdTTL)
	mergeDur(&l.RetryInterval, fl.RetryInterval)
	mergeDur(&l.LeaseDuration, fl.LeaseDuration)
	mergeDur(&l.LeaseRenewInterval, fl.LeaseRenewInterval)

	s := &def.Scheduler
	fs := file.Scheduler
	mergeInt(&s.MaxMigrations, fs.MaxMigrations)
	mergeDur(&s.RescheduleInterval, fs.RescheduleInterval)
	mergeFloat(&s.JitterThreshold, fs.JitterThreshold)
	mergeDur(&s.JitterWindow, fs.JitterWindow)
	mergeInt(&s.JitterSpikeCount, fs.JitterSpikeCount)
	mergeInt(&s.WebhookPort, fs.WebhookPort)

	m := &def.Migration
	fm := file.Migration
	mergeDur(&m.EvictTimeout, fm.EvictTimeout)
	mergeDur(&m.WaitReadyTimeout, fm.WaitReadyTimeout)
	mergeDur(&m.PausedBackoff, fm.PausedBackoff)
	mergeInt(&m.MaxRetries, fm.MaxRetries)

	k := &def.KVCache
	fk := file.KVCache
	mergeInt(&k.MergeThreshold, fk.MergeThreshold)
	mergeInt(&k.ShardCount, fk.ShardCount)
	mergeDur(&k.StaleWatchdogMaxStale, fk.StaleWatchdogMaxStale)

	e := &def.Etcd
	fe := file.Etcd
	mergeDur(&e.CompactInterval, fe.CompactInterval)
	mergeDur(&e.DefragInterval, fe.DefragInterval)
	mergeDur(&e.DialTimeout, fe.DialTimeout)
	mergeDur(&e.AutoCompactionRetention, fe.AutoCompactionRetention)
	mergeDur(&e.HeartbeatInterval, fe.HeartbeatInterval)
	mergeDur(&e.ElectionTimeout, fe.ElectionTimeout)
	mergeDur(&e.CorruptCheckTime, fe.CorruptCheckTime)
	mergeInt64(&e.QuotaBackendBytes, fe.QuotaBackendBytes)
	mergeInt64(&e.SnapshotCount, fe.SnapshotCount)
	mergeInt64(&e.MaxRequestBytes, fe.MaxRequestBytes)
	if fe.InitialCorruptCheck {
		e.InitialCorruptCheck = true
	}
	if fe.Endpoints != "" {
		e.Endpoints = fe.Endpoints
	}
	if fe.DataDir != "" {
		e.DataDir = fe.DataDir
	}

	return def
}

// applyEnvOverrides applies KUBEPIVOT_* environment variable overrides.
// Env vars take highest priority (override both defaults and YAML).
func applyEnvOverrides(cfg SystemConfig) SystemConfig {
	if v := os.Getenv("KUBEPIVOT_SHARDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Controller.Shards = n
		}
	}
	if v := os.Getenv("KUBEPIVOT_ETCD_ENDPOINTS"); v != "" {
		cfg.Etcd.Endpoints = v
	}
	if v := os.Getenv("KUBEPIVOT_WORKER_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Controller.WorkerPoolSize = n
		}
	}
	if v := os.Getenv("KUBEPIVOT_KVCACHE_MERGE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.KVCache.MergeThreshold = n
		}
	}
	return cfg
}

// Validate checks required fields and returns errors for invalid config.
func (cfg SystemConfig) Validate() error {
	if cfg.Controller.Shards < 1 {
		return fmt.Errorf("controller.shards must be >= 1, got %d", cfg.Controller.Shards)
	}
	if cfg.Controller.Replicas < 3 {
		return fmt.Errorf("controller.replicas must be >= 3 (etcd quorum), got %d", cfg.Controller.Replicas)
	}
	if cfg.Controller.Replicas%2 == 0 {
		return fmt.Errorf("controller.replicas must be odd (etcd quorum), got %d", cfg.Controller.Replicas)
	}
	if cfg.Controller.WorkerPoolSize < 1 {
		return fmt.Errorf("controller.workerPoolSize must be >= 1, got %d", cfg.Controller.WorkerPoolSize)
	}
	if cfg.Scheduler.JitterThreshold < 0 || cfg.Scheduler.JitterThreshold > 1 {
		return fmt.Errorf("scheduler.jitterThreshold must be 0-1, got %f", cfg.Scheduler.JitterThreshold)
	}
	return nil
}

// IsProtectedNamespace checks if a namespace is in the protected list.
func (cfg ControllerConfig) IsProtectedNamespace(ns string) bool {
	for _, p := range cfg.ProtectedNamespaces {
		if strings.EqualFold(p, ns) {
			return true
		}
	}
	return false
}
