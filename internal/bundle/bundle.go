// Package bundle 把 SEA 产物、壳二进制与构建出的 DSH_HOME 装配为平台
// 桌面应用与开发布局。全部产物在 target/<name>/ 下：
//
//	macOS   target/<name>/<Name>.app/Contents/{MacOS,Resources,config,node_modules,package.json,dsh-home,Info.plist}
//	Linux   target/<name>/linux/<Name>/{bin,config,node_modules,package.json,dsh-home,share/icons/hicolor}
//	Windows target/<name>/windows/<Name>/{bin,config,node_modules,package.json,dsh-home,dsh.ico}
//	dev     target/<name>/dev/{bin,config,node_modules,package.json,dsh-home}
//
// dsh-home 是打包进应用的 DSH_HOME 种子（profiles/web/ 等，由 profile 包
// 构建），壳在运行时按 appconfig 的 dshHome 策略落位
// （xdg 数据目录 / 固定路径 / 继承环境）。
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/fsutil"
)

// SeedAllow 构造 DSH_HOME 种子的文件白名单判定。白名单 = package.json +
// node_modules + cordis.patch.yml（profile patch 层，恒必需）+ package.json
// 的 files 字段；其余工作区内容（target/、.dsh-store、锁文件、git 元数据
// 等）一律不进种子。返回的回调按 CopyDirDeref / DirHash 的 ignored 语义
// 工作：rel（/ 分隔）命中白名单时返回 true（保留），目录命中即保留整棵
// 子树（files 里列目录的 npm 语义）。
func SeedAllow(files []string) func(rel string, isDir bool) bool {
	allowed := map[string]bool{
		"package.json":     true,
		"node_modules":     true,
		"cordis.patch.yml": true,
	}
	for _, f := range files {
		f = path.Clean(f)
		if f == "." || f == "" || path.IsAbs(f) || strings.HasPrefix(f, "../") {
			continue // 非法 / 越界条目忽略
		}
		allowed[f] = true
	}
	return func(rel string, isDir bool) bool {
		if allowed[rel] {
			return true
		}
		// 祖先目录命中（files: ["lib"] → lib/sub/x.js 也保留）。
		for p := path.Dir(rel); p != "." && p != "/"; p = path.Dir(p) {
			if allowed[p] {
				return true
			}
		}
		return false
	}
}

// appConfig 与壳的 appconfig.json 结构一致（壳读取）。
type appConfig struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Window  struct {
		Width     int `json:"width"`
		Height    int `json:"height"`
		MinWidth  int `json:"minWidth"`
		MinHeight int `json:"minHeight"`
	} `json:"window"`
	Profile string `json:"profile"`
	DSHHome string `json:"dshHome"`
}

// Inputs 是一次装配的全部输入。
type Inputs struct {
	Workspace string // 工作区（target/ 产物根与图标源）
	Cfg       *config.Config
	SeaExe    string // SEA 可执行（sea/bin/dsh）
	ShellBin  string // 壳二进制（go build 壳源码的产物）
	SeedHash  string // 工作区内容 hash（写入种子的 .seed-hash 指纹，壳启动时比对）
}

// AppRoot 返回平台应用的产物根目录（位于工作区 target/ 下）。
func AppRoot(ws string, cfg *config.Config) string {
	build := config.BuildDir(ws, cfg)
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(build, cfg.Name+".app")
	case "linux":
		return filepath.Join(build, "linux", cfg.Name)
	case "windows":
		return filepath.Join(build, "windows", cfg.Name)
	}
	return filepath.Join(build, "app")
}

// BinNames 返回壳与后端文件名（平台相关扩展名）。
func BinNames() (shell, server string) {
	if runtime.GOOS == "windows" {
		return "dsh-shell.exe", "dsh-server.exe"
	}
	return "dsh-shell", "dsh-server"
}

