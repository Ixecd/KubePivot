package etcdmanager

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ixecd/kubepivot/internal/executor"
)

// CertConfig 证书生成配置。
type CertConfig struct {
	CertDir  string        // 证书输出目录，默认 /data/etcd/certs
	Org      string        // 组织名，默认 "KubePivot"
	ValidFor time.Duration // 有效期，默认 10 年
}

// DefaultCertConfig 返回默认证书配置。
func DefaultCertConfig() CertConfig {
	return CertConfig{
		CertDir:  "/data/etcd/certs",
		Org:      "KubePivot",
		ValidFor: 10 * 365 * 24 * time.Hour,
	}
}

// EnsureCerts 确保 etcd TLS 证书就绪。
//
// CA 分发策略（方案 B — Controller `install` 预生成 + K8s Secret 共享）：
//
//	kp controller install → CreateCertsSecret() 生成 CA → 写入 Secret
//	Pod-0/1/2 启动 → EnsureCerts() 从 Secret 读 CA → 生成各自的 server/peer 证书
//
// 全集群共用同一 CA，Pod 只读不写，最小权限原则。
func EnsureCerts(ctx context.Context, cfg CertConfig, podName, podIP, namespace string) error {
	// 统一从 Secret 读 CA，所有 Pod 只读不写
	caCert, caKey, err := readCAFromSecret(ctx, namespace)
	if err != nil {
		return fmt.Errorf("read CA from Secret: %w", err)
	}

	// 写 CA 到本地（所有节点都需要）
	if err := writeLocalCert(cfg.CertDir, "ca.pem", caCert); err != nil {
		return err
	}

	// 生成当前节点的 server + peer 证书（用共享 CA 签发）
	if err := generateNodeCerts(cfg, caCert, caKey, podName, podIP, namespace); err != nil {
		return fmt.Errorf("generate node certs: %w", err)
	}

	return nil
}

// ── Secret 读写 ──────────────────────────────────────────────────────────────

const certsSecretName = "kubepivot-etcd-certs"

// writeCertsToSecret 将 CA 证书写入 K8s Secret（幂等：已存在则更新）。
func writeCertsToSecret(ctx context.Context, caCert, caKey []byte, namespace string) error {
	tmpDir, err := os.MkdirTemp("", "kubepivot-certs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	caFile := filepath.Join(tmpDir, "ca.pem")
	caKeyFile := filepath.Join(tmpDir, "ca-key.pem")
	os.WriteFile(caFile, caCert, 0644)
	os.WriteFile(caKeyFile, caKey, 0600)

	exec := executor.GetExecutor()
	_, err = exec.Kubectl(ctx, "",
		"-n", namespace,
		"create", "secret", "generic", certsSecretName,
		"--from-file=ca.pem="+caFile,
		"--from-file=ca-key.pem="+caKeyFile,
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		return fmt.Errorf("generate Secret YAML: %w", err)
	}
	// 用 kubectl apply pipeline 写入（幂等）
	out, err := exec.Kubectl(ctx, "",
		"-n", namespace,
		"create", "secret", "generic", certsSecretName,
		"--from-file=ca.pem="+caFile,
		"--from-file=ca-key.pem="+caKeyFile,
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		return err
	}

	// kubectl apply -f - via CmdKubectl (supports stdin)
	cmd := exec.CmdKubectl(ctx, "", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(string(out))
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply Secret: %w", err)
	}
	return nil
}

func CreateCertsSecret(ctx context.Context, cfg CertConfig, namespace string, forceRotate bool) error {
	// 幂等：如果 Secret 已存在且未强制轮转，跳过
	if !forceRotate {
		if _, _, err := readCAFromSecret(ctx, namespace); err == nil {
			return nil // CA 已存在，跳过生成
		}
	}
	caCert, caKey, err := generateCA(cfg)
	if err != nil {
		return fmt.Errorf("generate CA: %w", err)
	}
	return writeCertsToSecret(ctx, caCert, caKey, namespace)
}

// readCAFromSecret 从 K8s Secret 读取 CA 证书。
func readCAFromSecret(ctx context.Context, namespace string) (caCert, caKey []byte, err error) {
	exec := executor.GetExecutor()
	certOut, err := exec.Kubectl(ctx, "",
		"-n", namespace,
		"get", "secret", certsSecretName,
		"-o", "jsonpath={.data.ca\\.pem}",
	)
	if err != nil {
		return nil, nil, fmt.Errorf("get CA cert from Secret: %w", err)
	}
	keyOut, err := exec.Kubectl(ctx, "",
		"-n", namespace,
		"get", "secret", certsSecretName,
		"-o", "jsonpath={.data.ca-key\\.pem}",
	)
	if err != nil {
		return nil, nil, fmt.Errorf("get CA key from Secret: %w", err)
	}

	caCert, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(certOut)))
	if err != nil {
		return nil, nil, err
	}
	caKey, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyOut)))
	if err != nil {
		return nil, nil, err
	}
	return caCert, caKey, nil
}

