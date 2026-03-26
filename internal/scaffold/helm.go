package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeHelmTemplateSkeleton 生成自包含的 Helm chart templates。
// 所有基础设施组件（postgres、etcd）均以 yaml 文件形式存放，
// 不依赖任何第三方 chart dependency。
// 组件命名规范：{name}-postgres、{name}-etcd，避免多项目同 namespace 冲突。
func writeHelmTemplateSkeleton(outputDir, name string) error {
	templatesDir := filepath.Join(outputDir, "deployments", name, "templates")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		return err
	}

	files := map[string]string{
		// ── NOTES.txt（chart 根目录，helm install/upgrade 后自动打印）──
		filepath.Join(templatesDir, "NOTES.txt"): `✅ {{ .Release.Name }} 部署成功！

命名空间: {{ .Release.Namespace }}
版本:     {{ .Values.image.tag | default .Chart.AppVersion }}
时间:     {{ now | date "2006-01-02 15:04:05" }}

组件状态:
  业务服务   ✓ running
  postgres  {{ if .Values.postgres.enabled }}✓ enabled{{ else }}✗ disabled{{ end }}
  etcd      {{ if .Values.etcd.enabled }}✓ enabled{{ else }}✗ disabled{{ end }}
  controller {{ if .Values.controller.enabled }}✓ enabled{{ else }}✗ disabled{{ end }}

快速访问:
  kubectl get pods -n {{ .Release.Namespace }}
  kubectl logs -n {{ .Release.Namespace }} deployment/{{ include "` + name + `.fullname" . }}
  kubectl port-forward -n {{ .Release.Namespace }} deployment/{{ include "` + name + `.fullname" . }} 8080:8080
`,

		// ── postgres StatefulSet + Service ──────────────────────────
		filepath.Join(templatesDir, "postgres-statefulset.yaml"): fmt.Sprintf(`{{- if .Values.postgres.enabled }}
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: %s-postgres
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-postgres
    {{- include "%s.labels" . | nindent 4 }}
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
            timeoutSeconds: 3
          livenessProbe:
            exec:
              command: ["pg_isready", "-U", "user", "-d", "%s"]
            initialDelaySeconds: 15
            periodSeconds: 10
            timeoutSeconds: 3
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
            storage: 1Gi
---
apiVersion: v1
kind: Service
metadata:
  name: %s-postgres
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-postgres
spec:
  selector:
    app: %s-postgres
  ports:
    - port: 5432
      targetPort: 5432
{{- end }}
`, name, name, name, name, name, name, name, name, name, name, name, name),

		// ── etcd Deployment + Service ────────────────────────────────
		filepath.Join(templatesDir, "etcd-deployment.yaml"): fmt.Sprintf(`{{- if .Values.etcd.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s-etcd
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-etcd
    {{- include "%s.labels" . | nindent 4 }}
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
            timeoutSeconds: 3
          livenessProbe:
            httpGet:
              path: /health
              port: 2379
            initialDelaySeconds: 10
            periodSeconds: 10
            timeoutSeconds: 3
          volumeMounts:
            - name: etcd-data
              mountPath: /etcd-data
      volumes:
        - name: etcd-data
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: %s-etcd
  namespace: {{ .Release.Namespace }}
  labels:
    app: %s-etcd
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
{{- end }}
`, name, name, name, name, name, name, name, name, name),

		// ── 业务服务 deployment，含 initContainers ───────────────────
		filepath.Join(templatesDir, "deployment.yaml"): fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "%s.fullname" . }}
  labels:
    {{- include "%s.labels" . | nindent 4 }}
