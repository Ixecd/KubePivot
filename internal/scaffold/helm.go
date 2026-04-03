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
	if err := writeControllerChart(deploymentsDir, name); err != nil {
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
              value: pass
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

	return writeFiles(map[string]string{
		filepath.Join(deploymentsDir, name+"-postgres", "Chart.yaml"):  chartYAML,
		filepath.Join(deploymentsDir, name+"-postgres", "values.yaml"): "storage: 1Gi\n",
		filepath.Join(dir, "statefulset.yaml"):                         statefulset,
		filepath.Join(dir, "service.yaml"):                             svc,
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

	return writeFiles(map[string]string{
		filepath.Join(deploymentsDir, name+"-etcd", "Chart.yaml"):  chartYAML,
		filepath.Join(deploymentsDir, name+"-etcd", "values.yaml"): "storage: 1Gi\n",
		filepath.Join(dir, "statefulset.yaml"):                     statefulset,
		filepath.Join(dir, "service.yaml"):                         svc,
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

	deployment := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s
    version: {{ .Values.image.tag | default .Chart.AppVersion }}
spec:
  replicas: {{ .Values.replicaCount }}
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
        version: {{ .Values.image.tag | default .Chart.AppVersion }}
    spec:
      serviceAccountName: %s
      initContainers:
        - name: wait-postgres
          image: busybox:1.35
          command: ['sh', '-c', 'until nc -z %s-postgres 5432; do echo waiting for postgres; sleep 2; done']
        - name: wait-etcd
          image: busybox:1.35
          command: ['sh', '-c', 'until nc -z %s-etcd 2379; do echo waiting for etcd; sleep 2; done']
      containers:
        - name: %s
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
          {{- with .Values.env }}
          env:
            {{- toYaml . | nindent 12 }}
          {{- end }}
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
`, name, name, name, name, name, name, name, name)

	svc := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: {{ .Release.Namespace }}
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
	vb.WriteString("  pullPolicy: IfNotPresent\n")
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
	vb.WriteString("  readOnlyRootFilesystem: false  # 改为 true 可加强安全，但需确保服务不写本地文件\n\n")
	vb.WriteString("env:\n")
	vb.WriteString("  - name: DATABASE_URL\n")
	vb.WriteString("    value: \"postgres://user:pass@" + name + "-postgres:5432/" + name + "?sslmode=disable&search_path=public\"\n")
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
		filepath.Join(dir, "virtualservice-preview.yaml"):  vsPreview,
	})
}

// ── controller 独立 chart ─────────────────────────────────────────────────────

func writeControllerChart(deploymentsDir, name string) error {
	dir := filepath.Join(deploymentsDir, name+"-controller", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s-controller
description: A2 Reconciliation Controller for %s
type: application
version: 0.1.0
appVersion: "latest"
dependencies: []
`, name, name)

	var vb strings.Builder
	vb.WriteString("# 配置好镜像后将 enabled 改为 true，再重新 dtk deploy\n")
	vb.WriteString("enabled: false\n\n")
	vb.WriteString("image:\n")
	vb.WriteString("  # TODO: 替换为你构建的 dev-toolkit-controller 镜像（需包含 dtk + kubectl + helm）\n")
	vb.WriteString("  repository: your-registry/dev-toolkit-controller\n")
	vb.WriteString("  pullPolicy: IfNotPresent\n")
	vb.WriteString("  tag: latest\n\n")
	vb.WriteString("# 由 dtk deploy 通过 --set-file 自动注入 configs/resources.yaml\n")
	vb.WriteString("resourcesConfig: \"\"\n")

	rbac := `{{- if .Values.enabled }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kubepivot-controller
  namespace: {{ .Release.Namespace }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubepivot-controller
rules:
  # 核心资源：deployments/statefulsets/pods 自愈用
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "replicasets"]
    verbs: ["get", "list", "watch", "update", "patch"]
  - apiGroups: [""]
    resources: ["pods", "services", "endpoints", "persistentvolumeclaims"]
    verbs: ["get", "list", "watch"]
  # Leader Election：leases 读写权
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  # helm rollback 用
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "watch"]
  # 事件记录
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubepivot-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kubepivot-controller
subjects:
  - kind: ServiceAccount
    name: kubepivot-controller
    namespace: {{ .Release.Namespace }}
{{- end }}
`

	configmap := `{{- if .Values.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: dev-toolkit-controller-resources
  namespace: {{ .Release.Namespace }}
data:
  resources.yaml: |
{{ .Values.resourcesConfig | indent 4 }}
{{- end }}
`

	deployment := fmt.Sprintf(`{{- if .Values.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dev-toolkit-controller
  namespace: {{ .Release.Namespace }}
  labels:
    app: dev-toolkit-controller
spec:
  replicas: 1
  selector:
    matchLabels:
      app: dev-toolkit-controller
  template:
    metadata:
      labels:
        app: dev-toolkit-controller
    spec:
      serviceAccountName: dev-toolkit-controller
      containers:
        - name: dev-toolkit-controller
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          command: ["dtk", "controller", "start"]
          env:
            - name: PROJECT_NAME
              value: "%s"
            - name: KUBE_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: ETCD_ENDPOINTS
              value: "%s-etcd:2379"
            - name: RESOURCES_CONFIG
              value: "/etc/controller/resources.yaml"
          volumeMounts:
            - name: resources-config
              mountPath: /etc/controller
      volumes:
        - name: resources-config
          configMap:
            name: dev-toolkit-controller-resources
{{- end }}
`, name, name)

	return writeFiles(map[string]string{
		filepath.Join(deploymentsDir, name+"-controller", "Chart.yaml"):  chartYAML,
		filepath.Join(deploymentsDir, name+"-controller", "values.yaml"): vb.String(),
		filepath.Join(dir, "rbac.yaml"):                                  rbac,
		filepath.Join(dir, "configmap.yaml"):                             configmap,
		filepath.Join(dir, "deployment.yaml"):                            deployment,
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
