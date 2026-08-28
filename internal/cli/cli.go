// Package cli 实现 dsh-web-desktopify 单命令：把工作区（examples/ 下的
// 拍平 desktop 定义）打包为独立自定义桌面。用法见 usage。
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/bundle"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/fsutil"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/profile"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/sea"
)

const usage = `dsh-web-desktopify — 把 dsh 的 --profile web 与 cordis.patch.yml 打包为独立自定义桌面。

用法（go install 后任意目录，或仓库内 go tool）：
  dsh-web-desktopify dev [<workspace>]                基于工作区起 dsh web 并打开浏览器
                                                             （缺省当前目录；非工作区目录自动从模板创建）
  dsh-web-desktopify bundle [--platform=os/arch] [--force] [--install] <workspace>
  dsh-web-desktopify plugin add [--workspace=<path>] <package...>
                                                            代理 dsh plugin add：在工作区跑 pnpm add，
                                                            并把声明 dsh.bundle 的依赖加入
                                                            dsh.profile.bundles（不安装到全局 DSH_HOME）

选项：
  --platform=os/arch   声明目标平台（默认本机；SEA/壳不支持交叉编译）
  --force              忽略构建缓存，全新打包（默认基于工作区 dir hash 增量）
  --install            打包后安装到当前平台（macOS /Applications、
                       Linux XDG data + .desktop、Windows %LOCALAPPDATA%\Programs）
  --skip-install       跳过依赖安装（使用已有安装）
  --workspace=<path>   plugin add 的目标工作区（缺省当前目录）
  --profile=<name>     plugin add 兼容 dsh 写法；desktop 只有 web，仅接受 web

工作区是拍平的 desktop 定义（见 examples/official、examples/custom）：
  package.json       全部配置：name/version/dependencies（npm 语义）、
                     dsh.profile.bundles、dsh.desktop（id/window/icon/dshHome）
  cordis.patch.yml   profile patch 层（dsh 应用在 bundle 层之后）
  icon.svg           应用图标（可选，dsh.desktop.icon 引用）

settings.yaml 等用户运行时数据不属于工作区：打包应用按 dshHome 策略
在 XDG_DATA_HOME/<name>（xdg）生成；dev 使用工作区本地临时目录
.dsh-store（每次 dev 重建，不污染全局数据目录）。

全部产物在仓库根 target/ 下。
`

// Run 执行 CLI，返回进程退出码。
func Run(args []string) int {
	skipInstall := false
	force := false
	install := false
	platform := ""
	workspace := ""
	profileName := ""
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--skip-install":
			skipInstall = true
		case a == "--force":
			force = true
		case a == "--install":
			install = true
		case a == "--platform":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--platform 需要参数（os/arch，如 macos/arm64）")
				return 2
			}
			i++
			platform = args[i]
		case strings.HasPrefix(a, "--platform="):
			platform = strings.TrimPrefix(a, "--platform=")
		case a == "--workspace":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--workspace 需要参数（工作区路径）")
				return 2
			}
			i++
			workspace = args[i]
		case strings.HasPrefix(a, "--workspace="):
			workspace = strings.TrimPrefix(a, "--workspace=")
		case a == "--profile":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--profile 需要参数（desktop 只有 web）")
				return 2
			}
			i++
			profileName = args[i]
		case strings.HasPrefix(a, "--profile="):
			profileName = strings.TrimPrefix(a, "--profile=")
		default:
			rest = append(rest, a)
		}
	}

	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch rest[0] {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "dev":
		ws := "."
		if len(rest) >= 2 {
			ws = rest[1]
		}
		if err := Dev(ws, skipInstall); err != nil {
			fmt.Fprintf(os.Stderr, "dev 失败：%v\n", err)
			return 1
		}
		return 0
	case "bundle":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "用法：dsh-web-desktopify bundle [--platform=os/arch] <workspace>")
			return 2
		}
		if _, err := Bundle(rest[1], platform, force, install, skipInstall); err != nil {
			fmt.Fprintf(os.Stderr, "bundle 失败：%v\n", err)
			return 1
		}
		return 0
	case "plugin":
		if len(rest) < 2 || rest[1] != "add" {
			fmt.Fprintln(os.Stderr, "用法：dsh-web-desktopify plugin add [--workspace=<path>] <package...>")
			return 2
		}
		if profileName != "" && profileName != config.ProfileName {
			fmt.Fprintf(os.Stderr, "desktop 只有 %s profile（--profile=%s 无效）\n", config.ProfileName, profileName)
			return 2
		}
		pkgs := rest[2:]
		if len(pkgs) == 0 {
			fmt.Fprintln(os.Stderr, "用法：dsh-web-desktopify plugin add [--workspace=<path>] <package...>")
			return 2
		}
		ws := workspace
		if ws == "" {
			ws = "." // 缺省当前目录（与 dsh plugin 在 profile 目录操作一致）
		}
		if err := PluginAdd(ws, pkgs, skipInstall); err != nil {
			fmt.Fprintf(os.Stderr, "plugin add 失败：%v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n%s", rest[0], usage)
		return 2
	}
}

