package bluegreen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"context"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	SlotBlue  = "blue"
	SlotGreen = "green"
)

type State struct {
	Active    string    `json:"active"`
	BlueTag   string    `json:"blue_tag"`
	GreenTag  string    `json:"green_tag"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *State) InactiveSlot() string {
	if s.Active == SlotBlue {
		return SlotGreen
	}
	return SlotBlue
}

type Store interface {
	Load(project, namespace, service string) (*State, error)
	Save(project, namespace, service string, s *State) error
}

// etcdStore etcd 优先存储
type etcdStore struct {
	client *clientv3.Client
}

func (e *etcdStore) key(project, namespace, service string) string {
	return fmt.Sprintf("kp/%s/%s/bluegreen/%s", project, namespace, service)
}

func (e *etcdStore) Load(project, namespace, service string) (*State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := e.client.Get(ctx, e.key(project, namespace, service))
	if err != nil || len(resp.Kvs) == 0 {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(resp.Kvs[0].Value, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (e *etcdStore) Save(project, namespace, service string, s *State) error {
	s.UpdatedAt = time.Now()
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = e.client.Put(ctx, e.key(project, namespace, service), string(data))
	return err
}

// fileStore 本地文件降级存储
type fileStore struct{ dir string }

func (f *fileStore) path(project, namespace, service string) string {
	return filepath.Join(f.dir, project, namespace, service+"-bluegreen.json")
}

func (f *fileStore) Load(project, namespace, service string) (*State, error) {
	data, err := os.ReadFile(f.path(project, namespace, service))
	if err != nil {
		return nil, err
	}
	var s State
	return &s, json.Unmarshal(data, &s)
}

func (f *fileStore) Save(project, namespace, service string, s *State) error {
	s.UpdatedAt = time.Now()
	data, _ := json.Marshal(s)
	path := f.path(project, namespace, service)
	os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, data, 0o644)
}

// NewAutoStore etcd 优先，降级本地文件
func NewAutoStore(etcdEndpoints string) Store {
	if etcdEndpoints != "" {
		client, err := clientv3.New(clientv3.Config{
			Endpoints:   []string{etcdEndpoints},
			DialTimeout: 3 * time.Second,
		})
		if err == nil {
			return &etcdStore{client: client}
		}
	}
	return &fileStore{dir: filepath.Join(os.Getenv("HOME"), ".kp", "bluegreen")}
}

// DefaultState 初始状态，blue 为 active
func DefaultState(tag string) *State {
	return &State{
		Active:   SlotBlue,
		BlueTag:  tag,
		GreenTag: "",
	}
}
