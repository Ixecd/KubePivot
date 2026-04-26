package controller

import (
	"testing"
)

func TestResourcesConfig_HasBlueGreen(t *testing.T) {
	cases := []struct {
		name string
		cfg  ResourcesConfig
		want bool
	}{
		{
			name: "valid blue-green",
			cfg: ResourcesConfig{
				Traffic: &Traffic{
					Strategy: "blue-green",
					Refs:     TrafficRefs{Name: "wallet-ingress"},
					Routes: []TrafficRoute{
						{Service: "wallet-blue", Weight: 100},
						{Service: "wallet-green", Weight: 0},
					},
				},
			},
			want: true,
		},
		{
			name: "no traffic field",
			cfg: ResourcesConfig{
				Traffic: nil,
			},
			want: false,
		},
		{
			name: "wrong strategy",
			cfg: ResourcesConfig{
				Traffic: &Traffic{
					Strategy: "canary",
					Refs:     TrafficRefs{Name: "wallet-ingress"},
					Routes: []TrafficRoute{
						{Service: "wallet-v1", Weight: 90},
					},
				},
			},
			want: false,
		},
		{
			name: "missing refs.name",
			cfg: ResourcesConfig{
				Traffic: &Traffic{
					Strategy: "blue-green",
					Refs:     TrafficRefs{Name: ""},
					Routes: []TrafficRoute{
						{Service: "wallet-blue", Weight: 100},
					},
				},
			},
			want: false,
		},
		{
			name: "all zero weights",
			cfg: ResourcesConfig{
				Traffic: &Traffic{
					Strategy: "blue-green",
					Refs:     TrafficRefs{Name: "wallet-ingress"},
					Routes: []TrafficRoute{
						{Service: "wallet-blue", Weight: 0},
						{Service: "wallet-green", Weight: 0},
					},
				},
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.HasBlueGreen()
			if got != tc.want {
				t.Errorf("HasBlueGreen() = %v, want %v", got, tc.want)
			}
		})
	}
}
