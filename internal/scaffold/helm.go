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
//	├── {name}-etcd/         # 独立 chart（Deployment）
//	├── {name}/              # 业务服务 chart（含 initContainers）
//	└── {name}-controller/   # controller chart
//
// 每个服务有独立的 helm release：{project}-{service}
func writeHelmTemplateSkeleton(outputDir, name string) error {
	deploymentsDir := filepath.Join(outputDir, "deployments", name)

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

// ── etcd 独立 chart ───────────────────────────────────────────────────────────

func writeEtcdChart(deploymentsDir, name string) error {
	dir := filepath.Join(deploymentsDir, name+"-etcd", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s-etcd
description: etcd Deployment for %s
type: application
version: 0.1.0
appVersion: "v3.5.14"
dependencies: []
`, name, name)

	deployment := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s-etcd
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-etcd
spec:
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
      volumes:
        - name: etcd-data
          emptyDir: {}
`, name, name, name, name, name)

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
		filepath.Join(deploymentsDir, name+"-etcd", "Chart.yaml"): chartYAML,
		filepath.Join(dir, "deployment.yaml"):                     deployment,
		filepath.Join(dir, "service.yaml"):                        svc,
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
          {{- with .Values.resources }}
          resources:
            {{- toYaml . | nindent 12 }}
          {{- end }}
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

	var vb strings.Builder
	vb.WriteString("replicaCount: 1\n\n")
	vb.WriteString("image:\n")
	vb.WriteString("  repository: qingchun22/" + name + "-arm64\n")
	vb.WriteString("  pullPolicy: IfNotPresent\n")
	vb.WriteString("  tag: \"\"\n\n")
	vb.WriteString("service:\n")
	vb.WriteString("  type: ClusterIP\n")
	vb.WriteString("  port: 8080\n\n")
	vb.WriteString("resources: {}\n\n")
	vb.WriteString("env:\n")
	vb.WriteString("  - name: DATABASE_URL\n")
	vb.WriteString("    value: \"postgres://user:pass@" + name + "-postgres:5432/" + name + "?sslmode=disable&search_path=public\"\n")
	vb.WriteString("  - name: ETCD_ENDPOINTS\n")
	vb.WriteString("    value: \"" + name + "-etcd:2379\"\n")

	return writeFiles(map[string]string{
		filepath.Join(deploymentsDir, name, "Chart.yaml"):        chartYAML,
		filepath.Join(deploymentsDir, name, "values.yaml"):       vb.String(),
		filepath.Join(dir, "NOTES.txt"):                          notes,
		filepath.Join(dir, "deployment.yaml"):                    deployment,
		filepath.Join(dir, "service.yaml"):                       svc,
		filepath.Join(dir, "serviceaccount.yaml"):                sa,
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
	vb.WriteString("  # TODO: 替换为你构建的 controller 镜像（需包含 dtk + kubectl + helm）\n")
	vb.WriteString("  repository: your-registry/" + name + "-controller\n")
	vb.WriteString("  tag: latest\n\n")
	vb.WriteString("# 由 dtk deploy 通过 --set-file 自动注入 configs/resources.yaml\n")
	vb.WriteString("resourcesConfig: \"\"\n")

	rbac := fmt.Sprintf(`{{- if .Values.enabled }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s-controller
  namespace: {{ .Release.Namespace }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: %s-controller
rules:
  # 当前为全量权限，上线前请按实际需要收紧
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %s-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: %s-controller
subjects:
  - kind: ServiceAccount
    name: %s-controller
    namespace: {{ .Release.Namespace }}
{{- end }}
`, name, name, name, name, name)

	configmap := fmt.Sprintf(`{{- if .Values.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-controller-resources
  namespace: {{ .Release.Namespace }}
data:
  resources.yaml: |
{{ .Values.resourcesConfig | indent 4 }}
{{- end }}
`, name)

	deployment := fmt.Sprintf(`{{- if .Values.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s-controller
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-controller
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s-controller
  template:
    metadata:
      labels:
        app: %s-controller
    spec:
      serviceAccountName: %s-controller
      containers:
        - name: controller
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          imagePullPolicy: IfNotPresent
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
            name: %s-controller-resources
{{- end }}
`, name, name, name, name, name, name, name, name)

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
