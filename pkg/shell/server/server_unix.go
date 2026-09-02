//go:build !windows

package server

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Name 是壳同目录下 SEA 后端可执行文件名（Unix 无扩展名）。
const Name = "dsh-server"

// rcFileFor 按用户 shell 返回要 source 的配置文件路径（缺失时 source 报错
// 但被重定向吞掉，不影响后续）。
func rcFileFor(shell string) string {
	switch filepath.Base(shell) {
	case "bash":
		return "~/.bashrc"
	case "zsh":
		return "~/.zshrc"
	default:
		return ""
	}
}

// shellQuote 用单引号包裹字符串，供拼进 shell 命令行（路径可能含空格）。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// command 构造 spawn dsh-server 的命令。用户 shell 有对应 rc 文件时先 source
// 它（让后端继承用户终端里的环境变量，如 API key），再 exec dsh-server——
// exec 保持同一进程（PID 不变），守护 wait 语义不受影响；source 输出重定向
// 到 /dev/null，避免污染后端 stdout 的 URL 行。进程放入独立进程组
// （Setpgid）：应用退出时按组终止，保证后端及其子进程整体清理，不留孤儿
// node。ctx 取消不依赖 exec.CommandContext 的异步 kill（只杀直接子进程且
// 时机不受控），由调用方经 Process.Stop 显式终止。
func command(exeDir, profile, port, dshHome string) *exec.Cmd {
	server := filepath.Join(exeDir, Name)
	args := []string{"--profile", profile, "--port", port}

	shell := os.Getenv("SHELL")
	rc := rcFileFor(shell)
	var cmd *exec.Cmd
	if shell != "" && rc != "" {
		cmdline := fmt.Sprintf("source %s >/dev/null 2>&1; exec %s %s",
			rc, shellQuote(server), strings.Join(args, " "))
		cmd = exec.Command(shell, "-c", cmdline)
		log.Printf("server: exec: %s", cmd.String())
	} else {
		cmd = exec.Command(server, args...)
		log.Printf("server: exec: %s", cmd.String())
	}
	cmd.Env = os.Environ()
	if dshHome != "" {
		cmd.Env = append(cmd.Env, "DSH_HOME="+dshHome)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// signal 向 dsh-server 所在进程组发信号（负 PID 覆盖组内全部进程）。
func signal(p *Process, sig syscall.Signal) {
	if p.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, sig)
}

// requestStop 优雅终止后端：SIGTERM 整个进程组，让其走自己的收口路径。
func requestStop(p *Process) {
	signal(p, syscall.SIGTERM)
}

// forceStop 兜底强杀：SIGKILL 整个进程组。
func forceStop(p *Process) {
	signal(p, syscall.SIGKILL)
}

// attachToJob 无操作：Unix 上进程组（Setpgid）即终止边界，无需额外资源。
func attachToJob(_ *Process) error {
	return nil
}
