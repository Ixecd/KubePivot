package controller

import (
	"context"
	"log"
	"time"

	"github.com/Ixecd/dev-toolkit/internal/state"
)

type Reconciler struct {
	sm        *state.Machine
	resources *ResourcesConfig
}

func NewReconciler(sm *state.Machine) *Reconciler {
	resources, err := LoadResources()
	if err != nil {
		log.Fatalf("加载 configs/resources.yaml 失败: %v", err)
	}
	return &Reconciler{
		sm:        sm,
		resources: resources,
	}
}

func (r *Reconciler) Start(ctx context.Context) {
	log.Println("🔄 Reconciliation Loop 已启动（etcd Watch + 定期对账）")

	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()

	// 启动 etcd Watch（实时事件驱动）
	go r.startEtcdWatcher(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("⛔ Reconciliation Loop 优雅关闭")
			return
		case <-ticker.C:
			r.reconcile()
		}
	}
}

func (r *Reconciler) reconcile() {
	for _, res := range r.resources.Resources {
		if err := r.checkAndHeal(res); err != nil {
			log.Printf("[ERROR] 资源 %s/%s 对账失败: %v", res.Kind, res.Name, err)
		}
	}
}