// ── 证书生成 ────────────────────────────────────────────────────────────────

// generateCA 生成自签 CA 证书。
func generateCA(cfg CertConfig) ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{cfg.Org},
			CommonName:   "KubePivot etcd CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(cfg.ValidFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return certPEM, keyPEM, nil
}

// generateNodeCerts 用共享 CA 为当前节点签发 server + peer 证书。
func generateNodeCerts(cfg CertConfig, caCertPEM, caKeyPEM []byte, podName, podIP, namespace string) error {
	caCert, caKey, err := decodeCA(caCertPEM, caKeyPEM)
	if err != nil {
		return err
	}

	// Server 证书（client→server）
	serverSANs := []string{
		"localhost",
		podName,
		fmt.Sprintf("%s.%s", podName, podDNSName(namespace)),
	}
	serverIPs := []net.IP{net.ParseIP("127.0.0.1")}
	if ip := net.ParseIP(podIP); ip != nil {
		serverIPs = append(serverIPs, ip)
		serverSANs = append(serverSANs, podIP)
	}

	serverCert, serverKey, err := genCert(caCert, caKey, cfg, serverSANs, serverIPs)
	if err != nil {
		return fmt.Errorf("server cert: %w", err)
	}
	writeLocalCert(cfg.CertDir, "server.pem", serverCert)
	writeLocalKey(cfg.CertDir, "server-key.pem", serverKey)

	// Peer 证书（peer→peer）
	peerSANs := []string{
		podName,
		fmt.Sprintf("%s.%s", podName, podDNSName(namespace)),
	}
	peerIPs := []net.IP{}
	if ip := net.ParseIP(podIP); ip != nil {
		peerIPs = append(peerIPs, ip)
		peerSANs = append(peerSANs, podIP)
	}

	peerCert, peerKey, err := genCert(caCert, caKey, cfg, peerSANs, peerIPs)
	if err != nil {
		return fmt.Errorf("peer cert: %w", err)
	}
	writeLocalCert(cfg.CertDir, "peer.pem", peerCert)
	writeLocalKey(cfg.CertDir, "peer-key.pem", peerKey)

	return nil
}

func genCert(caCert *x509.Certificate, caKey *rsa.PrivateKey, cfg CertConfig, dnsNames []string, ips []net.IP) ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			Organization: []string{cfg.Org},
			CommonName:   dnsNames[0],
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(cfg.ValidFor),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:    dnsNames,
		IPAddresses: ips,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return certPEM, keyPEM, nil
}

func decodeCA(certPEM, keyPEM []byte) (*x509.Certificate, *rsa.PrivateKey, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("decode CA key PEM")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return caCert, caKey, nil
}

// ── 文件写入 ────────────────────────────────────────────────────────────────

func writeLocalCert(dir, name string, data []byte) error {
	return os.WriteFile(filepath.Join(dir, name), data, 0644)
}

func writeLocalKey(dir, name string, data []byte) error {
	return os.WriteFile(filepath.Join(dir, name), data, 0600)
}

// ── 工具 ────────────────────────────────────────────────────────────────────

func podDNSName(namespace string) string {
	return fmt.Sprintf("kubepivot-controller.%s.svc.cluster.local", namespace)
}
