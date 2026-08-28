// Package dshhome 按壳配置解析运行时 DSH_HOME，并保证 profile 为来自
// bundle 种子的实体副本（dev/旧版本残留被强制替换）。
package dshhome

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"

	"github.com/omdsh-dev/dsh-web-desktopify/pkg/shell/appconfig"
)

// seedDirName 是 bundle 内 DSH_HOME 种子目录（壳可执行文件上一级），
// seedHashName 是种子里记录工作区内容 hash 的指纹文件。
const (
	seedDirName  = "dsh-home"
	seedHashName = ".seed-hash"
)

// 复制时排除的目录（安装簿记与 store，非 dsh 运行时所需）。
var seedSkipDirs = map[string]bool{
	".nub-store": true,
	".store":     true,
	".nub":       true,
}

// Resolve 按 cfg.DSHHome 策略解析 DSH_HOME：
//
//	DSH_APP_DSH_HOME（环境变量） — 显式覆盖，原样返回；
//	xdg（默认）                  — xdg.DataHome/<name>，强制种子落位；
//	<绝对路径>                    — 固定该目录，同样强制种子落位；
//	env                          — 返回空串，不设置 DSH_HOME（继承环境）。
//
// 打包 app 必须独立于工作区：启动时强制 profiles/web 为实体种子，dev/旧版
// 残留的 symlink 或旧实体拷贝都被替换（见 ensureSeed）。返回空串表示调用方
// 不设置 DSH_HOME。
func Resolve(cfg appconfig.Config, exeDir string) (string, error) {
	if v := os.Getenv("DSH_APP_DSH_HOME"); v != "" {
		return v, nil
	}
	seed := filepath.Join(exeDir, "..", seedDirName)

	switch cfg.DSHHome {
	case "env":
		return "", nil
	case "xdg":
		dst := filepath.Join(xdg.DataHome, cfg.Name)
		if err := ensureSeed(seed, dst, cfg.Profile); err != nil {
			return "", err
		}
		// 工作区级 bundle（@morlay/* 等）不在 app 安装闭包，上游 heal 不会
		// 链接它们——这里补上，否则后端 cordis:include 解析不到插件。
		if err := HealBundleFallback(dst, cfg.Profile, exeDir); err != nil {
			return "", fmt.Errorf("补齐 bundle fallback: %w", err)
		}
		return dst, nil
	default:
		dst := cfg.DSHHome
		if !filepath.IsAbs(dst) {
			return "", fmt.Errorf("dshHome 必须是 xdg / env / 绝对路径，得到 %q", cfg.DSHHome)
		}
		if err := ensureSeed(seed, dst, cfg.Profile); err != nil {
			return "", err
		}
		if err := HealBundleFallback(dst, cfg.Profile, exeDir); err != nil {
			return "", fmt.Errorf("补齐 bundle fallback: %w", err)
		}
		return dst, nil
	}
}

// ensureSeed 强制目标 DSH_HOME 的 profile 为来自种子的实体副本：指纹
// （.seed-hash）不一致的 symlink 或旧实体拷贝被移除并用种子覆盖；指纹一致
// 时跳过（避免每次启动全量复制 node_modules 闭包）。用户数据位于 home 根，
// 不受 profile 替换影响。
func ensureSeed(seed, dst, profile string) error {
	if !dirExists(seed) {
		return nil
	}
	profileDir := filepath.Join(dst, "profiles", profile)
	seedHash := readSeedHash(filepath.Join(seed, "profiles", profile, seedHashName))
	if seedHash != "" {
		if info, err := os.Lstat(profileDir); err == nil && info.Mode()&os.ModeSymlink == 0 {
			if readSeedHash(filepath.Join(profileDir, seedHashName)) == seedHash {
				return nil
			}
		}
	}
	if _, err := os.Lstat(profileDir); err == nil {
		if err := os.RemoveAll(profileDir); err != nil {
			return fmt.Errorf("移除旧 profile %s: %w", profileDir, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := copySeed(seed, dst); err != nil {
		return fmt.Errorf("拷贝 dsh-home 种子到 %s: %w", dst, err)
	}
	return nil
}

// readSeedHash 读取 .seed-hash 指纹内容（不存在返回空串）。
func readSeedHash(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// copySeed 把种子递归复制到 dst（跳过安装簿记目录），只补缺失文件，不覆盖
// 用户已有数据。
func copySeed(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if seedSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		return copyFileMode(path, target)
	})
}

func copyFileMode(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
