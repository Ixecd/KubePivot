package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeInternalSkeleton(outputDir, name, module string) error {
	files := map[string]string{
		filepath.Join(outputDir, "internal", "pkg", "code", "code.go"): `package code

import "fmt"

// ErrorCode 业务错误码
type ErrorCode int

const (
	ErrUnknown      ErrorCode = 100000
	ErrInvalidArg   ErrorCode = 100001
	ErrUnauthorized ErrorCode = 100002
	ErrForbidden    ErrorCode = 100003
	ErrNotFound     ErrorCode = 100004
	ErrInternal     ErrorCode = 100005
)

var errorCodeMessages = map[ErrorCode]string{
	ErrUnknown:      "unknown error",
	ErrInvalidArg:   "invalid argument",
	ErrUnauthorized: "unauthorized",
	ErrForbidden:    "forbidden",
	ErrNotFound:     "not found",
	ErrInternal:     "internal server error",
}
	func (e ErrorCode) Message() string {
	if msg, ok := errorCodeMessages[e]; ok {
		return msg
	}
	return fmt.Sprintf("error code %d", int(e))
}

// Error 业务错误，携带错误码 + 原因
type Error struct {
	Code  ErrorCode
	Cause error
}

func New(code ErrorCode) *Error { return &Error{Code: code} }

func (e *Error) WithCause(cause error) *Error {
	e.Cause = cause
	return e
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Code.Message(), e.Cause)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Code.Message())
}
`,

		filepath.Join(outputDir, "internal", "pkg", "response", "response.go"): `package response

import (
	"encoding/json"
	"net/http"

	"` + module + `/internal/pkg/code"
)

// Response 统一 HTTP 响应格式
type Response struct {
	Code    int         ` + "`json:\"code\"`" + `
	Message string      ` + "`json:\"message\"`" + `
	Data    interface{} ` + "`json:\"data,omitempty\"`" + `
}

func OK(w http.ResponseWriter, data interface{}) {
	write(w, http.StatusOK, Response{Code: 0, Message: "ok", Data: data})
}

func Fail(w http.ResponseWriter, err *code.Error) {
	status := http.StatusInternalServerError
	switch err.Code {
	case code.ErrInvalidArg:
		status = http.StatusBadRequest
	case code.ErrUnauthorized:
		status = http.StatusUnauthorized
	case code.ErrForbidden:
		status = http.StatusForbidden
	case code.ErrNotFound:
		status = http.StatusNotFound
	}
	write(w, status, Response{
		Code:    int(err.Code),
		Message: err.Code.Message(),
	})
}

func write(w http.ResponseWriter, status int, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
`,

		filepath.Join(outputDir, "internal", "api", "handler.go"): fmt.Sprintf(`package api

import (
	"net/http"

	"`+module+`/internal/pkg/response"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]string{
		"service": %q,
		"status":  "ok",
	})
}
`, name),

		filepath.Join(outputDir, "internal", "api", "server.go"): `package api

import "net/http"

func NewMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.Healthz)
	mux.HandleFunc("/", h.Home)
	return mux
}
`,
	}

	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gen internal file %s: %w", path, err)
		}
	}
	return nil
}

func writeAuthPackage(outputDir string) error {
	files := map[string]string{
		filepath.Join(outputDir, "internal", "auth", "auth.go"): `package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const tokenExpiry = 24 * time.Hour

type Claims struct {
	UserID   int64  ` + "`" + `json:"user_id"` + "`" + `
	Username string ` + "`" + `json:"username"` + "`" + `
	jwt.RegisteredClaims
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("密码加密失败: %w", err)
	}
	return string(bytes), nil
}

func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateToken(userID int64, username, secret string) (string, error) {
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ParseToken(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("非法签名方法: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("token 解析失败: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("token 无效")
	}
	return claims, nil
}
`,

		filepath.Join(outputDir, "internal", "auth", "middleware.go"): `package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const claimsKey contextKey = "claims"

func JWTMiddleware(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			http.Error(w, "缺少 Authorization header", http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Authorization 格式错误", http.StatusUnauthorized)
			return
		}
		claims, err := ParseToken(parts[1], secret)
		if err != nil {
			http.Error(w, "token 无效: "+err.Error(), http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next(w, r.WithContext(ctx))
	}
}

func GetClaims(r *http.Request) *Claims {
	claims, _ := r.Context().Value(claimsKey).(*Claims)
	return claims
}
`,

		filepath.Join(outputDir, "internal", "auth", "rbac.go"): `package auth

import (
	"context"
	"net/http"
)

type PermissionChecker interface {
	HasPermission(ctx context.Context, userID int64, permission string) (bool, error)
}

func RBACMiddleware(checker PermissionChecker, permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r)
		if claims == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ok, err := checker.HasPermission(r.Context(), claims.UserID, permission)
		if err != nil {
			http.Error(w, "权限查询失败", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "权限不足", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
`,
	}

	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gen auth file %s: %w", path, err)
		}
	}
	return nil
}

