// Package gateway 提供壳内 HTTP 网关：把 dsh 后端（随机端口）的 HTTP 服务
// 通过壳的随机端口转发，并在 index.html 响应中强制注入 wails runtime.js
// 与共享 localStorage bridge。页面 origin 是壳网关端口（随机、稳定），
// 不受后端随机端口影响。
//
// 路由：
//   - /wails/runtime.js（GET）— 伺服 wails runtime：由 wails assetserver
//     （Transport.ServeAssets 注入）提供，网关经内存缓冲转发（避免 wails
//     fatal）；页面经注入的 <script> 加载后，window.wails 可用，绑定调用
//     （Call.ByName）经 HTTP fetch 到 /wails/runtime。
//   - /wails/runtime（POST）— wails IPC：转发给 MessageProcessor
//     （HandleRuntimeCallWithIDs），响应格式与 wails HTTPTransport 一致。
//   - 其他 — 反代到 dsh 后端（保留原始 Host：dsh 的 /api browser-trust
//     栅栏要求 Origin 与 Host 同 host）。
//
// SSE（dsh 的 /api/events 流）依赖长连接：ReverseProxy 原生透传
// text/event-stream（flush 在 FlushInterval 0 下立即执行），无需特殊处理。
package gateway

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed bridge.js
var bridgeTemplate string

// BridgeScript 返回注入脚本（bridge 经 window.wails.Call.ByName 把
// localStorage 读写转接到共享存储服务）。
func BridgeScript() string {
	return bridgeTemplate
}

// Gateway 是壳网关服务的生命周期句柄。
type Gateway struct {
	srv    *http.Server
	ln     net.Listener
	mu     sync.RWMutex
	target *url.URL
	proc   *application.MessageProcessor
	assets http.Handler // wails assetserver（ServeAssets 注入，伺服 runtime.js）
	nonce  string
	seed   func() (map[string]string, []string) // 共享存储快照提供者（bridge 种子）
}

// Port 返回网关实际监听的端口。
func (g *Gateway) Port() int {
	return g.ln.Addr().(*net.TCPAddr).Port
}

// SetTarget 更新后端地址（后端重启后调用）。host 接受纯主机
// （127.0.0.1:58230）或完整 URL（http://127.0.0.1:58230）。
func (g *Gateway) SetTarget(host string) {
	u, err := parseTarget(host)
	if err != nil {
		log.Printf("gateway: 无效后端地址 %s: %v", host, err)
		return
	}
	g.mu.Lock()
	g.target = u
	g.mu.Unlock()
}

// parseTarget 解析后端地址：无 scheme 时补 http://（接受纯 host 或完整 URL）。
// 返回的 target 去掉 query——认证 token 等由浏览器 URL 携带（网关转发时
// 保留 In 的 query），target 只作为后端 origin，避免 SetURL 把 target 的
// query 与请求 query 合并导致 token 重复（dsh 认证拒绝重复 token）。
func parseTarget(host string) (*url.URL, error) {
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil {
		return nil, err
	}
	u.RawQuery = ""
	u.ForceQuery = false
	return u, nil
}

// SetMessageProcessor 设置 wails IPC 处理器（Transport.Start 时注入）。
func (g *Gateway) SetMessageProcessor(proc *application.MessageProcessor) {
	g.mu.Lock()
	g.proc = proc
	g.mu.Unlock()
}

// SetAssetHandler 设置 wails assetserver handler（Transport.ServeAssets 时
// 注入）：/wails/runtime.js 等 wails 资源由它伺服，壳无需自己提供。
func (g *Gateway) SetAssetHandler(handler http.Handler) {
	g.mu.Lock()
	g.assets = handler
	g.mu.Unlock()
}

// SetSeedProvider 设置共享存储快照提供者：注入 bridge 时把当前状态内嵌为
// 种子，页面启动即可同步读到上次会话写入的值（如 dsh.sessions.current），
// 读取路径不依赖异步 wails IPC。
func (g *Gateway) SetSeedProvider(seed func() (map[string]string, []string)) {
	g.mu.Lock()
	g.seed = seed
	g.mu.Unlock()
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/wails/runtime.js":
		// wails assetserver 伺服 runtime.js；未注入时（Transport 未启动）
		// 返回 503。响应经内存缓冲再写给客户端：assetserver 侧只写内存
		// 永不失败，避免客户端中途断开触发 wails 的 fatal（os.Exit(1)
		// 直接杀死整个壳进程）。
		g.mu.RLock()
		assets := g.assets
		g.mu.RUnlock()
		if assets == nil {
			http.Error(w, "wails assets not ready", http.StatusServiceUnavailable)
			return
		}
		g.serveAssetsBuffered(w, r, assets)
		return
	case r.URL.Path == "/wails/runtime":
		g.handleIPC(w, r)
		return
	}
	g.mu.RLock()
	target := g.target
	g.mu.RUnlock()
	if target == nil {
		http.Error(w, "backend not ready", http.StatusBadGateway)
		return
	}
	g.route(w, r, target)
}

