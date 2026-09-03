// Package profile 管理工作区的依赖闭包（工作区根即 profile 内容）。
// 用户可直接在工作区 pnpm install，CLI 复用这份安装（SEA 闭包 / bundle
// 种子）；工程文件缺失时从内嵌模板兜底生成。
package profile

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/pm"
)

//go:embed all:templates
var templates embed.FS

// DshClosureDir 向上查找第一个 node_modules 里有 dsh 主包的目录（工作区
// 自身或祖先；monorepo 内嵌工作区时 hoisted 到根）。找不到返回空串。
func DshClosureDir(ws string) string {
	dir, err := filepath.Abs(ws)
	if err != nil {
		dir = ws
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "package.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Installed 报告闭包是否已安装：工作区或向上最近的 workspace 根的
// node_modules 中存在 dsh 主包。
func Installed(ws string) bool {
	return DshClosureDir(ws) != ""
}

// Ensure 确保工程文件齐全（缺失则从模板兜底生成），未安装时在工作区
// 运行 pnpm install。返回工作区路径。
func Ensure(ws string, skipInstall bool) (string, error) {
	dir := ws
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// 工程文件兜底：缺失时从模板生成。pnpm-workspace.yaml 只在独立工作区
	// （ws 自身就是 workspace 根）兜底——monorepo 内嵌工作区（根在上层）
	// 的 workspace 配置在根，工作区自身写一份会污染工程文件（pnpm 按
	// 最近的 workspace 配置解析，工作区这份会遮蔽根配置）。
	wsRoot := config.WorkspaceRoot(ws)
	entries, err := templates.ReadDir("templates")
	if err != nil {
		return "", fmt.Errorf("read embedded templates: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if e.Name() == "pnpm-workspace.yaml" && wsRoot != dir {
			continue
		}
		target := filepath.Join(dir, e.Name())
		if _, err := os.Stat(target); err == nil {
			continue
		}
		data, err := templates.ReadFile("templates/" + e.Name())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}

	if !Installed(ws) && !skipInstall {
		if err := Install(dir, false); err != nil {
			return "", err
		}
	}
	if !Installed(ws) {
		return "", fmt.Errorf("闭包未安装（%s 及其祖先的 node_modules/@deepseek-ai/dsh 均缺失）；先在工作区执行 pnpm install 或 bundle", dir)
	}
	return dir, nil
}

// Install 在 profile 目录运行 pnpm install（增量，已有安装时快速收敛）。
func Install(dir string, skip bool) error {
	if skip {
		return nil
	}
	fmt.Printf("==> pnpm install（%s）\n", dir)
	cmd, err := pm.Command("install")
	if err != nil {
		return err
	}
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> exec: %s（cwd %s）\n", cmd.String(), dir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pnpm install（%s）: %w", dir, err)
	}
	return nil
}

// DshPkgDir 返回已安装的 @deepseek-ai/dsh 主包目录。
func DshPkgDir(profileDir string) string {
	return filepath.Join(profileDir, "node_modules", "@deepseek-ai", "dsh")
}

// Version 返回已安装 dsh 的版本号（读主包 package.json）。
func Version(profileDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(DshPkgDir(profileDir), "package.json"))
	if err != nil {
		return "", err
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	return m.Version, nil
}

// ClosureFingerprint 返回闭包顶层包清单的稳定指纹（包名+版本排序后
// 聚合 sha256）。node_modules 缺失时返回空指纹。
func ClosureFingerprint(profileDir string) (string, error) {
	nmDir := filepath.Join(profileDir, "node_modules")
	entries, err := os.ReadDir(nmDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	type pkgID struct{ name, version string }
	var ids []pkgID
	for _, e := range entries {
		// 安装簿记（.bin/.pnpm/.modules.yaml 等）与散文件不是包。
		if strings.HasPrefix(e.Name(), ".") || !e.IsDir() {
			continue
		}
		dirs := []string{e.Name()}
		if strings.HasPrefix(e.Name(), "@") {
			subs, err := os.ReadDir(filepath.Join(nmDir, e.Name()))
			if err != nil {
				return "", err
			}
			dirs = dirs[:0]
			for _, s := range subs {
				if s.IsDir() && !strings.HasPrefix(s.Name(), ".") {
					dirs = append(dirs, filepath.ToSlash(filepath.Join(e.Name(), s.Name())))
				}
			}
		}
		for _, d := range dirs {
			raw, err := os.ReadFile(filepath.Join(nmDir, d, "package.json"))
			if err != nil {
				return "", fmt.Errorf("读 %s/package.json: %w", d, err)
			}
			var m struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				return "", fmt.Errorf("解析 %s/package.json: %w", d, err)
			}
			ids = append(ids, pkgID{name: d, version: m.Version})
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].name < ids[j].name })
	h := sha256.New()
	for _, id := range ids {
		io.WriteString(h, id.name)
		h.Write([]byte{0})
		io.WriteString(h, id.version)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
