// internal/scheduler/cert.go
package scheduler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// GenerateSelfSignedCert 返回证书、私钥以及 CA 证书的 PEM 编码。
//
// 可选参数 serviceNamespace: webhook 服务所在命名空间（用于 SAN DNS）。
// 不传默认 "kubepivot-system"。
// 证书 SAN 包含 localhost + 127.0.0.1 + <namespace>.svc 泛域名。
func GenerateSelfSignedCert(serviceNamespace ...string) (certPEM, keyPEM, caPEM []byte, err error) {
	ns := "kubepivot-system"
	if len(serviceNamespace) > 0 && serviceNamespace[0] != "" {
		ns = serviceNamespace[0]
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate key: %w", err)
	}

	dnsNames := []string{
		"localhost",
		"kubepivot-webhook",
		"kubepivot-webhook." + ns,
		"kubepivot-webhook." + ns + ".svc",
		"kubepivot-webhook." + ns + ".svc.cluster.local",
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kubepivot-webhook"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              dnsNames,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create cert: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	caPEM = certPEM // 自签证书的 CA 就是它自己
	return certPEM, keyPEM, caPEM, nil
}

// internal/scheduler/cert.go 追加
func LoadCertFromFiles(certFile, keyFile string) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(certFile, keyFile)
}
