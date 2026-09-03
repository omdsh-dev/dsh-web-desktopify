package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/bundle"
)

// TestToolFingerprint：源码树完整时产出 src 指纹且稳定；源码树缺失/不
// 完整（go install 后脱离源码树、CI 结构不完整）时不崩溃（回退 VCS 或
// 空串）。
func TestToolFingerprint(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal", "cmd", "pkg/shell"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "a.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f1 := toolFingerprint(root)
	if f1 == "" || !strings.HasPrefix(f1, "src:") {
		t.Fatalf("完整源码树应产出 src 指纹，得到 %q", f1)
	}
	if f2 := toolFingerprint(root); f2 != f1 {
		t.Fatalf("指纹应稳定：%s != %s", f1, f2)
	}

	// 源码树缺失（go install 后脱离源码树）：不崩溃。
	_ = toolFingerprint(filepath.Join(t.TempDir(), "no-such-root"))

	// 源码树不完整（如 CI 中 desktop/ 缺 internal/）：不崩溃。
	partial := t.TempDir()
	if err := os.MkdirAll(filepath.Join(partial, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = toolFingerprint(partial)
}

// TestEnsureDevHome：dev home 是工作区本地 .dsh-store，构造幂等且保留
// 已有数据（不清理）。
func TestEnsureDevHome(t *testing.T) {
	ws := t.TempDir()
	home, err := ensureDevHome(ws)
	if err != nil {
		t.Fatalf("ensureDevHome: %v", err)
	}
	if home != devHome(ws) {
		t.Fatalf("home 应为 %s，得到 %s", devHome(ws), home)
	}

	// 已有数据保留：写入 settings.yaml 后再次构造不清理。
	if err := os.WriteFile(filepath.Join(home, "settings.yaml"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureDevHome(ws); err != nil {
		t.Fatalf("二次构造: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "settings.yaml"))
	if err != nil || string(raw) != "keep" {
		t.Fatalf("不应清空已有数据（%q, %v）", raw, err)
	}
}

// TestWorkspaceHashIgnoresDevStore：.dsh-store（dev 运行时目录）不参与
// 工作区 hash——dev 会话数据变化不会破坏 bundle 增量缓存。白名单模式下
// .dsh-store 不在白名单，天然排除（用与 Bundle 一致的 ignored 判定）。
func TestWorkspaceHashIgnoresDevStore(t *testing.T) {
	ws := t.TempDir()
	for _, f := range []string{"package.json", "cordis.patch.yml"} {
		if err := os.WriteFile(filepath.Join(ws, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ignored := bundle.SeedIgnored(nil)
	h1, err := workspaceHash(ws, hashSkip, ignored)
	if err != nil {
		t.Fatal(err)
	}
	// 写入 dev 运行时数据（模拟一次 dev 会话）。
	if err := os.MkdirAll(filepath.Join(ws, ".dsh-store", "profiles", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".dsh-store", "settings.yaml"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := workspaceHash(ws, hashSkip, ignored)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf(".dsh-store 不应影响工作区 hash：%s != %s", h1, h2)
	}
}

// TestEnsureDevHomeKeep：重复构造幂等，不重建已有数据。
func TestEnsureDevHomeKeep(t *testing.T) {
	ws := t.TempDir()
	home, err := ensureDevHome(ws)
	if err != nil {
		t.Fatalf("首次构造: %v", err)
	}

	// 再次调用（幂等）：不报错、不重建已有数据。
	if err := os.WriteFile(filepath.Join(home, "settings.yaml"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureDevHome(ws); err != nil {
		t.Fatalf("二次构造: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "settings.yaml"))
	if err != nil || string(raw) != "keep" {
		t.Fatalf("不应清空已有数据（%q, %v）", raw, err)
	}
}
