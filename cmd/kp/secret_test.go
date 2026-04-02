package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ── scanSecretRefs 测试 ───────────────────────────────────────────────────────

func TestScanSecretRefs_FindsSecretKeyRef(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "deployments", "myapp", "wallet-service", "templates")
	os.MkdirAll(tmplDir, 0o755)

	os.WriteFile(filepath.Join(tmplDir, "deployment.yaml"), []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: wallet-service
spec:
  template:
    spec:
      containers:
        - name: wallet-service
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: wallet-service-secret
                  key: DATABASE_URL
`), 0o644)

	refs := scanSecretRefs(dir, "wallet-service-secret")
	if len(refs) == 0 {
		t.Fatal("应该找到 1 个引用，实际为 0")
	}
	if refs[0].Service != "wallet-service" {
		t.Errorf("服务名错误，got %s", refs[0].Service)
	}
	if refs[0].MountType != "env" {
		t.Errorf("挂载类型应为 env，got %s", refs[0].MountType)
	}
}

func TestScanSecretRefs_DetectsVolumeMount(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "deployments", "myapp", "api-service", "templates")
	os.MkdirAll(tmplDir, 0o755)

	os.WriteFile(filepath.Join(tmplDir, "deployment.yaml"), []byte(`
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      volumes:
        - name: secret-vol
          secret:
            secretName: api-secret
      containers:
        - name: api-service
          env:
            - name: DUMMY
              value: "dummy"
              name: api-secret
`), 0o644)

	refs := scanSecretRefs(dir, "api-secret")
	if len(refs) == 0 {
		t.Fatal("应该找到 1 个引用")
	}
	if refs[0].MountType != "volume" {
		t.Errorf("挂载类型应为 volume，got %s", refs[0].MountType)
	}
}

func TestScanSecretRefs_NoMatch(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "deployments", "myapp", "svc", "templates")
	os.MkdirAll(tmplDir, 0o755)

	os.WriteFile(filepath.Join(tmplDir, "deployment.yaml"), []byte(`
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: svc
          env:
            - name: FOO
              value: bar
`), 0o644)

	refs := scanSecretRefs(dir, "other-secret")
	if len(refs) != 0 {
		t.Errorf("不应该找到引用，got %d", len(refs))
	}
}

func TestScanSecretRefs_MultipleServices(t *testing.T) {
	dir := t.TempDir()

	for _, svc := range []string{"svc-a", "svc-b"} {
		tmplDir := filepath.Join(dir, "deployments", "myapp", svc, "templates")
		os.MkdirAll(tmplDir, 0o755)
		os.WriteFile(filepath.Join(tmplDir, "deployment.yaml"), []byte(`
spec:
  template:
    spec:
      containers:
        - env:
            - valueFrom:
                secretKeyRef:
                  name: shared-secret
`), 0o644)
	}

	refs := scanSecretRefs(dir, "shared-secret")
	if len(refs) != 2 {
		t.Errorf("应该找到 2 个服务，got %d", len(refs))
	}
}

// ── extractServiceFromPath 测试 ───────────────────────────────────────────────

func TestExtractServiceFromPath_Deployment(t *testing.T) {
	path := "/home/qc/web3-blitz/deployments/web3-blitz/wallet-service/templates/deployment.yaml"
	svc, kind := extractServiceFromPath(path)
	if svc != "wallet-service" {
		t.Errorf("服务名错误，got %s", svc)
	}
	if kind != "Deployment" {
		t.Errorf("Kind 应为 Deployment，got %s", kind)
	}
}

func TestExtractServiceFromPath_StatefulSet(t *testing.T) {
	path := "/home/qc/web3-blitz/deployments/web3-blitz/postgres/templates/statefulset.yaml"
	svc, kind := extractServiceFromPath(path)
	if svc != "postgres" {
		t.Errorf("服务名错误，got %s", svc)
	}
	if kind != "StatefulSet" {
		t.Errorf("Kind 应为 StatefulSet，got %s", kind)
	}
}

// ── parseCertExpiry 测试 ──────────────────────────────────────────────────────

func TestParseCertExpiry_InvalidPEM(t *testing.T) {
	_, err := parseCertExpiry([]byte("not a pem"))
	if err == nil {
		t.Error("无效 PEM 应该返回 error")
	}
}
