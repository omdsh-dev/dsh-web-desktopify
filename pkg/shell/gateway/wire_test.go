package gateway

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestWailsWire 端到端接线契约：application.New 注入 Transport 后，网关的
// /wails/runtime.js 由 wails assetserver 伺服（200），/wails/runtime 已有
// MessageProcessor（不再是 503）。防止回归到"supervise 自建第二个网关、
// 窗口经未接线网关加载导致 runtime.js 503、window.wails 缺失"的双网关结构
// ——页面必须由被 wails 接线的这同一个网关伺服。
func TestWailsWire(t *testing.T) {
	ctx := t.Context()

	gw, err := Start("127.0.0.1:1", ctx)
	if err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer gw.srv.Close()
	base := "http://127.0.0.1:" + strconv.Itoa(gw.Port())

	application.New(application.Options{
		Name:      "gateway-wire-test",
		Transport: NewTransport(gw),
	})

	// runtime.js：ServeAssets 注入的 wails assetserver 伺服。
	resp, err := http.Get(base + "/wails/runtime.js")
	if err != nil {
		t.Fatalf("GET runtime.js: %v", err)
	}
	head := make([]byte, 32)
	n, _ := resp.Body.Read(head)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runtime.js 应 200，got %d", resp.StatusCode)
	}
	if n == 0 {
		t.Fatal("runtime.js 内容不应为空")
	}

	// IPC：Transport.Start 已注入 MessageProcessor，不再 503
	// （"wails runtime not ready" 表示 proc 未注入）。
	resp2, err := http.Post(base+"/wails/runtime", "application/json",
		strings.NewReader(`{"object":0,"method":0}`))
	if err != nil {
		t.Fatalf("POST runtime: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusServiceUnavailable {
		t.Fatal("IPC 不应 503（MessageProcessor 未注入？）")
	}
}
