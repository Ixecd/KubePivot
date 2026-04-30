package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeHelmTemplateSkeleton 生成多服务独立 helm chart 目录结构。
//
// 目录结构：
//
//	deployments/{name}/
//	├── {name}-postgres/     # 独立 chart（StatefulSet）
//	├── {name}-etcd/         # 独立 chart（StatefulSet）
//	├── {name}/              # 业务服务 chart（含 initContainers）
//	└── {name}-controller/   # controller chart
//
// 每个服务有独立的 helm release：{project}-{service}
func writeHelmTemplateSkeleton(outputDir, name string) error {
	deploymentsDir := filepath.Join(outputDir, "deployments", name)

	// 清理旧模板目录里的 controller 文件（已移到独立 chart）
	oldTemplatesDir := filepath.Join(deploymentsDir, "templates")
	filesToRemove := []string{
		"controller-deployment.yaml",
		"controller-rbac.yaml",
		"resources-configmap.yaml",
	}
	for _, f := range filesToRemove {
		os.Remove(filepath.Join(oldTemplatesDir, f)) // 忽略不存在的错误
	}

	if err := writePostgresChart(deploymentsDir, name); err != nil {
		return err
	}
	if err := writeEtcdChart(deploymentsDir, name); err != nil {
		return err
	}
	if err := writeServiceChart(deploymentsDir, name); err != nil {
		return err
	}
	return nil
}

// ── postgres 独立 chart ───────────────────────────────────────────────────────

func writePostgresChart(deploymentsDir, name string) error {
	dir := filepath.Join(deploymentsDir, name+"-postgres", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s-postgres
description: PostgreSQL StatefulSet for %s
type: application
version: 0.1.0
appVersion: "16-alpine"
dependencies: []
`, name, name)

	statefulset := fmt.Sprintf(`apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: %s-postgres
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-postgres
spec:
  serviceName: %s-postgres
  replicas: 1
  selector:
    matchLabels:
      app: %s-postgres
  template:
    metadata:
      labels:
        app: %s-postgres
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_USER
              value: user
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{ .Release.Name }}-postgres-auth
                  key: password
            - name: POSTGRES_DB
              value: %s
            - name: PGDATA
              value: /var/lib/postgresql/data/pgdata
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "user", "-d", "%s"]
            initialDelaySeconds: 5
            periodSeconds: 5
          livenessProbe:
            exec:
              command: ["pg_isready", "-U", "user", "-d", "%s"]
            initialDelaySeconds: 15
            periodSeconds: 10
          volumeMounts:
            - name: postgres-data
              mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
    - metadata:
        name: postgres-data
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: {{ .Values.storage }}
`, name, name, name, name, name, name, name, name)

	svc := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s-postgres
  namespace: {{ .Release.Namespace }}
spec:
  selector:
    app: %s-postgres
  ports:
    - port: 5432
      targetPort: 5432
`, name, name)

	postgresValuesYAML := "storage: 1Gi\npostgres:\n  password: pass  # 生产环境请务必修改\n"

	postgresSecretYAML := `apiVersion: v1
kind: Secret
metadata:
  name: {{ .Release.Name }}-postgres-auth
  namespace: {{ .Release.Namespace }}
type: Opaque
data:
  password: {{ .Values.postgres.password | b64enc | quote }}
`

	netpol := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s-postgres
  namespace: {{ .Release.Namespace }}
spec:
  podSelector:
    matchLabels:
      app: %s-postgres
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: %s
      ports:
        - port: 5432
          protocol: TCP
