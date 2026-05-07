package state

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Store 持久化接口
type Store interface {
	Save(record *DeployRecord) error
	Load(project, namespace string) (*DeployRecord, error)
	Delete(project, namespace string) error
}

// ── etcd Store ────────────────────────────────────────────────────────────────

type etcdStore struct {
	client *clientv3.Client
}

func NewEtcdStore(endpoints []string) (Store, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 3 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 etcd 失败: %w", err)
	}
	return &etcdStore{client: client}, nil
}

func (s *etcdStore) Save(record *DeployRecord) error {
	data, err := marshalRecord(record)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := etcdKey(record.Project, record.Namespace)
	_, err = s.client.Put(ctx, key, string(data))
	if err != nil {
		return fmt.Errorf("写入 etcd 失败: %w", err)
	}
	slog.Debug("状态已保存到 etcd", "key", key, "state", record.State)
	return nil
}

func (s *etcdStore) Load(project, namespace string) (*DeployRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := etcdKey(project, namespace)
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("读取 etcd 失败: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("记录不存在: %s", key)
	}

	return unmarshalRecord(resp.Kvs[0].Value)
}

func (s *etcdStore) Delete(project, namespace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := etcdKey(project, namespace)
	_, err := s.client.Delete(ctx, key)
	return err
}

// ── 本地文件 Store ────────────────────────────────────────────────────────────

type localStore struct{}

func NewLocalStore() Store {
	return &localStore{}
}

func (s *localStore) Save(record *DeployRecord) error {
	path, err := localPath(record.Project, record.Namespace)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}

	data, err := marshalRecord(record)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入状态文件失败: %w", err)
	}
	slog.Debug("状态已保存到本地文件", "path", path, "state", record.State)
	return nil
}

func (s *localStore) Load(project, namespace string) (*DeployRecord, error) {
	path, err := localPath(project, namespace)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("记录不存在: %s", path)
		}
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}
	return unmarshalRecord(data)
}

func (s *localStore) Delete(project, namespace string) error {
	path, err := localPath(project, namespace)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除状态文件失败: %w", err)
	}
	return nil
}

// ── 辅助序列化函数（解决 undefined） ────────────────────────────────────────

func marshalRecord(record *DeployRecord) ([]byte, error) {
	return json.MarshalIndent(record, "", "  ")
}

func unmarshalRecord(data []byte) (*DeployRecord, error) {
	var r DeployRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("反序列化状态失败: %w", err)
	}
	return &r, nil
}

// ── 自动选择 Store ────────────────────────────────────────────────────────────

func NewAutoStore(etcdEndpoints string) Store {
	if etcdEndpoints == "" {
		slog.Debug("未配置 etcd，使用本地文件存储状态")
		return NewLocalStore()
	}

	endpoints := strings.Split(etcdEndpoints, ",")
	store, err := NewEtcdStore(endpoints)
	if err != nil {
		slog.Warn("etcd 连接失败，降级到本地文件存储状态", "err", err)
		return NewLocalStore()
	}

	slog.Debug("使用 etcd 存储状态", "endpoints", etcdEndpoints)
	return store
}

// ── 工具函数 ────────────────────────────────────────────────────────────────

func etcdKey(project, namespace string) string {
	return EtcdKey(project, namespace)
}

func localPath(project, namespace string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取 home 目录失败: %w", err)
	}
	return filepath.Join(home, ".kp", "state", project, namespace+".json"), nil
}

// autoMigrateToEtcd 检测到 etcd 可用且本地有状态时，自动迁移
func autoMigrateToEtcd(store Store, project, namespace string) error {
	// 只有当前 store 是 etcd 时才需要迁移
	etcdSt, ok := store.(*etcdStore)
	if !ok {
		return nil
	}

	// etcd 里已经有状态，不需要迁移
	if _, err := etcdSt.Load(project, namespace); err == nil {
		return nil
	}

	// 检查本地文件是否有状态
	localSt := &localStore{}
	localRecord, err := localSt.Load(project, namespace)
	if err != nil {
		// 本地也没有，新项目，不需要迁移
		return nil
	}

	// 迁移：把本地状态写入 etcd
	if err := etcdSt.Save(localRecord); err != nil {
		return fmt.Errorf("写入 etcd 失败: %w", err)
	}

	backupPath, _ := localPath(project, namespace)
	slog.Info("状态已从本地文件迁移到 etcd",
		"project", project,
		"namespace", namespace,
		"state", localRecord.State,
		"backup", backupPath,
	)
	fmt.Printf("✓ 状态已迁移到 etcd（本地备份保留：%s）\n", backupPath)
	return nil
}
