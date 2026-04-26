package route

import (
	"testing"
)

func TestParseIngressRoutes(t *testing.T) {
	pathTypePrefix := "Prefix"

	cases := []struct {
		name    string
		ing     ingressManifest
		wantLen int
		wantSvc string
	}{
		{
			name: "single backend",
			ing: ingressManifest{
				Spec: ingressSpec{
					Rules: []ingressRule{
						{
							Host: "example.com",
							HTTP: &ingressHTTP{
								Paths: []ingressPath{
									{
										Path:     "/",
										PathType: &pathTypePrefix,
										Backend: ingressBackend{
											Service: ingressBackendService{
												Name: "wallet-blue",
												Port: ingressBackendPort{Number: 80},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantLen: 1,
			wantSvc: "wallet-blue",
		},
		{
			name: "no rules",
			ing: ingressManifest{
				Spec: ingressSpec{},
			},
			wantLen: 0,
		},
		{
			name: "duplicate backends deduplicated",
			ing: ingressManifest{
				Spec: ingressSpec{
					Rules: []ingressRule{
						{
							HTTP: &ingressHTTP{
								Paths: []ingressPath{
									{Backend: ingressBackend{Service: ingressBackendService{Name: "wallet"}}},
									{Backend: ingressBackend{Service: ingressBackendService{Name: "wallet"}}},
								},
							},
						},
					},
				},
			},
			wantLen: 1,
			wantSvc: "wallet",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes := parseIngressRoutes(&tc.ing)
			if len(routes) != tc.wantLen {
				t.Fatalf("len=%d want %d", len(routes), tc.wantLen)
			}
			if tc.wantSvc != "" && routes[0].Service != tc.wantSvc {
				t.Errorf("service=%s want %s", routes[0].Service, tc.wantSvc)
			}
			// 蓝绿场景下 Ingress 路由的 weight 默认应为 100
			if tc.wantLen > 0 && routes[0].Weight != 100 {
				t.Errorf("weight=%d want 100", routes[0].Weight)
			}
		})
	}
}

func TestBuildIngressFromRoutes_New(t *testing.T) {
	active := Route{Service: "wallet-blue", Weight: 100}

	ing := buildIngressFromRoutes("default", "wallet-ingress", active, nil)

	if ing.APIVersion != "networking.k8s.io/v1" {
		t.Errorf("apiVersion=%s want networking.k8s.io/v1", ing.APIVersion)
	}
	if ing.Kind != "Ingress" {
		t.Errorf("kind=%s want Ingress", ing.Kind)
	}

	if len(ing.Spec.Rules) != 1 {
		t.Fatalf("rules count=%d want 1", len(ing.Spec.Rules))
	}
	rule := ing.Spec.Rules[0]
	if rule.HTTP == nil {
		t.Fatalf("rule.HTTP is nil")
	}
	if len(rule.HTTP.Paths) != 1 {
		t.Fatalf("paths count=%d want 1", len(rule.HTTP.Paths))
	}
	path := rule.HTTP.Paths[0]
	if path.Backend.Service.Name != "wallet-blue" {
		t.Errorf("backend.service.name=%s want wallet-blue", path.Backend.Service.Name)
	}

	// labels 必带
	labels, ok := ing.Metadata["labels"].(map[string]any)
	if !ok {
		t.Fatalf("没有 labels")
	}
	if labels["app.kubernetes.io/managed-by"] != "kp" {
		t.Errorf("managed-by label 不对: %v", labels)
	}

	// annotations 必带
	anns, ok := ing.Metadata["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("没有 annotations")
	}
	if anns["kubepivot.io/managed"] != "true" {
		t.Errorf("managed annotation 不对: %v", anns)
	}
}

func TestBuildIngressFromRoutes_PreserveUserFields(t *testing.T) {
	pathTypePrefix := "Prefix"
	className := "nginx"

	current := &ingressManifest{
		Metadata: map[string]any{
			"annotations": map[string]any{
				"cert-manager.io/issuer":     "letsencrypt",
				"kubepivot.io/managed":       "true", // 应被覆盖
				"kubepivot.io/last-switch":   "yesterday",
			},
		},
		Spec: ingressSpec{
			IngressClassName: &className,
			Rules: []ingressRule{
				{
					Host: "old-host.com",
					HTTP: &ingressHTTP{
						Paths: []ingressPath{
							{
								Path:     "/api",
								PathType: &pathTypePrefix,
								Backend:  ingressBackend{Service: ingressBackendService{Name: "wallet-old"}},
							},
						},
					},
				},
			},
			TLS: []map[string]any{
				{"hosts": []string{"old-host.com"}, "secretName": "old-tls"},
			},
		},
	}

	active := Route{Service: "wallet-green", Weight: 100}
	ing := buildIngressFromRoutes("default", "wallet-ingress", active, current)

	// 1. 用户自定义 annotation 保留（cert-manager）
	anns := ing.Metadata["annotations"].(map[string]any)
	if anns["cert-manager.io/issuer"] != "letsencrypt" {
		t.Errorf("cert-manager annotation 没保留: %v", anns)
	}

	// 2. KubePivot 自己的 annotation 应该用新的（非 current 的）
	if anns["kubepivot.io/managed"] != "true" {
		t.Errorf("kubepivot.io/managed 不对")
	}

	// 3. IngressClassName 保留
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "nginx" {
		t.Errorf("IngressClassName 没保留")
	}

	// 4. TLS 保留
	if len(ing.Spec.TLS) != 1 {
		t.Errorf("TLS 没保留: %v", ing.Spec.TLS)
	}

	// 5. host 保留
	if ing.Spec.Rules[0].Host != "old-host.com" {
		t.Errorf("host 没保留: %s", ing.Spec.Rules[0].Host)
	}

	// 6. backend service 切换到 green
	backend := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name
	if backend != "wallet-green" {
		t.Errorf("backend.service.name=%s want wallet-green", backend)
	}

	// 7. path 保留（用户原 path 是 /api，不是 default 的 /）
	if ing.Spec.Rules[0].HTTP.Paths[0].Path != "/api" {
		t.Errorf("path 没保留: %s", ing.Spec.Rules[0].HTTP.Paths[0].Path)
	}
}
