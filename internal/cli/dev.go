package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/pm"
)

// devHome 是 dev/plugin 共用的运行时 DSH_HOME：工作区本地目录
// <ws>/.dsh-store（不触碰打包应用使用的全局 XDG 数据目录）。
func devHome(ws string) string {
	return filepath.Join(ws, ".dsh-store")
}

// dshBin 返回工作区闭包里的 dsh 可执行：向上找第一个 node_modules/.bin
// 里有 dsh 的目录。找不到返回工作区路径（报错信息用）。
func dshBin(ws string) string {
	dir, err := filepath.Abs(ws)
	if err != nil {
		dir = ws
	}
	for {
		bin := filepath.Join(dir, "node_modules", ".bin", "dsh")
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join(ws, "node_modules", ".bin", "dsh")
		}
		dir = parent
	}
}

// ensureDevHome 构造 dev 运行时 DSH_HOME 布局：profiles/web 是 dsh 原生
// 管理的独立 profile 目录（dsh 首次启动时 initProfile 生成），工作区依赖
// 经 `dsh plugin add <pkg>@link:<ws>` 链接进 profile。dev 会话数据
// （settings.yaml、认证、storages 等）在 home 根，跨启动保留。
func ensureDevHome(ws string) (string, error) {
	homeDir := devHome(ws)
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return "", fmt.Errorf("构造 dev home %s: %w", homeDir, err)
	}
	return homeDir, nil
}

// ensureDevProfile 确保 dev 的 web profile 已初始化并把工作区依赖链接
// 进去（dsh 原生机制：`dsh plugin --profile web add <pkg>@link:<ws>`）。
// 幂等：profile 已存在且依赖已链接时跳过。返回 profile 目录。
func ensureDevProfile(ws, homeDir string) (string, error) {
	profileDir := filepath.Join(homeDir, "profiles", config.ProfileName)
	cfg, err := config.Load(ws)
	if err != nil {
		return "", err
	}
	// 依赖已全部链接（profile 的 package.json 里每个工作区依赖都带
	// link: 前缀）时跳过，避免每次 dev 都跑 pnpm add。
	if linked, err := devProfileLinked(profileDir, cfg); err == nil && linked {
		return profileDir, nil
	}
	bin := dshBin(ws)
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("工作区未安装 dsh（%s）；先 pnpm install 或去掉 --skip-install", bin)
	}
	// 工作区依赖（dependencies + peerDependencies）全部以 link: 形式
	// 加入 profile，指向每个包在工作区闭包里的实际目录（与 dsh-custom
	// 的 dev 脚本一致：import.meta.resolve 解析包实体后 link）；dsh
	// 本身在 peerDependencies，链接后 profile 的闭包解析到工作区安装
	// 的 dsh。
	var specs []string
	for name := range cfg.Dependencies {
		dir, err := resolveLinkedPkg(ws, name)
		if err != nil {
			return "", err
		}
		specs = append(specs, name+"@link:"+dir)
	}
	if len(specs) == 0 {
		return profileDir, nil
	}
	args := append([]string{"plugin", "--profile", config.ProfileName, "add"}, specs...)
	cmd := exec.Command(bin, args...)
	cmd.Env = withEnv(os.Environ(), "DSH_HOME", homeDir)
	// dsh 内部用 PATH 上的 pnpm 跑 add：优先放入真实 pnpm，避免命中 nub shim。
	if pnpmBin, err := pm.Bin(); err == nil {
		cmd.Env = prependPath(cmd.Env, filepath.Dir(pnpmBin))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s（DSH_HOME=%s）\n", cmd.String(), homeDir)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("dsh plugin add: %w", err)
	}
	return profileDir, nil
}

// resolveLinkedPkg 解析依赖包在工作区闭包里的实际目录（解引用 symlink，
// 与 dev.ts 的 import.meta.resolve 语义一致）：先找工作区自身
// node_modules，再向上找 workspace 根（monorepo 内嵌工作区时 hoisted）。
func resolveLinkedPkg(ws, name string) (string, error) {
	dir, err := filepath.Abs(ws)
	if err != nil {
		dir = ws
	}
	for {
		pkg := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
		if info, err := os.Stat(pkg); err == nil && info.IsDir() {
			real, err := filepath.EvalSymlinks(pkg)
			if err != nil {
				return "", fmt.Errorf("解析 %s: %w", pkg, err)
			}
			return real, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("工作区闭包未安装 %s（%s 及其祖先的 node_modules 均缺失）", name, ws)
		}
		dir = parent
	}
}

// devProfileLinked 报告 profile 是否已把工作区全部依赖以 link: 形式装好。
// profile 未初始化（package.json 缺失）时返回 false。
func devProfileLinked(profileDir string, cfg *config.Config) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(profileDir, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var m struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return false, err
	}
	for name := range cfg.Dependencies {
		spec, ok := m.Dependencies[name]
		if !ok || !strings.HasPrefix(spec, "link:") {
			return false, nil
		}
	}
	return true, nil
}

// runWeb 启动 dsh web（DSH_HOME=homeDir，cwd=工作区），解析就绪 URL 后
// 返回并保持前台运行（dsh 的 stdout/stderr 透传，Ctrl+C 退出）。dsh web
// 就绪行：`dsh web: http://127.0.0.1:<port>`。NODE_OPTIONS=--import=tsx/esm
// 让 dsh 的 TS 入口（lib/bin.js 经 tsx 加载）在 Node 26 原生 TS 支持之外
// 也能跑（与 dsh-custom 的 dev 脚本一致）；cwd 设为工作区使 tsx 从工作区
// 闭包解析。
func runWeb(dshBin, ws, homeDir string) (string, error) {
	cmd := exec.Command(dshBin, "web", "--port", "0", "--no-open")
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "DSH_HOME="+homeDir, "NODE_OPTIONS=--import=tsx/esm")
	fmt.Printf("==> exec: %s（DSH_HOME=%s, cwd=%s）\n", cmd.String(), homeDir, ws)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start dsh web: %w", err)
	}

	// 信号兜底：Ctrl+C / kill 时主动终止 dsh web，避免遗留孤儿后端。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "\n收到 %v，停止 dsh web\n", sig)
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		os.Exit(130)
	}()

	urlCh := make(chan string, 1)
	go func() {
		defer close(urlCh)
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return // EOF（dsh 退出）或读错误
			}
			fmt.Print(line)
			if after, ok := strings.CutPrefix(line, "dsh web: "); ok {
				urlCh <- strings.TrimSpace(after)
			}
		}
	}()

	select {
	case u := <-urlCh:
		if u == "" {
			cmd.Wait()
			return "", fmt.Errorf("dsh web 未输出就绪 URL 即退出")
		}
		// 前台保持：阻塞等待 dsh web 退出，避免后端残留为孤儿进程。
		return u, cmd.Wait()
	case err := <-waitErr(cmd):
		return "", fmt.Errorf("dsh web 退出: %w", err)
	}
}

func waitErr(cmd *exec.Cmd) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- cmd.Wait() }()
	return ch
}

// openURL 用系统默认浏览器打开 URL（平台命令）。
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	fmt.Printf("==> exec: %s\n", cmd.String())
	return cmd.Start()
}