// assembleLayout 装配平台无关的公共布局（bin/ + 资源 + 种子），返回 bin
// 目录。appRoot 由调用方先清理。withSeed=false 时不复制 DSH_HOME 种子
// （dev 布局：运行时 home 由 CLI 单独构造）。
func assembleLayout(in Inputs, appRoot string) (string, error) {
	binDir := filepath.Join(appRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}

	// 壳与 SEA 后端。
	shellName, serverName := BinNames()
	if err := fsutil.CopyFile(in.ShellBin, filepath.Join(binDir, shellName)); err != nil {
		return "", fmt.Errorf("copy shell: %w", err)
	}
	if err := fsutil.CopyFile(in.SeaExe, filepath.Join(binDir, serverName)); err != nil {
		return "", fmt.Errorf("copy dsh-server: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(binDir, serverName), 0o755); err != nil {
			return "", err
		}
	}

	// 壳运行时配置。
	if err := writeAppConfig(binDir, in.Cfg); err != nil {
		return "", fmt.Errorf("write appconfig.json: %w", err)
	}

	// SEA 运行时资源：config/、node_modules/、package.json（从 staging 复制）。
	staging := config.SeaDir(in.Workspace, in.Cfg)
	for _, name := range []string{"config", "node_modules", "package.json"} {
		src := filepath.Join(staging, name)
		if err := fsutil.CopyDir(src, filepath.Join(appRoot, name)); err != nil {
			return "", fmt.Errorf("copy %s: %w", name, err)
		}
	}

	// DSH_HOME 种子：工作区 → appRoot/dsh-home/profiles/web（解引用）。
	// 白名单 = package.json + package.json 的 files 字段；node_modules 不
	// 进种子——dsh 运行时从安装闭包（SEA 的 Contents/node_modules）经
	// 双锚点解析 bundle 依赖，profile 私有 node_modules 只放 pnpm-managed
	// 插件（见 app-boot profile.ts），种子带了会遮蔽安装闭包的解析。
	homeRoot := filepath.Join(appRoot, "dsh-home")
	profileDir := filepath.Join(homeRoot, "profiles", config.ProfileName)
	if err := os.MkdirAll(filepath.Join(homeRoot, "profiles"), 0o755); err != nil {
		return "", err
	}
	allow := SeedAllow(in.Cfg.Files)
	seedIgnored := func(rel string, isDir bool) bool {
		// node_modules 不进种子（运行时从安装闭包解析）。
		if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") {
			return true
		}
		return !allow(rel, isDir)
	}
	if err := fsutil.CopyDirDeref(in.Workspace, profileDir, nil, seedIgnored); err != nil {
		return "", fmt.Errorf("copy dsh-home seed: %w", err)
	}
	// 种子指纹：工作区内容 hash，壳启动时比对，避免每次全量复制闭包。
	if in.SeedHash != "" {
		if err := os.WriteFile(filepath.Join(homeRoot, "profiles", config.ProfileName, ".seed-hash"), []byte(in.SeedHash), 0o644); err != nil {
			return "", fmt.Errorf("write .seed-hash: %w", err)
		}
	}

	return binDir, nil
}

// writeAppConfig 生成壳同目录的 appconfig.json。
func writeAppConfig(binDir string, cfg *config.Config) error {
	var ac appConfig
	ac.Name = cfg.Name
	ac.ID = cfg.Desktop.ID
	ac.Version = cfg.Version
	ac.Window.Width = cfg.Desktop.Window.Width
	ac.Window.Height = cfg.Desktop.Window.Height
	ac.Window.MinWidth = cfg.Desktop.Window.MinWidth
	ac.Window.MinHeight = cfg.Desktop.Window.MinHeight
	ac.Profile = config.ProfileName
	ac.DSHHome = cfg.Desktop.DSHHome
	raw, err := json.MarshalIndent(ac, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(binDir, "appconfig.json"), append(raw, '\n'), 0o644)
}

// Assemble 按当前平台组装应用，返回产物根目录。
func Assemble(in Inputs) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return assembleMacOS(in)
	case "linux":
		return assembleLinux(in)
	case "windows":
		return assembleWindows(in)
	default:
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// AssembleDev 组装开发布局（target/<name>/dev），返回 bin 目录。
// dev 不复制 DSH_HOME 种子：运行时 home 由 CLI 构造（profiles/web 指向
// 工作区），用户在工作区的 pnpm install 结果直接可见。