`, name, name, name)

	return writeFiles(map[string]string{
		filepath.Join(deploymentsDir, name+"-postgres", "Chart.yaml"):  chartYAML,
		filepath.Join(deploymentsDir, name+"-postgres", "values.yaml"): postgresValuesYAML,
		filepath.Join(dir, "statefulset.yaml"):                         statefulset,
		filepath.Join(dir, "service.yaml"):                             svc,
		filepath.Join(dir, "secret.yaml"):                              postgresSecretYAML,
		filepath.Join(dir, "netpol.yaml"):                              netpol,
	})
}

// ── etcd 独立 chart（StatefulSet + PVC）────────────────────────────────────────
func writeEtcdChart(deploymentsDir, name string) error {
	dir := filepath.Join(deploymentsDir, name+"-etcd", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s-etcd
description: etcd StatefulSet for %s
type: application
version: 0.1.0
appVersion: "v3.5.14"
dependencies: []
`, name, name)

	statefulset := fmt.Sprintf(`apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: %s-etcd
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-etcd
spec:
  serviceName: %s-etcd
  replicas: 1
  selector:
    matchLabels:
      app: %s-etcd
  template:
    metadata:
      labels:
        app: %s-etcd
    spec:
      containers:
        - name: etcd
          image: quay.io/coreos/etcd:v3.5.14
          command:
            - etcd
            - --listen-client-urls=http://0.0.0.0:2379
            - --advertise-client-urls=http://%s-etcd:2379
            - --listen-peer-urls=http://0.0.0.0:2380
            - --initial-advertise-peer-urls=http://0.0.0.0:2380
            - --initial-cluster=default=http://0.0.0.0:2380
            - --data-dir=/etcd-data
          ports:
            - name: client
              containerPort: 2379
            - name: peer
              containerPort: 2380
          readinessProbe:
            httpGet:
              path: /health
              port: 2379
            initialDelaySeconds: 5
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: /health
              port: 2379
            initialDelaySeconds: 10
            periodSeconds: 10
          volumeMounts:
            - name: etcd-data
              mountPath: /etcd-data
  volumeClaimTemplates:
    - metadata:
        name: etcd-data
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: {{ .Values.storage }}
`, name, name, name, name, name, name)

	svc := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s-etcd
  namespace: {{ .Release.Namespace }}
spec:
  selector:
    app: %s-etcd
  ports:
    - name: client
      port: 2379
      targetPort: 2379
    - name: peer
      port: 2380
      targetPort: 2380
`, name, name)

	netpol := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s-etcd
  namespace: {{ .Release.Namespace }}
spec:
  podSelector:
    matchLabels:
      app: %s-etcd
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: %s
      ports:
        - port: 2379
          protocol: TCP
`, name, name, name)

	return writeFiles(map[string]string{
		filepath.Join(deploymentsDir, name+"-etcd", "Chart.yaml"):  chartYAML,
		filepath.Join(deploymentsDir, name+"-etcd", "values.yaml"): "storage: 1Gi\n",
		filepath.Join(dir, "statefulset.yaml"):                     statefulset,
		filepath.Join(dir, "service.yaml"):                         svc,
		filepath.Join(dir, "netpol.yaml"):                          netpol,
	})
}

// ── 业务服务 chart ────────────────────────────────────────────────────────────