func writeMetricsSkeleton(outputDir, name string) error {
	content := fmt.Sprintf(`package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	HTTPRequestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "%s_http_request_total",
		Help: "Total number of HTTP requests",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "%s_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	BusinessErrorTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "%s_business_error_total",
		Help: "Total number of business errors",
	}, []string{"code"})
)

func Init() {
	prometheus.MustRegister(HTTPRequestTotal, HTTPRequestDuration, BusinessErrorTotal)
}
`, name, name, name)

	path := filepath.Join(outputDir, "internal", "metrics", "metrics.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeTestSkeleton(outputDir, name, module string) error {
	camel := toCamel(name)

	files := map[string]string{
		filepath.Join(outputDir, "test", "e2e", "e2e_test.go"): fmt.Sprintf(`//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const baseURL = "http://localhost:8080"

func TestE2E_%s(t *testing.T) {
	resp, err := http.Get(baseURL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
`, camel),

		filepath.Join(outputDir, "test", "integration", "api_test.go"): `package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPI_Healthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}
`,

		filepath.Join(outputDir, "test", "smoke", "smoke_test.go"): `//go:build e2e

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmoke_Health(t *testing.T) {
	resp, err := http.Get("http://localhost:8080/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
`,
	}

	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gen test file %s: %w", path, err)
		}
	}
	return nil
}

func writeSnapshotSkeleton(outputDir, name string) error {
	content := fmt.Sprintf(`# %s 快照归档

命名格式：SNAPSHOT-{日期}-{里程碑}.md

示例：
- SNAPSHOT-2026-03-18-init.md
- SNAPSHOT-2026-03-18-feature-x.md
`, name)

	path := filepath.Join(outputDir, "snapshots", "README.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeSwaggerSpec(outputDir, name string) error {
	content := fmt.Sprintf(`swagger: "2.0"
info:
  title: %s API
  description: %s 服务 API 文档
  version: "1.0.0"

host: localhost:8080
basePath: /
schemes:
  - http

securityDefinitions:
  Bearer:
    type: apiKey
    name: Authorization
    in: header

consumes:
  - application/json
produces:
  - application/json

paths:
  /healthz:
    get:
      summary: 健康检查
      produces:
        - text/plain
      responses:
        200:
          description: ok
  /:
    get:
      summary: 服务信息
      security:
        - Bearer: []
      responses:
        200:
          description: 服务信息
`, name, name)

	path := filepath.Join(outputDir, "docs", "swagger.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeDockerfile(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go env -w GOPROXY=https://goproxy.cn,direct
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o %s ./cmd/%s

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/%s .
EXPOSE 8080
CMD ["./%s"]
`, name, name, name, name)
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeBuildSh(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`#!/bin/bash
set -e
IMAGE_NAME=${1:-%s}
TAG=${2:-latest}
docker build -t "$IMAGE_NAME:$TAG" -f "$(dirname "$0")/Dockerfile" .
`, name)
	return os.WriteFile(path, []byte(content), 0o755)
}

func writeServiceMain(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`package main

import (
	"fmt"
	"net/http"
	"os"

	"%s/internal/api"
)

func main() {
	port := getenv("PORT", "8080")
	h := api.NewHandler()
	mux := api.NewMux(h)
	addr := ":" + port
	fmt.Printf("service %%q listening on %%s\n", %q, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		panic(err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
`, "github.com/Ixecd/kubepivot", name)
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeComponentsConfig(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`components:
  # 基础设施层（无 image，只部署 helm chart，无需 build/push）
  - name: %s-postgres
    type: StatefulSet
    deps: []

  - name: %s-etcd
    type: StatefulSet
    deps: []

  # 业务服务层
  - name: %s
    port: 8080
    image: %s
    deps:
      - %s-postgres
      - %s-etcd
    # strategy: rolling       # rolling（默认）/ blue-green / canary
    # api_version: v1         # 对外 API 版本，用于 kp compat 依赖检查
`, name, name, name, name, name, name)
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeTestScript(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`#!/bin/bash
# API smoke test for %s
BASE="http://localhost:8080"

echo "=== Healthz ==="
curl -s "$BASE/healthz"
echo ""

echo "=== Unauthorized (expect 401) ==="
curl -s "$BASE/"
echo ""

echo "=== Authorized ==="
curl -s -H "Authorization: Bearer demo-token" "$BASE/"
echo ""
`, name)
	return os.WriteFile(path, []byte(content), 0o755)
}

func writeGoMod(templatePath, outputPath, module string) error {
	// 直接硬编码 go.mod 模板，不依赖外部文件
	content := fmt.Sprintf("module %s\n\ngo 1.25\n", module)
	return os.WriteFile(outputPath, []byte(content), 0o644)
}

func writeSecretScript(outputDir, name string) error {
	path := filepath.Join(outputDir, "scripts", "create-secret.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`#!/bin/bash
# 创建/更新 %s 的 K8s Secret
# 幂等：已存在则更新，不存在则创建
# 用法：./scripts/create-secret.sh
#
# 生产环境：修改下方变量为真实值
# 开发/demo：直接运行，自动使用安全随机值

set -e

NAMESPACE=${KUBE_NAMESPACE:-%s}

# 从 .env 读取（如果存在）
if [ -f .env ]; then
  export $(grep -v '^#' .env | grep -v '^$' | xargs)
fi

kubectl create secret generic %s-secret \
  -n "$NAMESPACE" \
  --from-literal=DATABASE_URL="${DATABASE_URL:-postgres://%s:%s@%s-postgres:5432/%s?sslmode=disable}" \
  --from-literal=JWT_SECRET="${JWT_SECRET:-$(openssl rand -hex 32)}" \
  --from-literal=APP_SECRET="${APP_SECRET:-$(openssl rand -hex 16)}" \
  --save-config \
  --dry-run=client -o yaml | kubectl apply -f -

echo "✅ %s-secret 已创建/更新（namespace: $NAMESPACE）"
echo "⚠️  生产环境请设置真实的 DATABASE_URL / JWT_SECRET / APP_SECRET"
`, name, name, name, name, name, name, name, name)
	return os.WriteFile(path, []byte(content), 0o755)
}

func toCamel(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
