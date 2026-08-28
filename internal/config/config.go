// Package config 解析工作区配置。工作区是拍平的 desktop 定义：profile
// 内容直接放在工作区根（package.json 含 dsh.profile.bundles 与
// dsh.desktop，另有 cordis.patch.yml、pnpm-workspace.yaml、.npmrc）。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProfileName 是 desktop 唯一支持的 dsh profile 名。
const ProfileName = "web"

// Window 是桌面窗口的几何配置。
type Window struct {
	Width     int `json:"width"`
	Height    int `json:"height"`
	MinWidth  int `json:"minWidth"`
	MinHeight int `json:"minHeight"`
}

// Desktop 是 desktop 特有配置（package.json 的 dsh.desktop 字段）。
type Desktop struct {
	ID string `json:"id"`
	// Icon 是相对工作区的图标源文件（SVG 或 PNG），缺省不生成图标。
	Icon string `json:"icon"`
	// DSHHome 是运行时 DSH_HOME 策略：
	//   缺省            — xdg.DataHome/<name>（XDG_DATA_HOME 规范）；
	//   env             — 不设置 DSH_HOME，继承环境；
	//   绝对路径         — DSH_HOME 固定为该路径，缺失部分从 bundle 种子补齐。
	DSHHome string `json:"dshHome"`
	Window  Window `json:"window"`
}

// Config 是一份完整的工作区配置。
type Config struct {
	// Name 复用 package.json 的 name（应用名：窗口标题、数据目录、产物目录）。
	Name string
	// Version 复用 package.json 的 version。
	Version string
	// Bundles 复用 package.json 的 dsh.profile.bundles。
	Bundles []string
	// Files 复用 package.json 的 files：DSH_HOME 种子只复制白名单条目
	// （外加 package.json 与 node_modules），其余工作区内容不进产物。
	Files []string
	// Dependencies 是 profile 的依赖声明（package.json）。
	Dependencies map[string]string
	// Desktop 是 dsh.desktop 特有配置。
	Desktop Desktop
}

// manifest 是 profile package.json 的最小结构。
type manifest struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Private      bool              `json:"private"`
	Files        []string          `json:"files"`
	Dependencies map[string]string `json:"dependencies"`
	DSH          struct {
		Profile struct {
			Bundles []string `json:"bundles"`
		} `json:"profile"`
		Desktop Desktop `json:"desktop"`
	} `json:"dsh"`
}

// Load 读取并校验工作区配置。
func Load(ws string) (*Config, error) {
	manifestPath := filepath.Join(ws, "package.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w（工作区必须提供 package.json，声明 dsh.profile.bundles 与依赖）", manifestPath, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("%s: name 不能为空（应用名复用 npm 包名）", manifestPath)
	}
	if len(m.DSH.Profile.Bundles) == 0 {
		return nil, fmt.Errorf("%s: dsh.profile.bundles 不能为空（例如 [\"@deepseek-ai/dsh-base\", \"@deepseek-ai/dsh-web-app\"]）", manifestPath)
	}

	cfg := &Config{
		Name:         m.Name,
		Version:      m.Version,
		Bundles:      m.DSH.Profile.Bundles,
		Files:        m.Files,
		Dependencies: m.Dependencies,
		Desktop:      m.DSH.Desktop,
	}
	if cfg.Version == "" {
		cfg.Version = "0.0.1"
	}
	if cfg.Desktop.DSHHome == "" {
		cfg.Desktop.DSHHome = "xdg"
	}
	if cfg.Desktop.Window.Width == 0 {
		cfg.Desktop.Window.Width = 1280
	}
	if cfg.Desktop.Window.Height == 0 {
		cfg.Desktop.Window.Height = 800
	}
	if cfg.Desktop.Window.MinWidth == 0 {
		cfg.Desktop.Window.MinWidth = 800
	}
	if cfg.Desktop.Window.MinHeight == 0 {
		cfg.Desktop.Window.MinHeight = 600
	}
	if cfg.Desktop.ID == "" {
		cfg.Desktop.ID = "ai.deepseek." + sanitizeID(cfg.Name)
	}
	return cfg, nil
}

// TargetDir 返回产物根 target/ 目录（位于工作区）。
func TargetDir(ws string) string {
	return filepath.Join(ws, "target")
}

// BuildDir 返回该 desktop 的构建目录（target/<name>/，位于工作区）。
func BuildDir(ws string, cfg *Config) string {
	return filepath.Join(TargetDir(ws), cfg.Name)
}

// SeaDir 返回 SEA 打包暂存目录（target/<name>/sea）。
func SeaDir(ws string, cfg *Config) string {
	return filepath.Join(BuildDir(ws, cfg), "sea")
}

// DeployDir 返回 pnpm deploy 闭包目录（target/<name>/deploy）。
// 与 sea/、.app、dsh-home 隔离，避免重复打包互相清除。
func DeployDir(ws string, cfg *Config) string {
	return filepath.Join(BuildDir(ws, cfg), "deploy")
}

// DSHHomeDir 返回构建出的 DSH_HOME 种子目录（target/<name>/dsh-home）。
func DSHHomeDir(ws string, cfg *Config) string {
	return filepath.Join(BuildDir(ws, cfg), "dsh-home")
}

func sanitizeID(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r-'A'+'a')
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