// checkPlatform 校验 bundle 目标平台（SEA 与 Wails 壳均不支持交叉编译）。
func checkPlatform(platform string) error {
	if platform == "" {
		return nil
	}
	parts := strings.SplitN(platform, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("--platform 必须是 os/arch 形式（如 macos/arm64），得到 %q", platform)
	}
	if parts[0] != runtime.GOOS || parts[1] != runtime.GOARCH {
		return fmt.Errorf("不支持交叉编译：SEA（node --build-sea）与 Wails 壳只能在本机平台构建（当前 %s/%s，目标 %s）", runtime.GOOS, runtime.GOARCH, platform)
	}
	return nil
}

// Bundle 执行一次完整打包（依赖闭包 → SEA → 壳 → 平台组装），返回产物
// 路径。
//
// 默认基于构建缓存：工作区内容（package.json / cordis.patch.yml /
// pnpm-workspace.yaml / .npmrc / 图标 / pnpm-lock.yaml 等）与上次打包一致
// 时直接复用已有产物；--force 忽略缓存全新打包。
func Bundle(ws, platform string, force, install, skipInstall bool) (string, error) {
	if err := checkPlatform(platform); err != nil {
		return "", err
	}
	root, ws, cfg, err := loadWorkspace(ws)
	if err != nil {
		return "", err
	}

	// 工作区内容白名单：package.json + files 字段（+ node_modules 指纹
	// 单独纳入）。被白名单排除的内容（构建产物、缓存、锁文件等）不参与
	// hash，也不进 DSH_HOME 种子（bundle 内部同规则，见 SeedAllow）。
	allow := bundle.SeedAllow(cfg.Files)
	hashIgnored := func(rel string, isDir bool) bool {
		// node_modules 由 ClosureFingerprint 单独指纹，不参与 dir hash。
		if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") {
			return true
		}
		return !allow(rel, isDir)
	}

	// CLI/壳源码指纹：代码变更后旧产物不再复用（见 toolFingerprint）。
	tool := toolFingerprint(root)

	// 构建缓存：工作区 hash + 闭包指纹 + 平台 + 工具指纹。wsHash 同时作为
	// DSH_HOME 种子的 .seed-hash 指纹。
	statePath := filepath.Join(config.BuildDir(ws, cfg), ".build-state.json")
	wsHash := ""
	if !force {
		wsHash, err = workspaceHash(ws, hashSkip, hashIgnored)
		if err != nil {
			return "", fmt.Errorf("计算工作区 hash: %w", err)
		}
		if state, err := os.ReadFile(statePath); err == nil {
			var st buildState
			if json.Unmarshal(state, &st) == nil && tool != "" && st.Tool == tool && st.Hash == wsHash && st.Platform == platformName() {
				appRoot := bundle.AppRoot(ws, cfg)
				if dirExists(appRoot) {
					fmt.Printf("==> 无变化（%s），复用 %s\n", st.Hash[:12], appRoot)
					if install {
						if err := bundle.Install(appRoot, cfg); err != nil {
							return "", err
						}
					}
					return appRoot, nil
				}
			}
		}
	}

	fmt.Printf("==> 打包 %s（%s %s）\n", cfg.Name, config.ProfileName, cfg.Version)

	// 种子指纹：--force 也计算（写入产物种子，供壳启动比对）。
	if wsHash == "" {
		if wsHash, err = workspaceHash(ws, hashSkip, hashIgnored); err != nil {
			return "", fmt.Errorf("计算工作区 hash: %w", err)
		}
	}

	// 1) SEA 后端。
	seaExe, err := sea.Build(ws, cfg, skipInstall)
	if err != nil {
		return "", err
	}
	fmt.Printf("==> SEA 后端: %s\n", seaExe)

	// 2) 壳二进制（构建输入由 pkg/shell 内嵌，脱离源码树）。
	shellBin, err := buildShell(ws, cfg)
	if err != nil {
		return "", err
	}

	// 3) 平台组装。
	appRoot, err := bundle.Assemble(bundle.Inputs{
		Workspace: ws,
		Cfg:       cfg,
		SeaExe:    seaExe,
		ShellBin:  shellBin,
		SeedHash:  wsHash,
	})
	if err != nil {
		return "", err
	}
	fmt.Printf("==> 产物: %s\n", appRoot)

	// 记录构建状态。
	st := buildState{Hash: wsHash, Platform: platformName(), Tool: tool}
	if raw, err := json.Marshal(st); err == nil {
		if err := os.MkdirAll(config.BuildDir(ws, cfg), 0o755); err == nil {
			_ = os.WriteFile(statePath, raw, 0o644)
		}
	}

	// 4) 安装（可选）。
	if install {
		if err := bundle.Install(appRoot, cfg); err != nil {
			return "", err
		}
	}
	return appRoot, nil
}

// buildState 是构建缓存记录。
type buildState struct {
	Hash     string `json:"hash"`
	Platform string `json:"platform"`
	Tool     string `json:"tool"` // CLI/壳源码指纹（代码变更使旧产物失效）
}

