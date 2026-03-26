package controller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Ixecd/dev-toolkit/internal/state"
)

// Reconciler 负责周期性对账和事件驱动自愈
type Reconciler struct {
	sm         *state.Machine
	kubeconfig string
	resources  *ResourcesConfig
}

func NewReconciler(sm *state.Machine, kubeconfig string) *Reconciler {
	resources, err := LoadResources("")
	if err != nil {
		slog.Error("加载 configs/resources.yaml 失败", "err", err)
		// 降级：空配置，不监控任何资源
		resources = &ResourcesConfig{}
	}
	return &Reconciler{
		sm:         sm,
		kubeconfig: kubeconfig,
		resources:  resources,
	}
}

func (r *Reconciler) Start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	slog.Info("Reconciliation Loop 已启动", "resources", len(r.resources.Resources))

	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()

	// etcd Watch 事件驱动（实时触发）
	go r.startEtcdWatcher(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Reconciliation Loop 已关闭")
			return
		case <-ticker.C:
			r.reconcile()
		}
	}
}

func (r *Reconciler) reconcile() {
	for _, res := range r.resources.Resources {
		if err := r.checkAndHeal(res); err != nil {
			slog.Error("资源对账失败", "kind", res.Kind, "name", res.Name, "err", err)
		}
	}
}
