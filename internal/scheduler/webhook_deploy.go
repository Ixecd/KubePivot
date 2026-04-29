// internal/scheduler/webhook_deploy.go
package scheduler

import (
	"encoding/base64"
	"fmt"
)

// WebhookDeployConfig 包含部署 MutatingWebhookConfiguration 所需的所有参数。
type WebhookDeployConfig struct {
	// ServiceName 是 Webhook 服务的 DNS 名称。
	// 在集群内通常是 <service-name>.<namespace>.svc
	ServiceName string
	// ServiceNamespace 是 Webhook 服务所在的命名空间。
	ServiceNamespace string
	// ServicePort 是 Webhook 服务的端口。
	ServicePort int
	// CAPEM 是 CA 证书的 PEM 编码，用于 caBundle 字段。
	CAPEM []byte
}

// GenerateWebhookYAML 生成 MutatingWebhookConfiguration YAML。
func GenerateWebhookYAML(cfg WebhookDeployConfig) (string, error) {
	if cfg.ServiceName == "" {
		return "", fmt.Errorf("ServiceName is required")
	}
	if cfg.ServicePort == 0 {
		cfg.ServicePort = 443
	}
	if len(cfg.CAPEM) == 0 {
		return "", fmt.Errorf("CAPEM is required")
	}

	caBundle := base64.StdEncoding.EncodeToString(cfg.CAPEM)
	servicePath := "/mutate"
	if cfg.ServiceNamespace != "" {
		// 集群内服务 URL
		cfg.ServiceName = cfg.ServiceName + "." + cfg.ServiceNamespace + ".svc"
	}

	yaml := fmt.Sprintf(`apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: kubepivot-scheduler-webhook
webhooks:
  - name: scheduler.kubepivot.io
    clientConfig:
      service:
        name: %s
        namespace: %s
        path: %s
        port: %d
      caBundle: %s
    rules:
      - operations: ["CREATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["pods"]
    failurePolicy: Ignore
    sideEffects: None
    admissionReviewVersions: ["v1"]
`, cfg.ServiceName, cfg.ServiceNamespace, servicePath, cfg.ServicePort, caBundle)

	return yaml, nil
}

// GenerateCertSecretYAML 生成包含 TLS 证书的 Secret YAML。
func GenerateCertSecretYAML(certPEM, keyPEM []byte, namespace string) (string, error) {
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return "", fmt.Errorf("certPEM and keyPEM are required")
	}
	if namespace == "" {
		namespace = "kubepivot-system"
	}

	certB64 := base64.StdEncoding.EncodeToString(certPEM)
	keyB64 := base64.StdEncoding.EncodeToString(keyPEM)

	yaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kubepivot-webhook-tls
  namespace: %s
type: kubernetes.io/tls
data:
  tls.crt: %s
  tls.key: %s
`, namespace, certB64, keyB64)

	return yaml, nil
}
