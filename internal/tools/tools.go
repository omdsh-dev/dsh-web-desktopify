// Package tools 管理构建工具链（工作区 node_modules/.dsh-web-desktopify/
// tools/）。构建工具（tsdown 打包 SEA）按需安装，与工作区依赖解耦。
// 工程文件模板以 go:embed 内嵌在 templates/ 下。
//
// 工具链目录带状态记录 tools/state.json（安装时间与工具版本），供
// 回溯与清理判断；目录可整体删除，下次构建自动重装。
package tools

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/pm"
)

//go:embed all:templates
var templates embed.FS

// DirName 是工具链目录名。
const DirName = "tools"

// stateFile 是工具链状态记录文件名（tools/state.json）。
const stateFile = "state.json"

// State 是工具链的状态记录。
type State struct {
	// InstalledAt 是安装时间（RFC3339）。
	InstalledAt string `json:"installedAt"`
	// Tools 是已安装工具及其版本（tsdown 等）。
	Tools map[string]string `json:"tools"`
}

// Dir 返回工具链目录（位于工作区 node_modules/.dsh-web-desktopify/tools）。
func Dir(ws string) string {
	return filepath.Join(config.BundleRoot(ws), DirName)
}

// StatePath 返回工具链状态记录路径。
func StatePath(ws string) string {
	return filepath.Join(Dir(ws), stateFile)
}

// Ensure 确保工具链已安装，返回工具链目录。已安装（tsdown 可执行存在）
// 时跳过安装（状态记录缺失时幂等补写）。
func Ensure(ws string) (string, error) {
	dir := Dir(ws)
	bin := filepath.Join(dir, "node_modules", ".bin", "tsdown")
	if _, err := os.Stat(bin); err == nil {
		fmt.Printf("==> 工具链已安装（复用 %s）\n", dir)
		writeState(ws, bin)
		return dir, nil
	}
	fmt.Printf("==> 安装工具链到 %s\n", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir tools %s: %w", dir, err)
	}
	entries, err := templates.ReadDir("templates")
	if err != nil {
		return "", fmt.Errorf("read embedded templates: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := templates.ReadFile("templates/" + e.Name())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}
	cmd, err := pm.Command("install")
	if err != nil {
		return "", err
	}
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s（cwd %s）\n", cmd.String(), dir)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pnpm install（%s）: %w", dir, err)
	}
	writeState(ws, bin)
	fmt.Printf("==> 工具链就绪: %s\n", bin)
	return dir, nil
}

// writeState 写工具链状态记录（安装时间 + 工具版本）。已存在时跳过
// （保留首次安装时间）；tsdown --version 失败不影响状态写入。
func writeState(ws, bin string) {
	path := StatePath(ws)
	if _, err := os.Stat(path); err == nil {
		return
	}
	state := State{
		InstalledAt: time.Now().Format(time.RFC3339),
		Tools:       map[string]string{},
	}
	if out, err := exec.Command(bin, "--version").Output(); err == nil {
		state.Tools["tsdown"] = strings.TrimSpace(string(out))
	}
	if raw, err := json.MarshalIndent(state, "", "  "); err == nil {
		_ = os.WriteFile(path, append(raw, '\n'), 0o644)
	}
}

// Run 运行工具链里已安装的 bin（如 tsdown、node 脚本），cwd 为 dir。
func Run(ws, dir, bin string, args ...string) error {
	path := filepath.Join(Dir(ws), "node_modules", ".bin", bin)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("tools bin %s 缺失（先 Ensure）: %w", bin, err)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s（cwd %s）\n", cmd.String(), dir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", bin, err)
	}
	return nil
}
