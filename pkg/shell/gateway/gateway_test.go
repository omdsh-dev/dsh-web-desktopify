package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestInjectIndex(t *testing.T) {
	html := "<!doctype html><html><head><meta charset=\"utf-8\"></head><body>x</body></html>"
	g := &Gateway{nonce: "n1"}
	out := g.injectIndex(html)
	if !strings.Contains(out, "data-dsh-gateway=\"n1\"") {
		t.Fatal("应注入 marker")
	}
	if !strings.Contains(out, "/wails/runtime.js") {
		t.Fatal("应注入 runtime.js 引用")
	}
	// runtime.js 是 ESM：必须 type="module"，否则浏览器按经典脚本解析报
	// "Unexpected keyword 'export'"。
	if !strings.Contains(out, "type=\"module\" src=\"/wails/runtime.js\"") {
		t.Fatal("runtime.js 应注入为 type=module 脚本")
	}
	if !strings.Contains(out, "__dshSharedLocalStorageInstalled") {
		t.Fatal("应注入 bridge")
	}
	// 无种子提供者时 seed 应为空数组，bridge 仍有效
	if !strings.Contains(out, "var __seed = [];") {
		t.Fatal("无种子时应为空数组")
	}
	// 幂等：二次注入不重复
	out2 := g.injectIndex(out)
	if strings.Count(out2, "/wails/runtime.js") != 1 {
		t.Fatalf("重复注入应被幂等抑制: %s", out2)
	}
}

func TestInjectIndexSeed(t *testing.T) {
	html := "<!doctype html><html><head></head><body>x</body></html>"
	g := &Gateway{
		nonce: "n1",
		seed: func() (map[string]string, []string) {
			return map[string]string{
				"dsh.sessions.current": `"sess-9"`,
				"k2":                   "v2",
			}, []string{"dsh.sessions.current", "k2"}
		},
	}
	out := g.injectIndex(html)
	// 种子按序内嵌为 [key, value] 数组，dsh.sessions.current 在首位。
	// 值里的引号是 JSON 转义（\"），用原始字符串匹配。
	if !strings.Contains(out, `var __seed = [["dsh.sessions.current","\"sess-9\""],["k2","v2"]];`) {
		t.Fatalf("种子应内嵌 dsh.sessions.current 值: %s", out)
	}
}

func TestInjectIndexNoHead(t *testing.T) {
	html := "<html><body>x</body></html>"
	g := &Gateway{nonce: "n1"}
	if out := g.injectIndex(html); out != html {
		t.Fatal("无 <head> 时不注入")
	}
}

func TestGatewayRoutes(t *testing.T) {
	// 后端：回显路径
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<head></head>BACKEND:"+r.URL.Path)
	}))
	defer backend.Close()

	gw, err := Start(strings.TrimPrefix(backend.URL, "http://"), context.Background())
	if err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer gw.srv.Close()
	base := "http://127.0.0.1:" + strconv.Itoa(gw.Port())

	// 1. runtime.js 伺服（测试环境为占位；CLI 构建时覆盖为 wails 真 runtime）
	resp, err := http.Get(base + "/wails/runtime.js")
	if err != nil {
		t.Fatalf("GET runtime.js: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(body) == 0 {
		t.Fatal("runtime.js 不应为空")
	}

	// 2. 其他路由反代 + 注入
	resp3, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if !strings.Contains(string(body3), "BACKEND:/") {
		t.Fatalf("应反代后端: %s", body3)
	}
	if !strings.Contains(string(body3), "/wails/runtime.js") {
		t.Fatalf("index 应注入 runtime.js: %s", body3)
	}

	// 4. IPC 未就绪（无 MessageProcessor）→ 503
	resp4, err := http.Post(base+"/wails/runtime", "application/json", strings.NewReader(`{"object":0,"method":0}`))
	if err != nil {
		t.Fatalf("POST runtime: %v", err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("IPC 未就绪应 503，got %d", resp4.StatusCode)
	}
}

func TestGatewaySetTarget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	gw, err := Start("127.0.0.1:1", context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gw.srv.Close()
	base := "http://127.0.0.1:" + strconv.Itoa(gw.Port())

	// 网关监听端口应为 OS 随机分配（非固定），两次实例端口不同。
	if gw.Port() == 1 {
		t.Fatal("网关端口不应是占位 1")
	}

	// 占位 target 未就绪 → 502
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("未就绪应 502，got %d", resp.StatusCode)
	}

	gw.SetTarget(strings.TrimPrefix(backend.URL, "http://"))
	resp2, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET after set: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("纯 host 就绪后应 200，got %d", resp2.StatusCode)
	}

	// 完整 URL（server.Start 返回的形态）
	gw.SetTarget(backend.URL)
	resp3, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET after set url: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("完整 URL 就绪后应 200，got %d", resp3.StatusCode)
	}
}

