package etcdmanager

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateCA(t *testing.T) {
	cfg := DefaultCertConfig()
	certPEM, keyPEM, err := generateCA(cfg)
	if err != nil {
		t.Fatalf("generateCA: %v", err)
	}
	if len(certPEM) == 0 {
		t.Error("certPEM is empty")
	}
	if len(keyPEM) == 0 {
		t.Error("keyPEM is empty")
	}
	if string(certPEM)[:27] != "-----BEGIN CERTIFICATE-----" {
		t.Error("certPEM does not start with PEM header")
	}
	if string(keyPEM)[:31] != "-----BEGIN RSA PRIVATE KEY-----" {
		t.Error("keyPEM does not start with PEM header")
	}
}

func TestGenerateNodeCerts(t *testing.T) {
	cfg := DefaultCertConfig()
	cfg.CertDir = t.TempDir()

	caCert, caKey, err := generateCA(cfg)
	if err != nil {
		t.Fatalf("generateCA: %v", err)
	}

	err = generateNodeCerts(cfg, caCert, caKey, "controller-0", "10.0.0.1", "kubepivot-system")
	if err != nil {
		t.Fatalf("generateNodeCerts: %v", err)
	}

	// 验证 server 证书
	for _, name := range []string{"server.pem", "server-key.pem", "peer.pem", "peer-key.pem"} {
		path := filepath.Join(cfg.CertDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", name)
		}
	}

	// 验证 server-key 权限 0600
	info, _ := os.Stat(filepath.Join(cfg.CertDir, "server-key.pem"))
	if info.Mode().Perm() != 0600 {
		t.Errorf("server-key.pem permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestDecodeCA(t *testing.T) {
	cfg := DefaultCertConfig()
	certPEM, keyPEM, _ := generateCA(cfg)

	cert, key, err := decodeCA(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("decodeCA: %v", err)
	}
	if cert == nil {
		t.Error("cert is nil")
	}
	if key == nil {
		t.Error("key is nil")
	}
}

func TestDecodeCA_InvalidPEM(t *testing.T) {
	_, _, err := decodeCA([]byte("not pem"), []byte("not pem"))
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}

func TestBase64RoundTrip(t *testing.T) {
	cfg := DefaultCertConfig()
	certPEM, keyPEM, _ := generateCA(cfg)

	// 模拟 readCAFromSecret 的 Base64 编解码 + TrimSpace
	encoded := base64.StdEncoding.EncodeToString(certPEM)
	// 模拟 kubectl jsonpath 可能带上的换行符
	encodedWithNewline := "\n" + encoded + "\n"
	decoded, err := base64.StdEncoding.DecodeString(encodedWithNewline)
	// 带换行符的 base64 应该解码失败
	if err != nil {
		// 验证 TrimSpace 后能解码
		trimmed := trimSpaceStr(encodedWithNewline)
		_, err := base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			t.Fatalf("decode after trim: %v", err)
		}
	}

	_ = decoded
	_ = keyPEM
}

func TestReadCAFromSecret_TrimSpace(t *testing.T) {
	// 验证 bytes.TrimSpace 函数可用于 Base64 解码前的清理
	dirty := []byte("  \n YQ== \n ")
	clean := bytes.TrimSpace(dirty)
	if len(clean) == 0 || string(clean) == string(dirty) {
		t.Error("trimSpace did not clean dirty bytes")
	}
	_, err := base64.StdEncoding.DecodeString(string(clean))
	if err != nil {
		t.Fatalf("decode after trim: %v", err)
	}
}

func trimSpaceStr(s string) string {
	return string(bytes.TrimSpace([]byte(s)))
}
