package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/build"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/fsutil"
	"github.com/omdsh-dev/dsh-web-desktopify/pkg/shell"
)

// buildShell 构建壳二进制（Wails v3）到 dst 目录（调用方提供的暂存
// 目录，完成后由调用方发布进缓存）。构建输入由 shell 包内嵌在 CLI
// 二进制中，运行时解出并动态生成 go.mod 后 go build——不依赖外部源码
// 树，CLI 可 go install 后脱离仓库运行。
func buildShell(ws string, cfg *config.Config, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	binName := "dsh-shell"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	out := filepath.Join(dst, binName)
	fmt.Printf("==> 解出内嵌壳源码\n")
	srcDir, cleanup, err := materializeShellSrc(ws, cfg)
	if err != nil {
		return err
	}
	defer cleanup() // 构建中间产物不留盘（build/ 下只留状态记录与暂存）
	// 用完整 import 路径构建：外层模块经 replace 把仓库路径解析到内层
	// 子模块 pkg/shell/，cmd 包即 github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/cmd。
	cmd := exec.Command("go", "build", "-o", out, "github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/cmd")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// -mod=mod 自动补全 go.sum（go.mod 动态生成）；GOWORK=off 让壳模块
	// 脱离仓库 go.work 单独解析。
	cmd.Env = fsutil.WithEnv(os.Environ(),
		"GOFLAGS", os.Getenv("GOFLAGS")+" -mod=mod",
		"GOWORK", "off",
	)
	fmt.Printf("==> exec: %s（cwd %s）\n", cmd.String(), srcDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build 壳: %w", err)
	}
	return nil
}

// materializeShellSrc 把内嵌的壳构建输入（shell.FS）解出为临时模块根
// （node_modules/.dsh-web-desktopify/build/shell-src/）并动态写入 go.mod，
// 返回该根目录。每次全量重写，保证与二进制内嵌内容一致。调用方完成
// go build 后应调用 cleanup 删除解出目录（构建中间产物不留盘）。
//
// 模块布局：外层 module dsh-shell，内层子模块 pkg/shell/ 声明
// module github.com/omdsh-dev/dsh-web-desktopify，外层经 replace 指回——
// 壳源码的 import 解析为本地子目录，且绑定 FQN（PkgPath.TypeName.Method）
// 稳定为 github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/...。
func materializeShellSrc(ws string, cfg *config.Config) (srcDir string, cleanup func(), err error) {
	srcDir = filepath.Join(build.BuildDir(ws), "shell-src")
	cleanup = func() { _ = os.RemoveAll(srcDir) }
	if err = os.RemoveAll(srcDir); err != nil {
		return "", nil, err
	}
	// 布局：外层模块根 srcDir/（go.mod: module dsh-shell），内层子模块根
	// srcDir/pkg/shell/（go.mod: module github.com/omdsh-dev/dsh-web-desktopify），
	// shell.FS 内容解出到 srcDir/pkg/shell/pkg/shell/——包路径保持
	// github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/...，与绑定 FQN 一致。
	inner := filepath.Join(srcDir, "pkg", "shell", "pkg", "shell")
	if err = writeEmbedDir(shell.FS, ".", inner); err != nil {
		return "", nil, fmt.Errorf("解出壳源码: %w", err)
	}
	innerRoot := filepath.Join(srcDir, "pkg", "shell")
	if err = os.WriteFile(filepath.Join(innerRoot, "go.mod"), []byte(shellInnerGoMod()), 0o644); err != nil {
		return "", nil, fmt.Errorf("写壳内层 go.mod: %w", err)
	}
	if err = os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(shellGoMod()), 0o644); err != nil {
		return "", nil, fmt.Errorf("写壳 go.mod: %w", err)
	}
	return srcDir, cleanup, nil
}

// shellGoMod 返回壳外层模块的 go.mod：module dsh-shell，仓库路径经 replace
// 指回内层子模块 pkg/shell/。
func shellGoMod() string {
	return "module dsh-shell\n" +
		"\n" +
		"go " + strings.TrimPrefix(runtime.Version(), "go") + "\n" +
		"\n" +
		"require github.com/omdsh-dev/dsh-web-desktopify v0.0.0\n" +
		"\n" +
		"replace github.com/omdsh-dev/dsh-web-desktopify => ./pkg/shell\n"
}

// shellInnerGoMod 返回壳内层子模块（pkg/shell/）的 go.mod：module 名即仓库
// 路径，使绑定 FQN 稳定；依赖与主模块一致。
func shellInnerGoMod() string {
	return "module github.com/omdsh-dev/dsh-web-desktopify\n" +
		"\n" +
		"go " + strings.TrimPrefix(runtime.Version(), "go") + "\n" +
		"\n" +
		"require (\n" +
		"\tgithub.com/adrg/xdg v0.5.3\n" +
		"\tgithub.com/wailsapp/wails/v3 v3.0.0-beta.11\n" +
		"\tgolang.org/x/sys v0.47.0\n" +
		")\n"
}

// writeEmbedDir 把 fsys 中 dir 前缀下的全部条目写出到 dst 下（去掉前缀）。
func writeEmbedDir(fsys embed.FS, dir, dst string) error {
	return fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
