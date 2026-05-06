// internal/scheduler/webhook_test.go
package scheduler

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ixecd/kubepivot/internal/metrics"
	"github.com/Ixecd/kubepivot/internal/sizing"
)

// mockLister 同时实现 PodLister 和 NodeLister
type mockLister struct {
	pods  []*PodInfo
	nodes []*NodeInfo
}

func (m *mockLister) ListAllPods(ctx context.Context) ([]*PodInfo, error) {
	return m.pods, nil
}
func (m *mockLister) ListAllNodes(ctx context.Context) ([]*NodeInfo, error) {
	return m.nodes, nil
}

// fakeSizingAndMetrics 占位的 MetricsProvider 和 SizingProvider
type fakeSizingAndMetrics struct{}

func (f *fakeSizingAndMetrics) GetPodMetrics(ctx context.Context, namespace, pod string) (*metrics.PodMetrics, error) {
	return nil, nil
}
func (f *fakeSizingAndMetrics) QueryRange(ctx context.Context, cpuQuery, memQuery string, start time.Time, step time.Duration) ([]*metrics.PodMetrics, error) {
	return nil, nil
}
func (f *fakeSizingAndMetrics) Compute(ctx context.Context, samples []*metrics.PodMetrics, profile sizing.Profile) (*sizing.Suggestion, error) {
	return &sizing.Suggestion{RecommendedCPU: 100, RecommendedMem: 128 * 1024 * 1024, Confidence: 1.0}, nil
}

