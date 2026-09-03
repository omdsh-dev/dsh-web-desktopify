// Package cli 实现 dsh-web-desktopify 单命令：把工作区（examples/ 下的
// 拍平 desktop 定义）打包为独立自定义桌面。用法见 usage。
package cli

import (
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
在 XDG_DATA_HOME/<name>（xdg）生成；dev 使用工作区本地目录
.dsh-store（会话数据跨启动保留，不污染全局数据目录），profiles/web
由 dsh 原生管理（工作区依赖以 link: 形式装入 profile）。

全部产物在 node_modules/.dsh-web-desktopify/ 下（cache/<step>/<digest>/，
node_modules 已被 git 忽略）。
`

// Run 执行 CLI，返回进程退出码。
func Run(args []string) int {
	skipInstall := false
	force := false
	install := false
	emptyHome := false
	platform := ""
	workspace := ""
	profileName := ""
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--empty-home":
			emptyHome = true
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
		if err := Dev(ws, skipInstall, emptyHome); err != nil {
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
// 产物按输入指纹内容寻址缓存（node_modules/.dsh-web-desktopify/cache/
// <step>/<digest>/）：检查只关心目录在不在，不比对状态记录；依赖传导由
// digest 链保证——deploy 重建后其 digest 变化，SEA / 平台组装的 digest
// 随之变化，必然重建。--force 全部重建（deploy 重新 pnpm deploy，不再
// 复用旧依赖闭包）。
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

	if force {
		fmt.Printf("==> --force：忽略构建缓存，全部重建\n")
	}

	// 工作区指纹：deploy 步输入（工程文件 + 闭包清单），同时写入 DSH_HOME
	// 种子供壳启动比对。
	wsHash, err := workspaceHash(ws, hashSkip, hashIgnored)
	if err != nil {
		return "", fmt.Errorf("计算工作区 hash: %w", err)
	}
	parts := strings.SplitN(wsHash, ":", 2)
	fp := ""
	if len(parts) == 2 {
		fp = parts[1]
	}
	if fp == "" {
		fmt.Printf("==> 工作区指纹: %s（闭包未安装，无指纹）\n", shortHash(wsHash))
	} else {
		fmt.Printf("==> 工作区指纹: %s（闭包指纹 %s）\n", shortHash(wsHash), shortHash(fp))
	}

	// ---- 分步 DAG（内容寻址缓存）----
	// 1) deploy 闭包。输入 = 工作区指纹。缓存 = cache/deploy/<digest>/，
	//    命中即目录存在；重建时重新 pnpm deploy（新版本依赖进闭包）。
	deployStep := &buildStep{
		id:    "deploy",
		label: "deploy 闭包",
		input: func() (string, error) { return wsHash, nil },
		run: func(dst string) error {
			if skipInstall {
				return fmt.Errorf("deploy 缓存缺失（--skip-install 下不自动 deploy；先跑一次不带 --skip-install 的 bundle 或 pnpm deploy --filter=%s --prod）", cfg.Name)
			}
			return sea.DeployClosure(ws, cfg, dst)
		},
	}

	// 2) SEA 后端。输入 = 工具链指纹 + deploy digest。deploy 重建后 digest
	//    变化，自动重跑；依赖未变时复用 cache/sea/<digest>/。
	seaStep := &buildStep{
		id:    "sea",
		label: "SEA 后端",
		deps:  []*buildStep{deployStep},
		input: func() (string, error) { return tool, nil },
		run: func(dst string) error {
			return sea.Build(ws, cfg, deployStep.cachePath(ws), dst)
		},
	}

	// 3) 壳二进制。输入 = 工具链指纹；依赖仅用于 digest 传导。
	shellStep := &buildStep{
		id:    "shell",
		label: "壳二进制",
		deps:  []*buildStep{seaStep},
		input: func() (string, error) { return tool, nil },
		run: func(dst string) error {
			return buildShell(ws, cfg, dst)
		},
	}

	// 4) 平台组装。输入 = 平台 + 种子指纹 + SEA digest + 壳 digest。
	assembleStep := &buildStep{
		id:    "assemble",
		label: "平台组装",
		deps:  []*buildStep{seaStep, shellStep},
		input: func() (string, error) { return platformName() + ":" + wsHash, nil },
		run: func(dst string) error {
			return bundle.Assemble(bundle.Inputs{
				Workspace: ws,
				Cfg:       cfg,
				SeaDir:    seaStep.cachePath(ws),
				ShellBin:  filepath.Join(shellStep.cachePath(ws), binName()),
				SeedHash:  wsHash,
			}, dst)
		},
	}

	// 按依赖序执行（deploy → sea → shell → assemble）。
	steps := []*buildStep{deployStep, seaStep, shellStep, assembleStep}
	for _, s := range steps {
		if _, _, err := runStep(ws, s, force); err != nil {
			return "", err
		}
	}

	appRoot := assembleStep.cachePath(ws)
	fmt.Printf("==> 产物: %s\n", appRoot)

	// 安装（可选）。
	if install {
		if err := bundle.Install(appRoot, cfg); err != nil {
			return "", err
		}
	}
	return appRoot, nil
}

// binName 返回壳二进制文件名（平台相关扩展名）。
func binName() string {
	if runtime.GOOS == "windows" {
		return "dsh-shell.exe"
	}
	return "dsh-shell"
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

// shortHash 截断 hash 为 12 位，便于日志对比（不足 12 位原样返回）。
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// Dev 基于工作区直接起一个 dsh web 并打开浏览器页面（不组装桌面应用）。
// DSH_HOME 为工作区本地目录 .dsh-store，profiles/web 由 dsh 原生管理
// （initProfile + plugin add link: 工作区依赖）；目录还不是工作区时从模板
// 兜底创建工程文件并安装依赖。**不清理 .dsh-store**：dev 会话数据跨启动
// 保留。
func Dev(ws string, skipInstall, _ bool) error {
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

	// 2) 构造 dev 运行时 DSH_HOME：工作区 .dsh-store（保留已有数据），
	//    profiles/web 由 dsh 原生管理（initProfile + plugin add link: 工作区
	//    依赖）。
	homeDir, err := ensureDevHome(ws)
	if err != nil {
		return err
	}
	fmt.Printf("==> dev home: %s\n", homeDir)
	if _, err := ensureDevProfile(ws, homeDir); err != nil {
		return err
	}

	// 3) 启动 dsh web（工作区闭包里的 dsh），解析就绪 URL。
	dshBin := dshBin(ws)
	if _, err := os.Stat(dshBin); err != nil {
		return fmt.Errorf("工作区未安装 dsh（%s）；先 pnpm install 或去掉 --skip-install", dshBin)
	}
	url, err := runWeb(dshBin, ws, homeDir)
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
