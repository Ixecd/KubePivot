package route

import (
	"errors"
	"testing"

	"github.com/Ixecd/kubepivot/internal/code"
)

func TestNewProvider(t *testing.T) {
	cases := []struct {
		kind    string
		wantErr bool
		wantTyp string // 期望的 Provider.Name() 返回值
	}{
		{kind: "Ingress", wantErr: false, wantTyp: "Ingress"},
		{kind: "Gateway", wantErr: false, wantTyp: "GatewayAPI"},
		{kind: "GatewayAPI", wantErr: false, wantTyp: "GatewayAPI"},
		{kind: "Foo", wantErr: true},
		{kind: "", wantErr: true},
		{kind: "ingress", wantErr: true}, // 区分大小写
	}

	for _, tc := range cases {
		t.Run("kind="+tc.kind, func(t *testing.T) {
			p, err := NewProvider(tc.kind)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if p.Name() != tc.wantTyp {
					t.Errorf("Name()=%s want %s", p.Name(), tc.wantTyp)
				}
			} else {
				// 错误码应该是 ErrRouteProviderNotAvailable
				var rerr *Error
				if !errors.As(err, &rerr) {
					t.Errorf("err 不是 *Error 类型: %T", err)
				} else if rerr.Code != code.ErrRouteProviderNotAvailable {
					t.Errorf("err.Code=%d want %d", rerr.Code, code.ErrRouteProviderNotAvailable)
				}
			}
		})
	}
}

// 注意：AutoDetect 和 ProviderForKind 的实际探测调用 kubectl，
// 不在单测里跑（会失败，因为测试环境没真实集群）。
// 这两个的集成测试在 v2.6.0 demo 工程里做。
