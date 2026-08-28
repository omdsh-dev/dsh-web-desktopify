// 扁平模块 fallback 的 bundle 补齐。dsh-app-boot 的
// healProfilesModuleFallback 只为 app 安装闭包（Contents/package.json 的
// 依赖 BFS）在 $DSH_HOME/profiles/node_modules 建符号链接；工作区级 bundle
// （dsh.profile.bundles，如 @morlay/better-session）不在 app 依赖图里，其
// 插件包（session-rdb / session-branch / ui-conversation-message-actions 等）
// 永远不会被链接，导致 Loader 从 profile 目录按父目录上溯解析不到这些包，
// cordis:include 加载失败、后端永不就绪。这里镜像上游 heal，但锚点是
// profile 的 bundle 包：从运行中 app 自己的闭包（exeDir/../node_modules）
// 解析并链接，app 移动/重装后仍然有效。
package dshhome

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// fallbackDirName 是扁平 fallback 目录名（$DSH_HOME/profiles/node_modules）。
const fallbackDirName = "node_modules"

// bundleManifest 是 profile package.json 里 bundle 声明的最小结构。
type bundleManifest struct {
	DSH struct {
		Profile struct {
			Bundles []string `json:"bundles"`
		} `json:"profile"`
	} `json:"dsh"`
}

// HealBundleFallback 把 profile 的 bundle 层及其依赖闭包链接进
// $DSH_HOME/profiles/node_modules（缺失才建，幂等）。bundle 包名在
// app 闭包里解析：先查闭包顶层 node_modules，再查 .pnpm/node_modules
// （pnpm 虚拟存储根），解析为实体目录后建绝对符号链接——与上游 heal
// 的链接风格一致。单个包解析失败不阻断（后端会给出明确报错）；fallback
// 目录无法创建才返回错误。
func HealBundleFallback(dshHome, profile, exeDir string) error {
	if dshHome == "" {
		return nil
	}
	closure := filepath.Join(filepath.Dir(exeDir), "node_modules")
	if !dirExists(closure) {
		return nil // 无闭包（异常安装）时跳过，让后端报错
	}

	// profile manifest 以运行时 home 为准（ensureSeed 已把种子落位）。
	raw, err := os.ReadFile(filepath.Join(dshHome, "profiles", profile, "package.json"))
	if err != nil {
		return nil // 无 profile 清单（首启前/无种子）：上游 heal 覆盖 app 闭包
	}
	var m bundleManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("解析 profile manifest: %w", err)
	}
	if len(m.DSH.Profile.Bundles) == 0 {
		return nil
	}

	fallback := filepath.Join(dshHome, "profiles", fallbackDirName)
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return fmt.Errorf("mkdir fallback %s: %w", fallback, err)
	}

	seen := map[string]bool{}
	for _, bundle := range m.DSH.Profile.Bundles {
		if err := healPackage(closure, fallback, bundle, seen); err != nil {
			// 单个 bundle 缺失（用户删了依赖）不阻断：后端会报
			// cannot resolve profile bundle。
			log.Printf("dshhome: 链接 bundle %s 失败（跳过）: %v", bundle, err)
		}
	}
	return nil
}

// healPackage 把一个包及其依赖闭包链接进 fallback：先建本包链接，再 BFS
// 它的 dependencies/peerDependencies（与上游 heal 的闭包遍历同构）。seen
// 按解析后的实体目录去重，避免重复遍历（peer 互相链接成环）。
func healPackage(closure, fallback, name string, seen map[string]bool) error {
	dir, err := resolveFromClosure(closure, name)
	if err != nil {
		return err
	}
	if err := ensureLink(fallback, name, dir); err != nil {
		return err
	}
	if seen[dir] {
		return nil
	}
	seen[dir] = true
	deps, err := readDepNames(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil // 无清单/读失败：跳过依赖遍历，本包已链接
	}
	for _, dep := range deps {
		if err := healPackage(closure, fallback, dep, seen); err != nil {
			// 依赖缺失（peer 可选等）不阻断：Node 解析不到时由调用方报错。
			continue
		}
	}
	return nil
}

// resolveFromClosure 在 app 闭包内解析一个包的实体目录：先查闭包顶层
// node_modules（如 @morlay/better-session），再查 .pnpm/node_modules
// 虚拟存储根（如 @morlay/session-rdb）。返回 EvalSymlinks 后的真实目录。
func resolveFromClosure(closure, name string) (string, error) {
	for _, base := range []string{closure, filepath.Join(closure, ".pnpm", fallbackDirName)} {
		cand := filepath.Join(base, name)
		if _, err := os.Stat(filepath.Join(cand, "package.json")); err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(cand); err == nil {
			return real, nil
		}
		return cand, nil
	}
	return "", fmt.Errorf("%s 不在 app 闭包（%s）", name, closure)
}

// ensureLink 在 fallback 建 name → target 的符号链接：已存在且指向同目标
// 时保持；错误/悬空链接替换；实体目录（用户托管）跳过不动。
func ensureLink(fallback, name, target string) error {
	link := filepath.Join(fallback, name)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(link)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return nil // 实体目录遮蔽：尊重用户布局
		}
		cur, err := os.Readlink(link)
		if err == nil && cur == target {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, link)
}

// readDepNames 读取 package.json 的 dependencies + peerDependencies 键。
func readDepNames(manifestPath string) ([]string, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m struct {
		Dependencies         map[string]string `json:"dependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(m.Dependencies)+len(m.PeerDependencies)+len(m.OptionalDependencies))
	for k := range m.Dependencies {
		names = append(names, k)
	}
	for k := range m.PeerDependencies {
		names = append(names, k)
	}
	for k := range m.OptionalDependencies {
		names = append(names, k)
	}
	return names, nil
}
