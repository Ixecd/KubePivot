package route

import (
	"testing"
)

func TestParseHTTPRouteRoutes(t *testing.T) {
	w50 := int32(50)
	w0 := int32(0)

	cases := []struct {
		name      string
		hr        httpRouteManifest
		wantLen   int
		wantNames []string
		wantWs    []int32
	}{
		{
			name: "blue-green 100/0",
			hr: httpRouteManifest{
				Spec: httpRouteSpec{
					Rules: []httpRouteRule{
						{
							BackendRefs: []httpRouteBackend{
								{Name: "wallet-blue", Weight: nil},
								{Name: "wallet-green", Weight: &w0},
							},
						},
					},
				},
			},
			wantLen:   2,
			wantNames: []string{"wallet-blue", "wallet-green"},
			wantWs:    []int32{100, 0}, // weight nil → 默认 100
		},
		{
			name: "canary 50/50",
			hr: httpRouteManifest{
				Spec: httpRouteSpec{
					Rules: []httpRouteRule{
						{
							BackendRefs: []httpRouteBackend{
								{Name: "wallet-v1", Weight: &w50},
								{Name: "wallet-v2", Weight: &w50},
							},
						},
					},
				},
			},
			wantLen:   2,
			wantNames: []string{"wallet-v1", "wallet-v2"},
			wantWs:    []int32{50, 50},
		},
		{
			name: "duplicate dedupe",
			hr: httpRouteManifest{
				Spec: httpRouteSpec{
					Rules: []httpRouteRule{
						{
							BackendRefs: []httpRouteBackend{
								{Name: "wallet"},
								{Name: "wallet"}, // dup
							},
						},
					},
				},
			},
			wantLen:   1,
			wantNames: []string{"wallet"},
			wantWs:    []int32{100},
		},
		{
			name:    "empty rules",
			hr:      httpRouteManifest{Spec: httpRouteSpec{}},
			wantLen: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes := parseHTTPRouteRoutes(&tc.hr)
			if len(routes) != tc.wantLen {
				t.Fatalf("len=%d want %d", len(routes), tc.wantLen)
			}
			for i := range routes {
				if routes[i].Service != tc.wantNames[i] {
					t.Errorf("[%d] service=%s want %s", i, routes[i].Service, tc.wantNames[i])
				}
				if routes[i].Weight != tc.wantWs[i] {
					t.Errorf("[%d] weight=%d want %d", i, routes[i].Weight, tc.wantWs[i])
				}
			}
		})
	}
}

func TestBuildHTTPRouteFromRoutes_New(t *testing.T) {
	routes := []Route{
		{Service: "wallet-blue", Weight: 100},
		{Service: "wallet-green", Weight: 0},
	}

	hr := buildHTTPRouteFromRoutes("default", "wallet-route", routes, nil)

	if hr.APIVersion != "gateway.networking.k8s.io/v1" {
		t.Errorf("apiVersion=%s want gateway.networking.k8s.io/v1", hr.APIVersion)
	}
	if hr.Kind != "HTTPRoute" {
		t.Errorf("kind=%s want HTTPRoute", hr.Kind)
	}

	if len(hr.Spec.Rules) != 1 {
		t.Fatalf("rules count=%d want 1", len(hr.Spec.Rules))
	}
	rule := hr.Spec.Rules[0]
	if len(rule.BackendRefs) != 2 {
		t.Fatalf("backendRefs count=%d want 2", len(rule.BackendRefs))
	}

	// blue weight=100
	if rule.BackendRefs[0].Name != "wallet-blue" || *rule.BackendRefs[0].Weight != 100 {
		t.Errorf("blue backend 不对: %+v", rule.BackendRefs[0])
	}
	// green weight=0
	if rule.BackendRefs[1].Name != "wallet-green" || *rule.BackendRefs[1].Weight != 0 {
		t.Errorf("green backend 不对: %+v", rule.BackendRefs[1])
	}
}

func TestBuildHTTPRouteFromRoutes_PreserveUserFields(t *testing.T) {
	current := &httpRouteManifest{
		Metadata: map[string]any{
			"annotations": map[string]any{
				"argocd.argoproj.io/sync-wave": "1",
				"kubepivot.io/managed":         "true",
			},
		},
		Spec: httpRouteSpec{
			ParentRefs: []map[string]any{
				{"name": "my-gateway", "namespace": "infra"},
			},
			Hostnames: []string{"example.com"},
			Rules: []httpRouteRule{
				{
					Matches: []map[string]any{
						{"path": map[string]any{"type": "PathPrefix", "value": "/api"}},
					},
					BackendRefs: []httpRouteBackend{
						{Name: "wallet-old"},
					},
				},
			},
		},
	}

	routes := []Route{
		{Service: "wallet-blue", Weight: 100},
		{Service: "wallet-green", Weight: 0},
	}

	hr := buildHTTPRouteFromRoutes("default", "wallet-route", routes, current)

	// 1. 用户 annotation 保留
	anns := hr.Metadata["annotations"].(map[string]any)
	if anns["argocd.argoproj.io/sync-wave"] != "1" {
		t.Errorf("argo annotation 没保留: %v", anns)
	}

	// 2. parentRefs 保留
	if len(hr.Spec.ParentRefs) != 1 {
		t.Errorf("parentRefs 没保留: %+v", hr.Spec.ParentRefs)
	}

	// 3. hostnames 保留
	if len(hr.Spec.Hostnames) != 1 || hr.Spec.Hostnames[0] != "example.com" {
		t.Errorf("hostnames 没保留: %+v", hr.Spec.Hostnames)
	}

	// 4. matches 保留（用户的 path matcher）
	if len(hr.Spec.Rules[0].Matches) != 1 {
		t.Errorf("matches 没保留: %+v", hr.Spec.Rules[0].Matches)
	}

	// 5. backendRefs 替换为新 routes
	if len(hr.Spec.Rules[0].BackendRefs) != 2 {
		t.Errorf("backendRefs count=%d want 2", len(hr.Spec.Rules[0].BackendRefs))
	}
	if hr.Spec.Rules[0].BackendRefs[0].Name != "wallet-blue" {
		t.Errorf("backend[0] 没切换到 wallet-blue")
	}
}
