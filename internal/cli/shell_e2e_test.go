package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/build"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
)

// TestShellBuildE2E 验证 buildShell 能解出内嵌源码、动态生成 go.mod 并
// go build 出壳二进制（产物写入调用方提供的暂存目录）。解出的源码是
// 构建中间产物：构建完成后 shell-src 被清理，build/ 下不留中间目录。
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
	// 解出目录是中间产物：构建完成后应被清理。
	srcDir := filepath.Join(build.BuildDir(ws), "shell-src")
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Fatalf("shell-src 应被清理（中间产物不留盘）: %v", err)
	}
	// 解出内容本身由 materializeShellSrc 单测覆盖（go.mod 布局与
	// 绑定 FQN 稳定），此处验证清理语义。
}
