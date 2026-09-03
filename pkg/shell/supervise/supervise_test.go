package supervise

import (
	"testing"
)

// TestViewURL：窗口地址 = 网关根路径 + 后端 ready URL 的完整 query 透传。
func TestViewURL(t *testing.T) {
	gw := &gatewayStub{port: 58231}
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"无 query", "http://127.0.0.1:58230", "http://127.0.0.1:58231/"},
		{"带 query", "http://127.0.0.1:58230?token=abc&x=1", "http://127.0.0.1:58231/?token=abc&x=1"},
		{"非法 URL 忽略 query", "://bad", "http://127.0.0.1:58231/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := viewURL(gw, c.url); got != c.want {
				t.Fatalf("viewURL(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

// gatewayStub 是 viewURL 的最小依赖（只用到 Port）。
type gatewayStub struct{ port int }

func (g *gatewayStub) Port() int { return g.port }