func TestGatewayRandomPort(t *testing.T) {
	// 两次启动实例端口不同：网关监听端口是 OS 随机分配，非固定。
	ports := make(map[int]bool)
	for i := 0; i < 3; i++ {
		gw, err := Start("127.0.0.1:1", context.Background())
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		ports[gw.Port()] = true
		gw.srv.Close()
	}
	if len(ports) < 2 {
		t.Fatalf("网关端口应随机分配，实际: %v", ports)
	}
}

func TestGatewaySeed(t *testing.T) {
	// 端到端：网关注入页面时把共享存储快照内嵌为 bridge 种子，页面启动
	// 即可同步读到上次会话写入的值（dsh.sessions.current），不依赖 wails。
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<head></head>BODY")
	}))
	defer backend.Close()

	gw, err := Start(strings.TrimPrefix(backend.URL, "http://"), context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gw.srv.Close()
	gw.SetSeedProvider(func() (map[string]string, []string) {
		return map[string]string{"dsh.sessions.current": `"sess-9"`}, []string{"dsh.sessions.current"}
	})

	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(gw.Port()) + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `["dsh.sessions.current","\"sess-9\""]`) {
		t.Fatalf("页面应内嵌共享存储种子: %s", body)
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		host string
	}{
		{"127.0.0.1:58230", "127.0.0.1:58230"},
		{"http://127.0.0.1:58230", "127.0.0.1:58230"},
	}
	for _, c := range cases {
		u, err := parseTarget(c.in)
		if err != nil {
			t.Fatalf("parseTarget(%q): %v", c.in, err)
		}
		if u.Host != c.host {
			t.Fatalf("parseTarget(%q) host = %q, want %q", c.in, u.Host, c.host)
		}
	}
}

// makeAuthCookie 构造一个 authority 为指定 host 的 dsh 认证 cookie 值
// （v1.<base64url JSON>.<sig>，sig 任意）。
func makeAuthCookie(host string) string {
	payload, _ := json.Marshal(map[string]string{"authority": host})
	return "v1." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestFilterCookies(t *testing.T) {
	cur := makeAuthCookie("127.0.0.1:54896")
	old := makeAuthCookie("127.0.0.1:50155")
	older := makeAuthCookie("127.0.0.1:49526")

	cases := []struct {
		name   string
		header string
		host   string
		want   string
	}{
		{
			name:   "只保留当前端口的 dsh-auth",
			header: "dsh-auth-a=" + cur + "; dsh-auth-b=" + old + "; dsh-auth-c=" + older,
			host:   "127.0.0.1:54896",
			want:   "dsh-auth-a=" + cur,
		},
		{
			name:   "非 dsh-auth cookie 原样保留",
			header: "dsh-auth-a=" + cur + "; theme=dark; session=x",
			host:   "127.0.0.1:54896",
			want:   "dsh-auth-a=" + cur + "; theme=dark; session=x",
		},
		{
			name:   "无 dsh-auth 时原样返回",
			header: "theme=dark; session=x",
			host:   "127.0.0.1:54896",
			want:   "theme=dark; session=x",
		},
		{
			name:   "payload 解析失败保守保留",
			header: "dsh-auth-a=not-a-token; theme=dark",
			host:   "127.0.0.1:54896",
			want:   "dsh-auth-a=not-a-token; theme=dark",
		},
		{
			name:   "空 Cookie 头",
			header: "",
			host:   "127.0.0.1:54896",
			want:   "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filterCookies(c.header, c.host)
			if got != c.want {
				t.Fatalf("filterCookies(%q, %q) = %q, want %q", c.header, c.host, got, c.want)
			}
		})
	}
}
