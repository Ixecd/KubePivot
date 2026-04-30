package scheduler

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SchedulerMetrics 调度器相关指标
type SchedulerMetrics struct {
	iterationsTotal   prometheus.Counter
	solveDurationSec  prometheus.Gauge
	nodeUtilCPU       *prometheus.GaugeVec
	nodeUtilMem       *prometheus.GaugeVec
	oomKillTotal      prometheus.Counter
	fallbackLevel     prometheus.Gauge
	podMigrationTotal prometheus.Counter
}

var (
	schedMetrics     *SchedulerMetrics
	schedMetricsOnce sync.Once
)

func GetSchedulerMetrics() *SchedulerMetrics {
	schedMetricsOnce.Do(func() {
		schedMetrics = &SchedulerMetrics{
			iterationsTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "kubepivot_scheduler_iterations_total",
				Help: "Total number of scheduling iterations (including coordinator loops).",
			}),
			solveDurationSec: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "kubepivot_scheduler_solve_duration_seconds",
				Help: "Duration of the last scheduling solve in seconds.",
			}),
			nodeUtilCPU: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "kubepivot_node_utilization_cpu_ratio",
				Help: "Current CPU utilization ratio per node.",
			}, []string{"node"}),
			nodeUtilMem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Name: "kubepivot_node_utilization_memory_ratio",
				Help: "Current memory utilization ratio per node.",
			}, []string{"node"}),
			oomKillTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "kubepivot_pod_oomkill_total",
				Help: "Total number of OOM-killed pods observed.",
			}),
			fallbackLevel: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "kubepivot_scheduler_fallback_level",
				Help: "Current degradation level of the rescheduler (0-3).",
			}),
			podMigrationTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "kubepivot_pod_migration_total",
				Help: "Total number of pods migrated by the rescheduler.",
			}),
		}
	})
	return schedMetrics
}

// Describe 实现 prometheus.Collector
func (m *SchedulerMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.iterationsTotal.Describe(ch)
	m.solveDurationSec.Describe(ch)
	m.nodeUtilCPU.Describe(ch)
	m.nodeUtilMem.Describe(ch)
	m.oomKillTotal.Describe(ch)
	m.fallbackLevel.Describe(ch)
	m.podMigrationTotal.Describe(ch)
}

// Collect 实现 prometheus.Collector
func (m *SchedulerMetrics) Collect(ch chan<- prometheus.Metric) {
	m.iterationsTotal.Collect(ch)
	m.solveDurationSec.Collect(ch)
	m.nodeUtilCPU.Collect(ch)
	m.nodeUtilMem.Collect(ch)
	m.oomKillTotal.Collect(ch)
	m.fallbackLevel.Collect(ch)
	m.podMigrationTotal.Collect(ch)
}

// 便捷方法
func (m *SchedulerMetrics) IncIterations() {
	m.iterationsTotal.Inc()
}

func (m *SchedulerMetrics) SetSolveDuration(d time.Duration) {
	m.solveDurationSec.Set(d.Seconds())
}

func (m *SchedulerMetrics) SetNodeUtilCPU(node string, val float64) {
	m.nodeUtilCPU.WithLabelValues(node).Set(val)
}

func (m *SchedulerMetrics) SetNodeUtilMem(node string, val float64) {
	m.nodeUtilMem.WithLabelValues(node).Set(val)
}

func (m *SchedulerMetrics) IncOOMKill() {
	m.oomKillTotal.Inc()
}

func (m *SchedulerMetrics) SetFallbackLevel(level int) {
	m.fallbackLevel.Set(float64(level))
}

func (m *SchedulerMetrics) IncPodMigration() {
	m.podMigrationTotal.Inc()
}