func TestWebhookMutate_PodAssigned(t *testing.T) {
	// 注入一个装得下 Pod 的节点
	lister := &mockLister{
		pods:  nil,
		nodes: []*NodeInfo{{Name: "node1", AllocatableCPU: 2000, AllocatableMemory: 1024 * 1024 * 1024}},
	}
	sched := NewScheduler(lister, lister, &fakeSizingAndMetrics{}, &fakeSizingAndMetrics{}, nil)
	ws := NewWebhookServer(sched, ":8443", "", "")

	// 启动 TLS 测试服务器
	certPEM, keyPEM, _, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts := httptest.NewUnstartedServer(ws.server.Handler)
	ts.TLS = ws.server.TLSConfig
	ts.StartTLS()
	defer ts.Close()

	// 构造 AdmissionReview 请求
	review := admissionReviewRequest{
		APIVersion: "admission.k8s.io/v1",
		Kind:       json.RawMessage(`{"group":"admission.k8s.io","version":"v1","kind":"AdmissionReview"}`),
		Request: admissionRequest{
			UID: "uid-1",
			Kind: struct {
				Group   string `json:"group"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
			}{Group: "", Version: "v1", Kind: "Pod"},
			Object: podObj{
				Metadata: struct {
					Name         string `json:"name"`
					Namespace    string `json:"namespace"`
					GenerateName string `json:"generateName"`
				}{Name: "pod-a", Namespace: "default"},
				Spec: struct {
					NodeSelector map[string]string `json:"nodeSelector"`
					Containers   []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					} `json:"containers"`
				}{
					Containers: []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					}{
						{Name: "app", Resources: struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						}{Requests: struct {
							CPU    string `json:"cpu"`
							Memory string `json:"memory"`
						}{CPU: "100m", Memory: "128Mi"}}},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(review)
	// 从生成的证书中提取根证书并加入池
	roots := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	roots.AddCert(leaf)
	client := ts.Client()

	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.TLSClientConfig = &tls.Config{
			RootCAs: roots,
		}
	}

	resp, err := client.Post(ts.URL+"/mutate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var respReview admissionReviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&respReview); err != nil {
		t.Fatal(err)
	}
	if !respReview.Response.Allowed {
		t.Error("expected Allowed=true")
	}
	if len(respReview.Response.Patch) == 0 {
		t.Error("expected non-empty patch")
	}
}

func TestWebhookMutate_AssignFails_AllowedTrue(t *testing.T) {
	// 没有节点，分配必定失败
	lister := &mockLister{
		pods:  nil,
		nodes: []*NodeInfo{},
	}
	sched := NewScheduler(lister, lister, &fakeSizingAndMetrics{}, &fakeSizingAndMetrics{}, nil)
	ws := NewWebhookServer(sched, ":8443", "", "")

	certPEM, keyPEM, _, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts := httptest.NewUnstartedServer(ws.server.Handler)
	ts.TLS = ws.server.TLSConfig
	ts.StartTLS()
	defer ts.Close()

	review := admissionReviewRequest{
		APIVersion: "admission.k8s.io/v1",
		Kind:       json.RawMessage(`{"group":"admission.k8s.io","version":"v1","kind":"AdmissionReview"}`),
		Request: admissionRequest{
			UID: "uid-2",
			Kind: struct {
				Group   string `json:"group"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
			}{Group: "", Version: "v1", Kind: "Pod"},
			Object: podObj{
				Metadata: struct {
					Name         string `json:"name"`
					Namespace    string `json:"namespace"`
					GenerateName string `json:"generateName"`
				}{Name: "pod-b", Namespace: "default"},
				Spec: struct {
					NodeSelector map[string]string `json:"nodeSelector"`
					Containers   []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					} `json:"containers"`
				}{
					Containers: []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					}{
						{Name: "app", Resources: struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						}{Requests: struct {
							CPU    string `json:"cpu"`
							Memory string `json:"memory"`
						}{CPU: "100m", Memory: "128Mi"}}},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(review)
	client := ts.Client()
	resp, err := client.Post(ts.URL+"/mutate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var respReview admissionReviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&respReview); err != nil {
		t.Fatal(err)
	}
	if !respReview.Response.Allowed {
		t.Error("expected Allowed=true even if assign fails")
	}
	if len(respReview.Response.Patch) != 0 {
		t.Error("expected empty patch when assign fails")
	}
}

func TestWebhookMutate_GenerateName(t *testing.T) {
	// 验证 GenerateName 也能正确处理
	lister := &mockLister{
		pods:  nil,
		nodes: []*NodeInfo{{Name: "node1", AllocatableCPU: 2000, AllocatableMemory: 1024 * 1024 * 1024}},
	}
	sched := NewScheduler(lister, lister, &fakeSizingAndMetrics{}, &fakeSizingAndMetrics{}, nil)
	ws := NewWebhookServer(sched, ":8443", "", "")

	certPEM, keyPEM, _, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts := httptest.NewUnstartedServer(ws.server.Handler)
	ts.TLS = ws.server.TLSConfig
	ts.StartTLS()
	defer ts.Close()

	review := admissionReviewRequest{
		APIVersion: "admission.k8s.io/v1",
		Kind:       json.RawMessage(`{"group":"admission.k8s.io","version":"v1","kind":"AdmissionReview"}`),
		Request: admissionRequest{
			UID: "uid-3",
			Kind: struct {
				Group   string `json:"group"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
			}{Group: "", Version: "v1", Kind: "Pod"},
			Object: podObj{
				Metadata: struct {
					Name         string `json:"name"`
					Namespace    string `json:"namespace"`
					GenerateName string `json:"generateName"`
				}{GenerateName: "deploy-", Namespace: "default"},
				Spec: struct {
					NodeSelector map[string]string `json:"nodeSelector"`
					Containers   []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					} `json:"containers"`
				}{
					Containers: []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					}{
						{Name: "app", Resources: struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						}{Requests: struct {
							CPU    string `json:"cpu"`
							Memory string `json:"memory"`
						}{CPU: "100m", Memory: "128Mi"}}},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(review)
	client := ts.Client()
	resp, err := client.Post(ts.URL+"/mutate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var respReview admissionReviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&respReview); err != nil {
		t.Fatal(err)
	}
	if !respReview.Response.Allowed {
		t.Error("expected Allowed=true for GenerateName pod")
	}
	if len(respReview.Response.Patch) == 0 {
		t.Error("expected non-empty patch for GenerateName pod")
	}
}

func TestMigrationTargetHint_SetAndPop(t *testing.T) {
	SetMigrationTargetHint("ns", "pod-a", "node5")
	hint, ok := PopMigrationTargetHint("ns", "pod-a")
	if !ok {
		t.Fatal("expected hint to be found")
	}
	if hint != "node5" {
		t.Errorf("hint = %s, want node5", hint)
	}

	// Pop removed it — second call should not find
	_, ok = PopMigrationTargetHint("ns", "pod-a")
	if ok {
		t.Error("hint should be consumed after Pop")
	}
}

func TestMigrationTargetHint_Miss(t *testing.T) {
	_, ok := PopMigrationTargetHint("ns", "nonexistent")
	if ok {
		t.Error("should not find nonexistent hint")
	}
}

func TestWebhookMutate_MigrationTargetHint(t *testing.T) {
	// Pre-set a migration target hint — webhook should use it, not the scheduler
	SetMigrationTargetHint("default", "pod-mig", "node-migration-target")

	// Nodes: node1 is the only valid scheduler assignment, but hint points elsewhere
	lister := &mockLister{
		pods:  nil,
		nodes: []*NodeInfo{{Name: "node1", AllocatableCPU: 2000, AllocatableMemory: 1024 * 1024 * 1024}},
	}
	sched := NewScheduler(lister, lister, &fakeSizingAndMetrics{}, &fakeSizingAndMetrics{}, nil)
	ws := NewWebhookServer(sched, ":8443", "", "")

	certPEM, keyPEM, _, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts := httptest.NewUnstartedServer(ws.server.Handler)
	ts.TLS = ws.server.TLSConfig
	ts.StartTLS()
	defer ts.Close()

	review := admissionReviewRequest{
		APIVersion: "admission.k8s.io/v1",
		Kind:       json.RawMessage(`{"group":"admission.k8s.io","version":"v1","kind":"AdmissionReview"}`),
		Request: admissionRequest{
			UID: "uid-mig",
			Kind: struct {
				Group   string `json:"group"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
			}{Group: "", Version: "v1", Kind: "Pod"},
			Object: podObj{
				Metadata: struct {
					Name         string `json:"name"`
					Namespace    string `json:"namespace"`
					GenerateName string `json:"generateName"`
				}{Name: "pod-mig", Namespace: "default"},
				Spec: struct {
					NodeSelector map[string]string `json:"nodeSelector"`
					Containers   []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					} `json:"containers"`
				}{
					Containers: []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						} `json:"resources"`
					}{
						{Name: "app", Resources: struct {
							Requests struct {
								CPU    string `json:"cpu"`
								Memory string `json:"memory"`
							} `json:"requests"`
						}{Requests: struct {
							CPU    string `json:"cpu"`
							Memory string `json:"memory"`
						}{CPU: "100m", Memory: "128Mi"}}},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(review)
	roots := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	roots.AddCert(leaf)
	client := ts.Client()
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}

	resp, err := client.Post(ts.URL+"/mutate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var respReview admissionReviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&respReview); err != nil {
		t.Fatal(err)
	}
	if !respReview.Response.Allowed {
		t.Error("expected Allowed=true")
	}
	if len(respReview.Response.Patch) == 0 {
		t.Fatal("expected non-empty patch")
	}

	// Verify the patch uses the migration target hint, not scheduler's node1
	var patches []jsonPatchOp
	if err := json.Unmarshal(respReview.Response.Patch, &patches); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range patches {
		if p.Path == "/spec/nodeSelector" {
			if m, ok := p.Value.(map[string]interface{}); ok {
				if hostname, ok := m["kubernetes.io/hostname"]; ok && hostname == "node-migration-target" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected nodeSelector patch to target 'node-migration-target', got %s", string(respReview.Response.Patch))
	}
}

func TestWebhookMutate_NotPod(t *testing.T) {
	lister := &mockLister{
		pods:  nil,
		nodes: nil,
	}
	sched := NewScheduler(lister, lister, &fakeSizingAndMetrics{}, &fakeSizingAndMetrics{}, nil)
	ws := NewWebhookServer(sched, ":8443", "", "")

	certPEM, keyPEM, _, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ws.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts := httptest.NewUnstartedServer(ws.server.Handler)
	ts.TLS = ws.server.TLSConfig
	ts.StartTLS()
	defer ts.Close()

	review := admissionReviewRequest{
		APIVersion: "admission.k8s.io/v1",
		Kind:       json.RawMessage(`{"group":"admission.k8s.io","version":"v1","kind":"AdmissionReview"}`),
		Request: admissionRequest{
			UID: "uid-4",
			Kind: struct {
				Group   string `json:"group"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
			}{Group: "apps", Version: "v1", Kind: "Deployment"},
		},
	}

	body, _ := json.Marshal(review)
	client := ts.Client()
	resp, err := client.Post(ts.URL+"/mutate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var respReview admissionReviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&respReview); err != nil {
		t.Fatal(err)
	}
	if !respReview.Response.Allowed {
		t.Error("expected Allowed=true for non-Pod resource")
	}
	if len(respReview.Response.Patch) != 0 {
		t.Error("expected empty patch for non-Pod resource")
	}
}
