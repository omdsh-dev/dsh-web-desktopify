// SEA 闭包部署：用 pnpm 官方机制从 workspace 导出生产依赖闭包，替代
// 手工复制 node_modules（后者在 monorepo 内嵌工作区会遇到 symlink 环 /
// 层级丢失 / devDeps 混入等问题）。
//
// 流程（与 dsh-web-desktopify 工作区约定一致）：
//  1. 向上定位最近的 pnpm-workspace.yaml（monorepo 内嵌工作区时在仓库根）；
//  2. 在 workspace 根跑 `pnpm --filter=<包名> deploy --prod <staging>`，
//     导出该包的生产依赖闭包（package.json 依赖改写为 file: 指向本地包，
//     node_modules 按 pnpm 布局安装）；
//  3. deploy 生成的 pnpm-workspace.yaml 会含 allowBuilds 占位符
//     （`set this to true or false`）且缺 minimumReleaseAge——手动修正为
//     全部放行 + minimumReleaseAge: 0；
//  4. 在 staging 跑 `pnpm install`，补全被 deploy 略过的构建脚本。
package sea

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/pm"
)

// findWorkspaceRoot 向上查找最近的 pnpm-workspace.yaml，返回其目录。
func findWorkspaceRoot(ws string) (string, error) {
	dir, err := filepath.Abs(ws)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("向上未找到 pnpm-workspace.yaml（%s 及其父目录）", ws)
		}
		dir = parent
	}
}

// fixDeployWorkspace 修正 pnpm deploy 生成的 pnpm-workspace.yaml：
// allowBuilds 的 `set this to true or false` 占位符改为 true，补
// minimumReleaseAge: 0（deploy 继承的 minimumReleaseAge 可能阻止安装
// 新版包），并补 autoInstallPeers: true——deploy 生成的配置不继承工作区
// 的 autoInstallPeers，而 dsh-app-boot 等核心包把运行时必需的依赖
// （cordis-plugin-group、dsh-invariants 等）声明为 peerDependencies 且
// 全依赖树无普通依赖兜底，staging 补 install 时缺 autoInstallPeers 会漏装
// 这些 peer，SEA 启动即 ERR_MODULE_NOT_FOUND。
func fixDeployWorkspace(wsFile string) error {
	raw, err := os.ReadFile(wsFile)
	if err != nil {
		return err
	}
	s := string(raw)
	// 占位符 → true（pnpm approve-builds 未决时写入的占位文本）。
	s = strings.ReplaceAll(s, ": set this to true or false", ": true")
	if !strings.Contains(s, "minimumReleaseAge") {
		s = strings.TrimRight(s, "\n") + "\nminimumReleaseAge: 0\n"
	}
	if !strings.Contains(s, "autoInstallPeers") {
		s = strings.TrimRight(s, "\n") + "\nautoInstallPeers: true\n"
	}
	// nodeLinker hoisted：deploy 生成的配置默认 isolated，peer 依赖被装进
	// 依赖者自己的 .pnpm 目录，loader entry 包（dsh-bash-sandbox 等）运行时
	// 从自身目录向上解析 peer 会落空（顶层只放直接依赖）。hoisted 把 peer
	// 解析结果扁平提升到顶层 node_modules，闭包内任意包都能解析到。
	if !strings.Contains(s, "nodeLinker") {
		s = strings.TrimRight(s, "\n") + "\nnodeLinker: hoisted\n"
	}
	return os.WriteFile(wsFile, []byte(s), 0o644)
}

// DeployClosure 在 workspace 根用 pnpm deploy 导出 cfg 的生产依赖闭包到
// dst（调用方提供的暂存目录，pnpm deploy 不自动创建目标目录的父路径，
// 须先手动 MkdirAll），并补 install。完成后 node_modules 位于
// dst/node_modules。
func DeployClosure(ws string, cfg *config.Config, dst string) error {
	absWS, err := filepath.Abs(ws)
	if err != nil {
		return err
	}
	root, err := findWorkspaceRoot(absWS)
	if err != nil {
		return err
	}
	fmt.Printf("==> deploy 闭包（workspace 根 %s）\n", root)
	staging := dst
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}

	// 1) pnpm deploy --prod --ignore-scripts <staging>（在 workspace 根跑）。
	// --ignore-scripts：deploy 生成的 pnpm-workspace.yaml 含 allowBuilds
	// 占位符（`set this to true or false`）会触发 ERR_PNPM_IGNORED_BUILDS；
	// 构建脚本推迟到第 3 步（修正配置后）的 pnpm install 执行。
	// 独立 workspace（ws 就是 workspace 根）直接 deploy 当前包；
	// monorepo 内嵌工作区（根在上层）用 --filter 选择该包。
	bin, err := pm.Bin()
	if err != nil {
		return err
	}
	var args []string
	if root != absWS {
		args = append(args, "--filter="+cfg.Name)
	}
	args = append(args, "deploy", "--prod", "--ignore-scripts", staging)
	cmd, err := pm.Command(args...)
	if err != nil {
		return err
	}
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s（cwd %s）\n", cmd.String(), root)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pnpm deploy（workspace %s）: %w", root, err)
	}
	fmt.Printf("==> pnpm deploy 完成: %s\n", staging)

	// 2) 修正 staging 的 pnpm-workspace.yaml（deploy 生成）。
	wsFile := filepath.Join(staging, "pnpm-workspace.yaml")
	if _, err := os.Stat(wsFile); err == nil {
		if err := fixDeployWorkspace(wsFile); err != nil {
			return fmt.Errorf("fix deploy workspace: %w", err)
		}
		fmt.Printf("==> 修正 deploy workspace 配置（allowBuilds/minimumReleaseAge/autoInstallPeers/nodeLinker）\n")
	}

	// 3) 在 staging 补 pnpm install（--ignore-scripts：deploy 生成的
	// workspace 配置含 allowBuilds 占位符会触发 ERR_PNPM_IGNORED_BUILDS；
	// 闭包从 store 克隆已含构建产物，跳过脚本不影响运行时）。
	fmt.Printf("==> 补 pnpm install（%s）\n", staging)
	installCmd := exec.Command(bin, "install", "--no-frozen-lockfile", "--ignore-scripts")
	installCmd.Dir = staging
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s（cwd %s）\n", installCmd.String(), staging)
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("pnpm install（%s）: %w", staging, err)
	}

	nmDst := filepath.Join(staging, "node_modules")
	if _, err := os.Stat(nmDst); err != nil {
		return fmt.Errorf("deploy 未产出 node_modules: %w", err)
	}
	return nil
}