// toolFingerprint 返回构建工具指纹，用于构建缓存失效：CLI/壳代码变更
// 后旧产物不再复用。优先源码树 hash（internal/ + cmd/ + pkg/shell/）；
// 源码树不可用时回退二进制内嵌的 VCS revision；都不可用时返回空串。
func toolFingerprint(root string) string {
	var parts []string
	for _, dir := range []string{"internal", "cmd", "pkg/shell"} {
		h, err := fsutil.DirHash(filepath.Join(root, dir), hashSkip, nil)
		if err != nil {
			parts = nil
			break
		}
		parts = append(parts, h)
	}
	if len(parts) == 3 {
		return "src:" + strings.Join(parts, ":")
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev != "" {
			return "vcs:" + rev + ":" + modified
		}
	}
	return ""
}

// workspaceHash 计算工作区构建缓存指纹：工程文件 dir hash + 闭包顶层包
// 清单指纹（node_modules 变化单独纳入，避免复用与当前闭包不一致的旧产物）。
func workspaceHash(ws string, hashSkip map[string]bool, hashIgnored func(rel string, isDir bool) bool) (string, error) {
	h, err := fsutil.DirHash(ws, hashSkip, hashIgnored)
	if err != nil {
		return "", err
	}
	fp, err := profile.ClosureFingerprint(ws)
	if err != nil {
		return "", err
	}
	return h + ":" + fp, nil
}

// hashSkip 是工作区 dir hash 里"总是跳过"的名字（即使 files 白名单
// 列出也不参与 hash）：VCS 元数据与跨平台噪音。node_modules 不在此列
// 的必要性已由白名单 hashIgnored 覆盖（ClosureFingerprint 单独指纹）。
var hashSkip = map[string]bool{
	".git":      true,
	".DS_Store": true,
}

// platformName 返回当前平台的 canonical 名（os/arch）。
func platformName() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// Dev 基于工作区直接起一个 dsh web 并打开浏览器页面（不组装桌面应用）。
// DSH_HOME 为工作区本地临时目录 .dsh-store，profiles/web 符号链接指向
// 工作区；目录还不是工作区时从模板兜底创建工程文件并安装依赖。
func Dev(ws string, skipInstall bool) error {
	_, ws, err := resolveWorkspace(ws)
	if err != nil {
		return err
	}

	if _, err := config.Load(ws); err != nil {
		// 当前目录还不是工作区：从模板兜底创建工程文件并安装依赖。
		fmt.Printf("==> %s 不是工作区，从模板创建工程文件\n", ws)
		if _, err := profile.Ensure(ws, skipInstall); err != nil {
			return err
		}
	}
	_, _, cfg, err := loadWorkspace(ws)
	if err != nil {
		return err
	}

	fmt.Printf("==> dev %s（%s %s）\n", cfg.Name, config.ProfileName, cfg.Version)

	// 1) 工程文件兜底 + 未安装时 pnpm install（复用工作区已有安装）。
	if _, err := profile.Ensure(ws, skipInstall); err != nil {
		return err
	}

	// 2) 构造 dev 运行时 DSH_HOME：工作区 .dsh-store（每次全新重建），
	//    profiles/web → 工作区。
	homeDir, err := ensureDevHome(ws, true)
	if err != nil {
		return err
	}
	fmt.Printf("==> dev home: %s\n", homeDir)

	// 3) 启动 dsh web（工作区闭包里的 dsh），解析就绪 URL。
	dshBin := filepath.Join(ws, "node_modules", ".bin", "dsh")
	if _, err := os.Stat(dshBin); err != nil {
		return fmt.Errorf("工作区未安装 dsh（%s）；先 pnpm install 或去掉 --skip-install", dshBin)
	}
	url, err := runWeb(dshBin, homeDir)
	if err != nil {
		return err
	}

	// 4) 打开浏览器页面。
	fmt.Printf("==> 打开 %s（Ctrl+C 退出）\n", url)
	if err := openURL(url); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] 打开浏览器失败: %v\n", err)
	}
	return nil
}

// loadWorkspace 解析工作区并返回（仓库根, 绝对工作区路径, 配置）。
func loadWorkspace(ws string) (string, string, *config.Config, error) {
	root, ws, err := resolveWorkspace(ws)
	if err != nil {
		return "", "", nil, err
	}
	cfg, err := config.Load(ws)
	if err != nil {
		return "", "", nil, err
	}
	return root, ws, cfg, nil
}

// resolveWorkspace 只解析工作区绝对路径（不要求已是工作区）。
func resolveWorkspace(ws string) (string, string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(ws) {
		if ws == "examples" || strings.HasPrefix(ws, "examples"+string(filepath.Separator)) {
			ws = filepath.Join(root, ws)
		}
	}
	ws, err = filepath.Abs(ws)
	return root, ws, err
}

// repoRoot 返回仓库根（internal/cli 源文件上三级）。DSH_DESKTOP_ROOT
// 可显式覆盖（go install 后使用）。
func repoRoot() (string, error) {
	if v := os.Getenv("DSH_DESKTOP_ROOT"); v != "" {
		return v, nil
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("无法定位仓库根；设置 DSH_DESKTOP_ROOT 或从仓库根运行")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file))), nil
}

// dirExists 报告路径是否为已存在的目录。
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