func writeServiceChart(deploymentsDir, name string) error {
	dir := filepath.Join(deploymentsDir, name, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s
description: %s business service
type: application
version: 0.1.0
appVersion: "0.1.0"
dependencies: []
`, name, name)

	notes := fmt.Sprintf(`✅ {{ .Release.Name }} 部署成功！

命名空间: {{ .Release.Namespace }}
版本:     {{ .Values.image.tag | default .Chart.AppVersion }}
时间:     {{ now | date "2006-01-02 15:04:05" }}

快速访问:
  kubectl get pods -n {{ .Release.Namespace }}
  kubectl logs -n {{ .Release.Namespace }} deployment/%s
  kubectl port-forward -n {{ .Release.Namespace }} deployment/%s {{ .Values.service.port }}:{{ .Values.service.port }}
`, name, name)

	deployment := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
  namespace: {{ .Release.Namespace }}
  labels:
    app: {{ .Release.Name }}
    kubepivot.io/name: {{ .Chart.Name }}
    kubepivot.io/instance: {{ .Release.Name }}
    kubepivot.io/version: {{ .Chart.AppVersion }}
    app.kubernetes.io/managed-by: kp
    version: {{ .Values.image.tag | default .Chart.AppVersion }}
spec:
  replicas: {{ .Values.replicaCount }}
  selector:
    matchLabels:
      app: {{ .Release.Name }}
  template:
    metadata:
      labels:
        app: {{ .Release.Name }}
        version: {{ .Values.image.tag | default .Chart.AppVersion }}
    spec:
      serviceAccountName: {{ .Release.Name }}
      automountServiceAccountToken: {{ .Values.rbac.create }}
      initContainers:
        - name: wait-postgres
          image: busybox:1.35
          command: ['sh', '-c', 'count=0; until nc -z {{ .Release.Name }}-postgres 5432; do count=$((count+1)); if [ $count -gt 30 ]; then echo "timeout waiting for postgres"; exit 1; fi; echo waiting for postgres; sleep 2; done']
        - name: wait-etcd
          image: busybox:1.35
          command: ['sh', '-c', 'count=0; until nc -z {{ .Release.Name }}-etcd 2379; do count=$((count+1)); if [ $count -gt 60 ]; then echo "timeout waiting for etcd"; exit 1; fi; echo waiting for etcd; sleep 2; done']
      containers:
        - name: {{ .Chart.Name }}
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          ports:
            - name: http
              containerPort: {{ .Values.service.port }}
              protocol: TCP
          securityContext:
            runAsNonRoot: true
            runAsUser: 1000
            readOnlyRootFilesystem: {{ .Values.securityContext.readOnlyRootFilesystem }}
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          env:
            - name: DB_USER
              value: {{ .Values.db.user | quote }}
            - name: DB_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{ .Release.Name }}-postgres-auth
                  key: password
            - name: DB_HOST
              value: {{ .Release.Name }}-postgres
            - name: DB_NAME
              value: {{ .Release.Name }}
            - name: DB_SSLMODE
              value: {{ .Values.db.sslmode | quote }}
            - name: ETCD_ENDPOINTS
              value: {{ .Release.Name }}-etcd:2379
          livenessProbe:
            httpGet:
              path: /healthz
              port: {{ .Values.service.port }}
            initialDelaySeconds: 10
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /healthz
              port: {{ .Values.service.port }}
            initialDelaySeconds: 5
            periodSeconds: 5
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
`

	svc := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: {{ .Release.Namespace }}
  labels:
    kubepivot.io/name: {{ .Chart.Name }}
    kubepivot.io/instance: {{ .Release.Name }}
    kubepivot.io/version: {{ .Chart.AppVersion }}
    app.kubernetes.io/managed-by: kp
spec:
  type: {{ .Values.service.type }}
  selector:
    app: %s
  ports:
    - port: {{ .Values.service.port }}
      targetPort: {{ .Values.service.port }}
      protocol: TCP
`, name, name)

	sa := fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: {{ .Release.Namespace }}
  labels:
    kubepivot.io/name: {{ .Chart.Name }}
    kubepivot.io/instance: {{ .Release.Name }}
    kubepivot.io/version: {{ .Chart.AppVersion }}
    app.kubernetes.io/managed-by: kp
`, name)

	networkPolicy := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s
  namespace: {{ .Release.Namespace }}
spec:
  podSelector:
    matchLabels:
      app: %s
  policyTypes:
    - Ingress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ .Release.Namespace }}
      ports:
        - port: {{ .Values.service.port }}
          protocol: TCP
`, name, name)

	roleYAML := fmt.Sprintf(`# 业务服务默认不需要访问 K8s API，因此不绑定任何 RBAC 规则。
# 如果你的服务需要访问 ConfigMap、Secret 或其他 K8s 资源，
# 请将 values.yaml 中的 rbac.create 设为 true，并取消下面规则的注释。
#
# 示例：读取当前 namespace 的 ConfigMap
# apiVersion: rbac.authorization.k8s.io/v1
# kind: Role
# metadata:
#   name: %s-reader
#   namespace: {{ .Release.Namespace }}
# rules:
#   - apiGroups: [""]
#     resources: ["configmaps"]
#     verbs: ["get", "list"]
`, name)

	roleBindingYAML := `{{- if .Values.rbac.create -}}
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ .Release.Name }}-rb
  namespace: {{ .Release.Namespace }}
  labels:
    app: {{ .Release.Name }}
    kubepivot.io/name: {{ .Chart.Name }}
    kubepivot.io/instance: {{ .Release.Name }}
    app.kubernetes.io/managed-by: kp
subjects:
  - kind: ServiceAccount
    name: {{ .Release.Name }}
    namespace: {{ .Release.Namespace }}
