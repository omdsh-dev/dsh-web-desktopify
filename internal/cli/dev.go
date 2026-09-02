package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
)

// devHome 是 dev/plugin 共用的运行时 DSH_HOME：工作区本地临时目录
// <ws>/.dsh-store（不触碰打包应用使用的全局 XDG 数据目录）。
func devHome(ws string) string {
	return filepath.Join(ws, ".dsh-store")
}

// dshBin 返回工作区闭包里的 dsh 可执行：向上找第一个 node_modules/.bin
// 里有 dsh 的目录（工作区自身或 workspace 根——monorepo 内嵌工作区时
// dsh 在 peerDependencies，hoisted 到根）。找不到返回工作区路径（报错
// 信息用）。
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

// ensureDevHome 构造 dev 运行时 DSH_HOME 布局。profiles/web 整目录软链
// 到工作区（快，不复制闭包）——dsh 的 profile 加载器写回 manifest、建
// pnpm-workspace.yaml 等会穿透到工作区，其中 pnpm-workspace.yaml 由
// cleanupDevWorkspace 在退出时删除（仅当启动时工作区本来没有）。
// 不清理 .dsh-store：dev 会话数据（settings.yaml、认证、storages 等）
// 跨启动保留。
func ensureDevHome(ws string) (string, error) {
	homeDir := devHome(ws)
	if err := os.MkdirAll(filepath.Join(homeDir, "profiles"), 0o755); err != nil {
		return "", fmt.Errorf("构造 dev home %s: %w", homeDir, err)
	}
	profileLink := filepath.Join(homeDir, "profiles", config.ProfileName)
	if err := ensureProfileLink(profileLink, ws); err != nil {
		return "", err
	}
	return homeDir, nil
}

// ensureProfileLink 确保 profiles/web 符号链接指向工作区：不存在则创建，
// 已存在但指向别处时拒绝。
func ensureProfileLink(link, ws string) error {
	info, err := os.Lstat(link)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s 已存在但不是符号链接；请手动处理后再试", link)
		}
		got, err := filepath.EvalSymlinks(link)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", link, err)
		}
		want, err := filepath.EvalSymlinks(ws)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", ws, err)
		}
		if got != want {
			return fmt.Errorf("%s 指向 %s，不是工作区 %s；请手动处理后再试", link, got, ws)
		}
		return nil
	case os.IsNotExist(err):
		if err := os.Symlink(ws, link); err != nil {
			return fmt.Errorf("构造 profiles/web 链接: %w", err)
		}
		return nil
	default:
		return err
	}
}

// fileExists 报告路径是否为已存在的文件。
func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// cleanupDevWorkspace 删除 dsh 在 dev/plugin 运行期间写入工作区的
// pnpm-workspace.yaml（仅当启动时工作区本来没有该文件——monorepo 内嵌
// 工作区的 workspace 配置在根，工作区自身不该有）。
func cleanupDevWorkspace(ws string, hadWorkspace bool) {
	if hadWorkspace {
		return
	}
	if err := os.Remove(filepath.Join(ws, "pnpm-workspace.yaml")); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[warn] 清理 pnpm-workspace.yaml 失败: %v\n", err)
	}
}

// runWeb 启动 dsh web（DSH_HOME=homeDir），解析就绪 URL 后返回并保持
// 前台运行（dsh 的 stdout/stderr 透传，Ctrl+C 退出）。dsh web 就绪行：
// `dsh web: http://127.0.0.1:<port>`。
func runWeb(dshBin, homeDir string) (string, error) {
	cmd := exec.Command(dshBin, "web", "--port", "0", "--no-open")
	cmd.Env = append(os.Environ(), "DSH_HOME="+homeDir)
	fmt.Printf("==> exec: %s（DSH_HOME=%s）\n", cmd.String(), homeDir)
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
