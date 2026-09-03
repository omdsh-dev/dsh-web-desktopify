// Package bundle 把 SEA 产物、壳二进制与构建出的 DSH_HOME 装配为平台
// 桌面应用。全部产物在 node_modules/.dsh-web-desktopify/ 下
// （CLI 分步缓存 cache/<step>/<digest>/ 的 assemble 步）：
//
//	macOS   .../cache/assemble/<dg>/<Name>.app/Contents/{MacOS,Resources,config,node_modules,package.json,dsh-home,Info.plist}
//	Linux   .../cache/assemble/<dg>/<Name>/{bin,config,node_modules,package.json,dsh-home,share/icons/hicolor}
//	Windows .../cache/assemble/<dg>/<Name>/{bin,config,node_modules,package.json,dsh-home,dsh.ico}
//
// dsh-home 是打包进应用的 DSH_HOME 种子，壳在运行时按 appconfig 的
// dshHome 策略落位（xdg 数据目录 / 固定路径 / 继承环境）。
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
// node_modules + cordis.patch.yml + package.json 的 files 字段；其余工作区
// 内容一律不进种子。返回的回调按 CopyDirDeref / DirHash 的 ignored 语义
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
	Workspace string // 工作区（图标源与 DSH_HOME 种子来源）
	Cfg       *config.Config
	SeaDir    string // SEA 产物目录（bin/dsh、config/、node_modules/、package.json）
	ShellBin  string // 壳二进制
	SeedHash  string // 工作区内容 hash（写入种子的 .seed-hash 指纹，壳启动时比对）
}

// BinNames 返回壳与后端文件名（平台相关扩展名）。
func BinNames() (shell, server string) {
	if runtime.GOOS == "windows" {
		return "dsh-shell.exe", "dsh-server.exe"
	}
	return "dsh-shell", "dsh-server"
}

// assembleLayout 装配平台无关的公共布局（bin/ + 资源 + 种子），返回 bin
// 目录。appRoot 由调用方先清理。
func assembleLayout(in Inputs, appRoot string) (string, error) {
	binDir := filepath.Join(appRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	fmt.Printf("==> 装配布局 %s\n", appRoot)

	// 壳与 SEA 后端。
	shellName, serverName := BinNames()
	if err := fsutil.CopyFile(in.ShellBin, filepath.Join(binDir, shellName)); err != nil {
		return "", fmt.Errorf("copy shell: %w", err)
	}
	// SEA 产物里可执行名为 bin/dsh（SEA 打包约定），复制到 bin/ 时改名。
	seaBin := filepath.Join(in.SeaDir, "bin", "dsh")
	if runtime.GOOS == "windows" {
		seaBin += ".exe"
	}
	if err := fsutil.CopyFile(seaBin, filepath.Join(binDir, serverName)); err != nil {
		return "", fmt.Errorf("copy dsh-server: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(binDir, serverName), 0o755); err != nil {
			return "", err
		}
	}
	fmt.Printf("==>    bin/: %s, %s\n", shellName, serverName)

	// 壳运行时配置。
	if err := writeAppConfig(binDir, in.Cfg); err != nil {
		return "", fmt.Errorf("write appconfig.json: %w", err)
	}
	fmt.Printf("==>    bin/appconfig.json（%s %s）\n", in.Cfg.Name, in.Cfg.Version)

	// SEA 运行时资源：config/、node_modules/、package.json（从 SEA 产物复制）。
	// config/ 是可选资源目录（dsh 主包 0.1.2-rc.1 起不再发布 config/，
	// agent-presets 的 presets 移入 @deepseek-ai/dsh-agent-presets 包），
	// 存在才复制。
	for _, name := range []string{"config", "node_modules", "package.json"} {
		src := filepath.Join(in.SeaDir, name)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) && name == "config" {
				continue
			}
			return "", fmt.Errorf("copy %s: %w", name, err)
		}
		if err := fsutil.CopyDir(src, filepath.Join(appRoot, name)); err != nil {
			return "", fmt.Errorf("copy %s: %w", name, err)
		}
	}
	fmt.Printf("==>    SEA 运行时资源: config/ node_modules/ package.json\n")

	// DSH_HOME 种子：工作区 → appRoot/dsh-home/profiles/web（解引用）。
	// node_modules 不进种子——dsh 运行时从安装闭包（SEA 的
	// Contents/node_modules）解析 bundle 依赖，种子带了会遮蔽安装闭包。
	homeRoot := filepath.Join(appRoot, "dsh-home")
	profileDir := filepath.Join(homeRoot, "profiles", config.ProfileName)
	if err := os.MkdirAll(filepath.Join(homeRoot, "profiles"), 0o755); err != nil {
		return "", err
	}
	allow := SeedAllow(in.Cfg.Files)
	seedIgnored := func(rel string, isDir bool) bool {
		if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") {
			return true
		}
		return !allow(rel, isDir)
	}
	fmt.Printf("==>    DSH_HOME 种子（白名单: package.json, files=%v）\n", in.Cfg.Files)
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

// Assemble 按当前平台组装应用，把产物写进 dst 目录（调用方提供的暂存
// 目录，完成后由调用方发布进缓存）。
func Assemble(in Inputs, dst string) error {
	switch runtime.GOOS {
	case "darwin":
		return assembleMacOS(in, dst)
	case "linux":
		return assembleLinux(in, dst)
	case "windows":
		return assembleWindows(in, dst)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
