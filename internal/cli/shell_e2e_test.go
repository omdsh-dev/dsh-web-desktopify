package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
)

// TestShellBuildE2E 验证 buildShell 能解出内嵌源码、动态生成 go.mod 并
// go build 出壳二进制（产物写入调用方提供的暂存目录）。
func TestShellBuildE2E(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{Name: "e2e-shell"}
	dst := t.TempDir()
	if err := buildShell(ws, cfg, dst); err != nil {
		t.Fatalf("buildShell: %v", err)
	}
	out := filepath.Join(dst, binName())
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("壳二进制不存在: %v", err)
	}
	srcDir := filepath.Join(buildDir(ws), "shell-src")
	gomod, err := os.ReadFile(filepath.Join(srcDir, "go.mod"))
	if err != nil {
		t.Fatalf("读 go.mod: %v", err)
	}
	// 外层模块：独立标识 + replace 指回内层子模块。
	if !strings.HasPrefix(string(gomod), "module dsh-shell") {
		t.Fatalf("外层 go.mod 模块名错误: %s", gomod)
	}
	if !strings.Contains(string(gomod), "replace github.com/omdsh-dev/dsh-web-desktopify => ./pkg/shell") {
		t.Fatalf("外层 go.mod 应含 replace: %s", gomod)
	}
	// 内层子模块：module 名即仓库路径，绑定 FQN 稳定。
	innerGomod, err := os.ReadFile(filepath.Join(srcDir, "pkg", "shell", "go.mod"))
	if err != nil {
		t.Fatalf("读内层 go.mod: %v", err)
	}
	if !strings.HasPrefix(string(innerGomod), "module github.com/omdsh-dev/dsh-web-desktopify") {
		t.Fatalf("内层 go.mod 模块名错误: %s", innerGomod)
	}
	if !strings.Contains(string(innerGomod), "wailsapp/wails/v3") {
		t.Fatalf("内层 go.mod 应含 wails 依赖")
	}
	if !dirExists(filepath.Join(srcDir, "pkg", "shell", "pkg", "shell", "cmd")) {
		t.Fatal("解出的源码应有 cmd/ 目录")
	}
}
