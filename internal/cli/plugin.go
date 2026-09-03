// `plugin add` 子命令：代理 dsh 的 plugin add，但不安装到全局 DSH_HOME，
// 而是修改工作区的 dsh.profile.bundles。复用 dev 的运行时布局
// （.dsh-store + dsh 原生管理的 profiles/web），调用工作区闭包里的
// `dsh plugin --profile web add <pkg...>` 完成 pnpm add 与 bundles reconcile。
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/pm"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/profile"
)

// PluginAdd 代理 `dsh plugin --profile web add <pkg...>`，目标为工作区。
// pnpm add 与 bundles reconcile 由工作区闭包里的 dsh 完成。
func PluginAdd(ws string, pkgs []string, skipInstall bool) error {
	_, ws, _, err := loadWorkspace(ws)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("缺插件包名；用法：dsh-web-desktopify plugin add [--workspace=<path>] <package...>")
	}

	// 1) 工程文件兜底 + 未安装时 pnpm install（复用工作区已有安装）。
	if _, err := profile.Ensure(ws, skipInstall); err != nil {
		return err
	}

	// 2) 构造 dev 运行时 DSH_HOME（与 dev 一致）：工作区 .dsh-store，
	//    profiles/web 由 dsh 原生管理（只补缺失，不重建——dev 可能
	//    正在运行）。
	homeDir, err := ensureDevHome(ws)
	if err != nil {
		return err
	}

	// 3) 调用工作区闭包里的 dsh（与 dev 相同）。
	dshBin := dshBin(ws)
	if _, err := os.Stat(dshBin); err != nil {
		return fmt.Errorf("工作区未安装 dsh（%s）；先 pnpm install 或去掉 --skip-install", dshBin)
	}

	fmt.Printf("==> plugin add %s（工作区 %s）\n", strings.Join(pkgs, " "), ws)
	args := append([]string{"plugin", "--profile", config.ProfileName, "add"}, pkgs...)
	cmd := exec.Command(dshBin, args...)
	cmd.Env = withEnv(os.Environ(), "DSH_HOME", homeDir)
	// dsh 内部用 PATH 上的 pnpm 跑 add：优先放入真实 pnpm，避免命中 nub shim。
	if bin, err := pm.Bin(); err == nil {
		cmd.Env = prependPath(cmd.Env, filepath.Dir(bin))
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s（DSH_HOME=%s）\n", cmd.String(), homeDir)
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("dsh plugin add: %w", err)
	}

	// 4) 汇报 reconcile 后的 bundle 列表。
	if cfg, err := config.Load(ws); err == nil {
		fmt.Printf("==> bundles: [%s]\n", strings.Join(cfg.Bundles, ", "))
	}
	return nil
}

// withEnv 返回 env 的副本，其中 key 的值替换为 value（先移除旧条目再追加）。
func withEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	prefix := key + "="
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return append(out, prefix+value)
}

// prependPath 返回 env 的副本，把 dir 放到 PATH 最前。
func prependPath(env []string, dir string) []string {
	old := ""
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if after, ok := strings.CutPrefix(e, "PATH="); ok {
			old = after
			continue
		}
		out = append(out, e)
	}
	return append(out, "PATH="+dir+string(os.PathListSeparator)+old)
}
