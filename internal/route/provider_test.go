package route

import (
	"errors"
	"strings"
	"testing"

	"github.com/Ixecd/kubepivot/internal/code"
)

func TestRoute_Validate(t *testing.T) {
	cases := []struct {
		name    string
		route   Route
		wantErr bool
	}{
		{
			name:    "valid blue 100",
			route:   Route{Service: "wallet-blue", Weight: 100},
			wantErr: false,
		},
		{
			name:    "valid green 0",
			route:   Route{Service: "wallet-green", Weight: 0},
			wantErr: false,
		},
		{
			name:    "valid with match prefix",
			route:   Route{Service: "wallet", Weight: 50, Match: &Match{Path: "/api", PathType: "Prefix"}},
			wantErr: false,
		},
		{
			name:    "valid with empty PathType",
			route:   Route{Service: "wallet", Weight: 100, Match: &Match{Path: "/api"}},
			wantErr: false,
		},
		{
			name:    "invalid empty service",
			route:   Route{Service: "", Weight: 100},
			wantErr: true,
		},
		{
			name:    "invalid weight negative",
			route:   Route{Service: "wallet", Weight: -1},
			wantErr: true,
		},
		{
			name:    "invalid weight over 100",
			route:   Route{Service: "wallet", Weight: 101},
			wantErr: true,
		},
		{
			name:    "invalid bad PathType",
			route:   Route{Service: "wallet", Weight: 100, Match: &Match{Path: "/", PathType: "Glob"}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.route.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateRoutes(t *testing.T) {
	cases := []struct {
		name    string
		routes  []Route
		wantErr bool
	}{
		{
			name: "valid blue-green 100/0",
			routes: []Route{
				{Service: "wallet-blue", Weight: 100},
				{Service: "wallet-green", Weight: 0},
			},
			wantErr: false,
		},
		{
			name: "valid canary 90/10",
			routes: []Route{
				{Service: "wallet-v1", Weight: 90},
				{Service: "wallet-v2", Weight: 10},
			},
			wantErr: false,
		},
		{
			name:    "invalid empty",
			routes:  []Route{},
			wantErr: true,
		},
		{
			name: "invalid all zero weight",
			routes: []Route{
				{Service: "wallet-blue", Weight: 0},
				{Service: "wallet-green", Weight: 0},
			},
			wantErr: true,
		},
		{
			name: "invalid weight sum over 100",
			routes: []Route{
				{Service: "wallet-blue", Weight: 60},
				{Service: "wallet-green", Weight: 60},
			},
			wantErr: true,
		},
		{
			name: "invalid contains bad route",
			routes: []Route{
				{Service: "", Weight: 100},
				{Service: "wallet-green", Weight: 0},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRoutes(tc.routes)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateRoutes() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateWeight(t *testing.T) {
	cases := []struct {
		name      string
		input     []Route
		service   string
		weight    int32
		wantCount int
		wantSvc   string
		wantW     int32
	}{
		{
			name: "update existing service",
			input: []Route{
				{Service: "wallet-blue", Weight: 100},
				{Service: "wallet-green", Weight: 0},
			},
			service:   "wallet-blue",
			weight:    0,
			wantCount: 2,
			wantSvc:   "wallet-blue",
			wantW:     0,
		},
		{
			name: "append new service",
			input: []Route{
				{Service: "wallet-blue", Weight: 100},
			},
			service:   "wallet-green",
			weight:    0,
			wantCount: 2,
			wantSvc:   "wallet-green",
			wantW:     0,
		},
		{
			name:      "update on empty",
			input:     []Route{},
			service:   "wallet",
			weight:    100,
			wantCount: 1,
			wantSvc:   "wallet",
			wantW:     100,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := updateWeight(tc.input, tc.service, tc.weight)
			if len(result) != tc.wantCount {
				t.Fatalf("len(result)=%d want %d", len(result), tc.wantCount)
			}
			found := false
			for _, r := range result {
				if r.Service == tc.wantSvc {
					if r.Weight != tc.wantW {
						t.Errorf("service %s weight=%d want %d", tc.wantSvc, r.Weight, tc.wantW)
					}
					found = true
				}
			}
			if !found {
				t.Errorf("没找到 service %s", tc.wantSvc)
			}
		})
	}
}

func TestError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("kubectl: connection refused")
	err := WrapError(code.ErrRouteApplyFailed, cause, "ns=%s", "default")

	msg := err.Error()
	if !strings.Contains(msg, "Apply route rules failed") {
		t.Errorf("Error() 不含 code 描述: %s", msg)
	}
	if !strings.Contains(msg, "ns=default") {
		t.Errorf("Error() 不含 message: %s", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("Error() 不含 cause: %s", msg)
	}

	if errors.Unwrap(err) != cause {
		t.Errorf("Unwrap() != cause")
	}

	if !errors.Is(err, cause) {
		t.Errorf("errors.Is() 应该匹配 cause")
	}
}

func TestNewError(t *testing.T) {
	err := NewError(code.ErrRouteInvalid, "service=%s", "wallet")
	if err.Code != code.ErrRouteInvalid {
		t.Errorf("Code=%d want %d", err.Code, code.ErrRouteInvalid)
	}
	if !strings.Contains(err.Error(), "service=wallet") {
		t.Errorf("Error() 不含格式化后的 message: %s", err.Error())
	}
	if err.Cause != nil {
		t.Errorf("NewError 不应该有 cause")
	}
}