roleRef:
  kind: Role
  name: {{ .Release.Name }}-reader
  apiGroup: rbac.authorization.k8s.io
{{- end -}}
`

// 	secretYAML := `apiVersion: v1
// kind: Secret
// metadata:
//   name: {{ .Release.Name }}-postgres-auth
//   namespace: {{ .Release.Namespace }}
//   labels:
//     kubepivot.io/name: {{ .Chart.Name }}
//     kubepivot.io/instance: {{ .Release.Name }}
//     app.kubernetes.io/managed-by: kp
// type: Opaque
// data:
//   password: {{ .Values.postgres.password | b64enc | quote }}`


	vsPreview := fmt.Sprintf(`# virtualservice-preview.yaml
# 仅在 strategy: blue-green 时使用，kp deploy --preview 会自动生成填充版本
# 需要 Istio 已安装：kubectl get crd virtualservices.networking.istio.io
#
# 手动 apply 后，用 x-kp-preview: <slot> header 将流量路由到非活跃 slot：
#   curl -H "x-kp-preview: green" http://%s/healthz
#
# apiVersion: networking.istio.io/v1beta1
# kind: VirtualService
# metadata:
#   name: %s-preview
#   namespace: {{ .Release.Namespace }}
# spec:
#   hosts:
#   - %s
#   http:
#   - match:
#     - headers:
#         x-kp-preview:
#           exact: "green"
#     route:
#     - destination:
#         host: %s-green
#         port:
#           number: {{ .Values.service.port }}
#   - route:
#     - destination:
#         host: %s
#         port:
#           number: {{ .Values.service.port }}
#       weight: 100
`, name, name, name, name, name)

	var vb strings.Builder
	vb.WriteString("replicaCount: 1\n\n")
	vb.WriteString("image:\n")
	vb.WriteString("  repository: qingchun22/" + name + "-arm64\n")
	vb.WriteString("  pullPolicy: Always\n")
	vb.WriteString("  tag: \"\"\n\n")
	vb.WriteString("service:\n")
	vb.WriteString("  type: ClusterIP\n")
	vb.WriteString("  port: 8080\n\n")
	vb.WriteString("resources:\n")
	vb.WriteString("  requests:\n")
	vb.WriteString("    cpu: 100m\n")
	vb.WriteString("    memory: 128Mi\n")
	vb.WriteString("  limits:\n")
	vb.WriteString("    cpu: 500m\n")
	vb.WriteString("    memory: 512Mi\n\n")
	vb.WriteString("securityContext:\n")
	vb.WriteString("  readOnlyRootFilesystem: true  # 改为 true 可加强安全，但需确保服务不写本地文件\n\n")
	vb.WriteString("rbac:\n")
	vb.WriteString("  create: false\n")
	vb.WriteString("  roles:\n")
	vb.WriteString("    # - name: config-reader\n")
	vb.WriteString("    #   apiGroups: [\"\"]\n")
	vb.WriteString("    #   resources: [\"configmaps\"]\n")
	vb.WriteString("    #   verbs: [\"get\", \"list\"]\n")
	vb.WriteString("\n")
	vb.WriteString("db:\n")
	vb.WriteString("  user: \"user\"\n")
	vb.WriteString("  sslmode: \"disable\"  # 生产环境建议 require\n")
	vb.WriteString("env:\n")
	vb.WriteString("  - name: ETCD_ENDPOINTS\n")
	vb.WriteString("    value: \"" + name + "-etcd:2379\"\n")

	return writeFiles(map[string]string{
		filepath.Join(deploymentsDir, name, "Chart.yaml"):  chartYAML,
		filepath.Join(deploymentsDir, name, "values.yaml"): vb.String(),
		filepath.Join(dir, "NOTES.txt"):                    notes,
		filepath.Join(dir, "deployment.yaml"):              deployment,
		filepath.Join(dir, "service.yaml"):                 svc,
		filepath.Join(dir, "serviceaccount.yaml"):          sa,
		filepath.Join(dir, "networkpolicy.yaml"):           networkPolicy,
		filepath.Join(dir, "role.yaml"):                    roleYAML,
		filepath.Join(dir, "rolebinding.yaml"):             roleBindingYAML,
		filepath.Join(dir, "virtualservice-preview.yaml"):  vsPreview,
	})
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

func writeFiles(files map[string]string) error {
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", path, err)
		}
	}
	return nil
}
