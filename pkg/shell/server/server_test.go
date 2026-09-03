package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeServer 生成一个可执行的假 dsh-server：按参数打印就绪行后长驻，
// 收到 SIGTERM（Unix）或 stdin 关闭（Windows）时退出。
func fakeServer(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
echo "dsh web: http://127.0.0.1:58230?token=abc"
trap 'exit 0' TERM
while :; do sleep 1; done
`
	if runtime.GOOS == "windows" {
		script = `@echo off
echo dsh web: http://127.0.0.1:58230?token=abc
pause >nul
`
	}
	path := filepath.Join(dir, "dsh-server")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestStartParsesReadyURL：Start 从后端 stdout 解析就绪 URL（含 query），
// 返回的 Process 可终止。
func TestStartParsesReadyURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假后端脚本为 Unix 写法")
	}
	exeDir := fakeServer(t, t.TempDir())
	ctx := t.Context()

	p, url, err := Start(ctx, exeDir, "web", "0", "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if url != "http://127.0.0.1:58230?token=abc" {
		t.Fatalf("URL 解析错误: %q", url)
	}
	if p.Pid() <= 0 {
		t.Fatal("Pid 应非零")
	}
	// Stop 内部等待进程退出（exitCh 的值被 Stop 消费，这里验证进程已死）。
	p.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(p.Pid(), 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Stop 后进程未退出")
}

// TestStartTimeout：后端不输出就绪行时超时终止并返回错误。
func TestStartTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假后端脚本为 Unix 写法")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dsh-server")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := urlTimeout
	urlTimeout = 200 * time.Millisecond
	defer func() { urlTimeout = old }()

	_, _, err := Start(context.Background(), dir, "web", "0", "")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("应超时报错，得到 %v", err)
	}
}

// TestStartMissingBinary：后端可执行缺失时报错（SHELL 置空走直接 exec
// 路径，Start 立即失败而非经 shell 内部报错）。
func TestStartMissingBinary(t *testing.T) {
	t.Setenv("SHELL", "")
	_, _, err := Start(context.Background(), t.TempDir(), "web", "0", "")
	if err == nil {
		t.Fatal("缺失后端应报错")
	}
}
