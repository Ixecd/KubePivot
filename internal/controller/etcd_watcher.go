package controller

import (
	"context"
	"log"
	"os"

	"github.com/Ixecd/dev-toolkit/internal/state"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func (r *Reconciler) startEtcdWatcher(ctx context.Context) {
	key := state.EtcdKey("web3-blitz", "web3-blitz") // 使用修复后的导出函数

	cli, err := clientv3.New(clientv3.Config{
		Endpoints: []string{os.Getenv("ETCD_ENDPOINTS")},
	})
	if err != nil {
		log.Printf("[WARN] etcd Watch 启动失败，将只依赖定时对账: %v", err)
		return
	}
	defer cli.Close()

	watchChan := cli.Watch(ctx, key, clientv3.WithPrefix())

	for watchResp := range watchChan {
		if watchResp.Err() != nil {
			log.Printf("[ERROR] etcd Watch 错误: %v", watchResp.Err())
			continue
		}
		log.Println("📡 etcd 状态变更检测到，立即触发 Reconcile")
		r.reconcile()
	}
}