spec:
  {{- if not .Values.autoscaling.enabled }}
  replicas: {{ .Values.replicaCount }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "%s.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      {{- with .Values.podAnnotations }}
      annotations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      labels:
        {{- include "%s.labels" . | nindent 8 }}
        {{- with .Values.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
    spec:
      {{- with .Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      serviceAccountName: {{ include "%s.serviceAccountName" . }}
      {{- if or .Values.postgres.enabled .Values.etcd.enabled }}
      initContainers:
        {{- if .Values.postgres.enabled }}
        - name: wait-postgres
          image: busybox:1.35
          command: ['sh', '-c', 'until nc -z %s-postgres 5432; do echo waiting for %s-postgres; sleep 2; done']
        {{- end }}
        {{- if .Values.etcd.enabled }}
        - name: wait-etcd
          image: busybox:1.35
          command: ['sh', '-c', 'until nc -z %s-etcd 2379; do echo waiting for %s-etcd; sleep 2; done']
        {{- end }}
      {{- end }}
      containers:
        - name: {{ .Chart.Name }}
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
          {{- with .Values.envFrom }}
          envFrom:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          {{- with .Values.livenessProbe }}
          livenessProbe:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          {{- with .Values.readinessProbe }}
          readinessProbe:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          {{- with .Values.resources }}
          resources:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          {{- with .Values.volumeMounts }}
          volumeMounts:
            {{- toYaml . | nindent 12 }}
          {{- end }}
      {{- with .Values.volumes }}
      volumes:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
`, name, name, name, name, name, name, name, name, name),

		// ── controller RBAC ──────────────────────────────────────────
		filepath.Join(templatesDir, "controller-rbac.yaml"): `{{- if .Values.controller.enabled }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "` + name + `.fullname" . }}-controller
  namespace: {{ .Release.Namespace }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ include "` + name + `.fullname" . }}-controller
rules:
  # 当前为全量权限，上线前请按实际需要收紧
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ include "` + name + `.fullname" . }}-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: {{ include "` + name + `.fullname" . }}-controller
subjects:
  - kind: ServiceAccount
    name: {{ include "` + name + `.fullname" . }}-controller
    namespace: {{ .Release.Namespace }}
{{- end }}
`,

		// ── resources ConfigMap ──────────────────────────────────────
		filepath.Join(templatesDir, "resources-configmap.yaml"): `{{- if .Values.controller.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "` + name + `.fullname" . }}-resources
  namespace: {{ .Release.Namespace }}
data:
  resources.yaml: |
{{ .Values.controller.resourcesConfig | indent 4 }}
{{ end }}
`,

		// ── controller Deployment ────────────────────────────────────
		filepath.Join(templatesDir, "controller-deployment.yaml"): `{{- if .Values.controller.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "` + name + `.fullname" . }}-controller
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "` + name + `.labels" . | nindent 4 }}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: {{ include "` + name + `.fullname" . }}-controller
  template:
    metadata:
      labels:
        app: {{ include "` + name + `.fullname" . }}-controller
    spec:
      serviceAccountName: {{ include "` + name + `.fullname" . }}-controller
      containers:
        - name: controller
          image: "{{ .Values.controller.image.repository }}:{{ .Values.controller.image.tag }}"
          imagePullPolicy: IfNotPresent
          command: ["dtk", "controller", "start"]
          env:
            - name: PROJECT_NAME
              value: "` + name + `"
            - name: KUBE_NAMESPACE
              value: {{ .Release.Namespace }}
            - name: ETCD_ENDPOINTS
              value: "` + name + `-etcd:2379"
            - name: RESOURCES_CONFIG
              value: "/etc/controller/resources.yaml"
            - name: VERSION
              value: "{{ .Values.image.tag }}"
          volumeMounts:
            - name: resources-config
              mountPath: /etc/controller
      volumes:
        - name: resources-config
          configMap:
            name: {{ include "` + name + `.fullname" . }}-resources
{{- end }}
`,
	}

	// values.yaml 单独用 Builder 生成，避免 fmt.Sprintf raw string 嵌套问题
	var vb strings.Builder
	vb.WriteString("# Default values for " + name + ".\n")
	vb.WriteString("replicaCount: 1\n\n")
	vb.WriteString("image:\n")
	vb.WriteString("  repository: qingchun22/" + name + "-arm64\n")
	vb.WriteString("  pullPolicy: IfNotPresent\n")
	vb.WriteString("  tag: \"\"\n\n")
	vb.WriteString("imagePullSecrets: []\n")
	vb.WriteString("nameOverride: \"\"\n")
	vb.WriteString("fullnameOverride: \"\"\n\n")
	vb.WriteString("serviceAccount:\n")
	vb.WriteString("  create: true\n")
	vb.WriteString("  automount: true\n")
	vb.WriteString("  annotations: {}\n")
	vb.WriteString("  name: \"\"\n\n")
	vb.WriteString("podAnnotations: {}\n")
	vb.WriteString("podLabels: {}\n")
	vb.WriteString("podSecurityContext: {}\n")
	vb.WriteString("securityContext: {}\n\n")
	vb.WriteString("service:\n")
	vb.WriteString("  type: ClusterIP\n")
	vb.WriteString("  port: 8080\n\n")
	vb.WriteString("ingress:\n")
	vb.WriteString("  enabled: false\n\n")
	vb.WriteString("httpRoute:\n")
	vb.WriteString("  enabled: false\n\n")
	vb.WriteString("# 基础设施组件开关\n")
	vb.WriteString("# 不需要 postgres 或 etcd 时改为 false\n")
	vb.WriteString("# initContainers 会自动跳过对应的 wait 步骤\n")
	vb.WriteString("postgres:\n")
	vb.WriteString("  enabled: true\n\n")
	vb.WriteString("etcd:\n")
	vb.WriteString("  enabled: true\n\n")
	vb.WriteString("resources: {}\n\n")
	vb.WriteString("autoscaling:\n")
	vb.WriteString("  enabled: false\n")
	vb.WriteString("  minReplicas: 1\n")
	vb.WriteString("  maxReplicas: 100\n")
	vb.WriteString("  targetCPUUtilizationPercentage: 80\n\n")
	vb.WriteString("livenessProbe:\n")
	vb.WriteString("  httpGet:\n")
	vb.WriteString("    path: /healthz\n")
	vb.WriteString("    port: 8080\n")
	vb.WriteString("  initialDelaySeconds: 10\n")
	vb.WriteString("  periodSeconds: 10\n\n")
	vb.WriteString("readinessProbe:\n")
	vb.WriteString("  httpGet:\n")
	vb.WriteString("    path: /healthz\n")
	vb.WriteString("    port: 8080\n")
	vb.WriteString("  initialDelaySeconds: 5\n")
	vb.WriteString("  periodSeconds: 5\n\n")
	vb.WriteString("volumes: []\n")
	vb.WriteString("volumeMounts: []\n")
	vb.WriteString("nodeSelector: {}\n")
	vb.WriteString("tolerations: []\n")
	vb.WriteString("affinity: {}\n\n")
	vb.WriteString("# 环境变量 — 通过 deployment template 注入到容器\n")
	vb.WriteString("env:\n")
	vb.WriteString("  - name: DATABASE_URL\n")
	vb.WriteString("    value: \"postgres://user:pass@" + name + "-postgres:5432/" + name + "?sslmode=disable&search_path=public\"\n")
	vb.WriteString("  - name: ETCD_ENDPOINTS\n")
	vb.WriteString("    value: \"" + name + "-etcd:2379\"\n\n")
	vb.WriteString("controller:\n")
	vb.WriteString("  # 配置好镜像后将 enabled 改为 true，再重新 dtk deploy\n")
	vb.WriteString("  enabled: false\n")
	vb.WriteString("  # TODO: 替换为你构建的 controller 镜像\n")
	vb.WriteString("  # 镜像需包含 dtk 二进制 + kubectl + helm\n")
	vb.WriteString("  # 构建方式参考 build/docker/controller/Dockerfile\n")
	vb.WriteString("  image:\n")
	vb.WriteString("    repository: your-registry/" + name + "-controller\n")
	vb.WriteString("    tag: latest\n")
	vb.WriteString("  # 由 dtk deploy 通过 --set-file 自动注入 configs/resources.yaml\n")
	vb.WriteString("  resourcesConfig: \"\"\n")

	valuesPath := filepath.Join(outputDir, "deployments", name, "values.yaml")
	if err := os.WriteFile(valuesPath, []byte(vb.String()), 0o644); err != nil {
		return fmt.Errorf("gen values.yaml: %w", err)
	}

	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gen helm template %s: %w", path, err)
		}
	}
	return nil
}