// serveAssetsBuffered 把 wails assetserver 的响应完整读入内存再写给真实
// 客户端。wails assetserver 对写失败（如客户端中途断开）会调用 fatal →
// os.Exit(1) 杀掉整个壳进程；缓冲后 assetserver 只写内存，永远不会因真实
// 客户端的行为触发该路径。runtime.js 约 515KB，每次请求的内存缓冲开销可
// 忽略。
func (g *Gateway) serveAssetsBuffered(w http.ResponseWriter, r *http.Request, assets http.Handler) {
	rec := httptest.NewRecorder()
	assets.ServeHTTP(rec, r)
	h := w.Header()
	for k, vv := range rec.Header() {
		for _, v := range vv {
			h.Add(k, v)
		}
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// handleIPC 处理 wails 绑定调用（POST /wails/runtime），协议与 wails
// HTTPTransport 一致：body {object, method, args}，响应 JSON 或 text。
func (g *Gateway) handleIPC(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	proc := g.proc
	g.mu.RUnlock()
	if proc == nil {
		http.Error(w, "wails runtime not ready", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		Object *int            `json:"object"`
		Method *int            `json:"method"`
		Args   json.RawMessage `json:"args"`
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "parse body: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}
	if body.Object == nil || body.Method == nil {
		http.Error(w, "missing object or method", http.StatusUnprocessableEntity)
		return
	}

	windowID := 0
	if v := r.Header.Get("x-wails-window-id"); v != "" {
		fmt.Sscanf(v, "%d", &windowID)
	}
	args := &application.Args{}
	if len(body.Args) > 0 {
		if err := args.UnmarshalJSON(body.Args); err != nil {
			http.Error(w, "parse args: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}
	resp, err := proc.HandleRuntimeCallWithIDs(r.Context(), &application.RuntimeRequest{
		Object:            *body.Object,
		Method:            *body.Method,
		Args:              args,
		WebviewWindowID:   uint32(windowID),
		WebviewWindowName: r.Header.Get("x-wails-window-name"),
		ClientID:          r.Header.Get("x-wails-client-id"),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if s, ok := resp.(string); ok {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(s))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (g *Gateway) route(w http.ResponseWriter, r *http.Request, target *url.URL) {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// 后端认证（browser-auth）把 Host 作为 cookie 名与签名 audience：
			// token 的 authority 是后端端口。网关转发时把 Host 设为后端，
			// 并把浏览器 Origin（网关）改写为后端——dsh 的 /api trust 栅栏
			// 要求 Origin 与 Host 同 host，两侧对齐后端才能同时通过信任栅栏
			// 与 token 认证。
			pr.Out.Host = target.Host
			if origin := pr.Out.Header.Get("Origin"); origin != "" {
				pr.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
			// 过滤历史端口的 dsh-auth-* cookie：cookie 的 domain 是
			// 127.0.0.1（不含端口），每次启动后端端口变化都会新增一个
			// dsh-auth-<salt>（30 天过期），累积后请求头超过 Node 的
			// maxHeaderSize（16KB）触发 431，script/API 全部加载失败。
			// 只保留 authority 与当前后端 host 一致的 cookie。
			if cookie := pr.Out.Header.Get("Cookie"); cookie != "" {
				pr.Out.Header.Set("Cookie", filterCookies(cookie, target.Host))
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			ct := resp.Header.Get("Content-Type")
			if resp.StatusCode == http.StatusOK && strings.Contains(ct, "text/html") {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}
				resp.Body.Close()
				html := string(body)
				injected := g.injectIndex(html)
				resp.Body = io.NopCloser(strings.NewReader(injected))
				resp.Header.Set("Content-Length", fmt.Sprint(len(injected)))
			}
			return nil
		},
	}
	rp.ServeHTTP(w, r)
}

// seedMarker 是 bridge.js 中共享存储种子的占位符，注入时替换为 JSON。
const seedMarker = "/*__DSH_SHARED_SEED__*/"

// dshAuthPrefix 是 dsh 认证 cookie 名前缀（browser-auth 按 Host 派生）。
const dshAuthPrefix = "dsh-auth-"

// filterCookies 过滤 Cookie 头里 authority 与当前后端 host 不一致的
// dsh-auth-* cookie（其余 cookie 原样保留）。dsh 认证 cookie 的 value 是
// v1.<base64url JSON>.<sig>，JSON 的 authority 字段记录签发时的后端
// host（127.0.0.1:<port>）；cookie 的 domain 不含端口，历史端口的 cookie
// 会被 WKWebView 一并带上，累积超过 Node maxHeaderSize（16KB）时后端返回
// 431。解析失败或非 dsh-auth 的 cookie 一律保留（不阻断请求）。
func filterCookies(header, host string) string {
	if !strings.Contains(header, dshAuthPrefix) {
		return header
	}
	parts := strings.Split(header, "; ")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		name, value, _ := strings.Cut(part, "=")
		if strings.HasPrefix(name, dshAuthPrefix) && !authCookieMatches(value, host) {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "; ")
}

// authCookieMatches 解码 dsh 认证 cookie 的 payload，报告其 authority 是否
// 与 host 一致。value 形如 v1.<base64url JSON>.<sig>；payload 解析失败时
// 返回 true（保守保留，让后端自行判定）。
func authCookieMatches(value, host string) bool {
	dot1 := strings.IndexByte(value, '.')
	if dot1 < 0 {
		return true
	}
	rest := value[dot1+1:]
	dot2 := strings.IndexByte(rest, '.')
	if dot2 < 0 {
		return true
	}
	payload := rest[:dot2]
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return true
	}
	var claims struct {
		Authority string `json:"authority"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return true
	}
	return claims.Authority == host
}

// injectIndex 把 runtime.js 与 bridge 注入到 <head> 中（未找到 <head> 时
// 跳过）。nonce 用于幂等：已注入的响应不再重复注入。bridge 内嵌共享存储
// 快照种子（seed）：页面启动即可同步读到上次会话写入的值（如
// dsh.sessions.current），读取不依赖异步 wails IPC。
func (g *Gateway) injectIndex(html string) string {
	marker := "data-dsh-gateway=\"" + g.nonce + "\""
	if strings.Contains(html, marker) {
		return html
	}
	if idx := strings.Index(html, "<head>"); idx >= 0 {
		// runtime.js 是 ESM（wails 以 --format=esm 构建，含 export），必须
		// type="module" 加载，否则按经典脚本解析报 "Unexpected keyword
		// 'export'"。module 脚本按文档顺序执行：runtime.js（head 内先于
		// dsh bundle）执行完设置 window.wails；bridge（经典内联、解析期
		// 即接管 localStorage）的读取走种子缓存，不依赖 wails 时序。
		bridge := strings.Replace(bridgeTemplate, seedMarker, g.seedJSON(), 1)
		scripts := "<script " + marker + " type=\"module\" src=\"/wails/runtime.js\"></script>" +
			"<script " + marker + ">" + bridge + "</script>"
		return html[:idx+6] + scripts + html[idx+6:]
	}
	return html
}

// seedJSON 把共享存储快照序列化为有序 [key, value] 对数组（bridge 种子）。
func (g *Gateway) seedJSON() string {
	g.mu.RLock()
	seed := g.seed
	g.mu.RUnlock()
	if seed == nil {
		return "[]"
	}
	state, order := seed()
	var b strings.Builder
	b.WriteByte('[')
	first := true
	for _, k := range order {
		v, ok := state[k]
		if !ok {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		kj, _ := json.Marshal(k)
		vj, _ := json.Marshal(v)
		b.WriteByte('[')
		b.Write(kj)
		b.WriteByte(',')
		b.Write(vj)
		b.WriteByte(']')
	}
	b.WriteByte(']')
	return b.String()
}

// Start 启动 127.0.0.1:0（OS 分配端口）的网关。targetHost 为 dsh 后端
// 地址（纯 host 或完整 URL，可后续 SetTarget 更新）。ctx 取消时关闭服务。
func Start(targetHost string, ctx context.Context) (*Gateway, error) {
	target, err := parseTarget(targetHost)
	if err != nil {
		return nil, fmt.Errorf("parse target %s: %w", targetHost, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("gateway listen: %w", err)
	}
	g := &Gateway{
		ln:     ln,
		target: target,
		nonce:  fmt.Sprintf("%d", os.Getpid()),
	}
	g.srv = &http.Server{Handler: g}
	go func() {
		<-ctx.Done()
		_ = g.srv.Close()
	}()
	go func() {
		if err := g.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("gateway: serve: %v", err)
		}
	}()
	return g, nil
}
