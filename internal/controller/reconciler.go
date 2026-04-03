package controller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Ixecd/kubepivot/internal/state"
)

// Reconciler 负责周期性对账和事件驱动自愈
type Reconciler struct {
	sm         *state.Machine
	kubeconfig string
	resources  *ResourcesConfig
	detector   Detector
	helm       HelmClient
	queue      *ReconcileQueue
}

func NewReconciler(sm *state.Machine, kubeconfig string) *Reconciler {
	resources, err := LoadResources("")
	if err != nil {
		slog.Error("加载 configs/resources.yaml 失败", "err", err)
		resources = &ResourcesConfig{}
	}
	return &Reconciler{
		sm:         sm,
		kubeconfig: kubeconfig,
		resources:  resources,
		detector:   NewKubectlDetector(kubeconfig),
		helm:       &RealHelmClient{},
		queue:      NewReconcileQueue(),
	}
}

func (r *Reconciler) Start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	slog.Info("Reconciliation Loop 已启动", "resources", len(r.resources.Resources))

	// 启动 WorkQueue Worker
	r.queue.Run(ctx, func(reason string) {
		slog.Debug("WorkQueue 触发 Reconcile", "key", reason)
		r.reconcile()
	})

	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()

	wg.Add(1)

	go func() {
		defer wg.Done()
		r.StartDriftSyncLoop(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		r.StartSandboxGCLoop(ctx)
	}()

	go r.startEtcdWatcher(ctx, wg)

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
