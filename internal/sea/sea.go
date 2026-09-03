// Package sea 把 dsh profile 打包为 SEA 单文件后端（bin/dsh）。
package sea

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/fsutil"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/tools"
)

//go:embed all:templates
var templates embed.FS

// skipEntries 是复制闭包时跳过的 node_modules 簿记条目。
var skipEntries = map[string]bool{
	".store":        true,
	".nub":          true,
	".modules.yaml": true,
}

// bridgeName 是闭包内 CJS 桥的包名。
const bridgeName = "dsh-bridge"

// bridgePkgJSON / bridgeIndex 是 dsh-bridge 桥的内容：CJS 模块经
// createRequire 从文件系统加载后，其动态 import() 走正常 Node ESM
// loader（blob 内模块的 import() 受 SEA 限制），加载 dsh CLI 及其含
// 顶层 await 的依赖图。
const bridgePkgJSON = `{
  "name": "dsh-bridge",
  "version": "0.0.0",
  "type": "commonjs",
  "main": "index.cjs"
}
`

const bridgeIndex = `// dsh SEA 外部桥（dsh-web-desktopify 生成）：经 createRequire
// 从可执行文件旁 node_modules 加载的 CJS 模块，其 import() 走正常 Node
// ESM loader，加载 dsh CLI（lib/bin.js）及含顶层 await 的依赖图。
'use strict';
const { pathToFileURL } = require('node:url');
const { createRequire } = require('node:module');
const require2 = createRequire(__filename);
const bin = require2.resolve('@deepseek-ai/dsh/lib/bin.js');
import(pathToFileURL(bin).href).catch((err) => {
  console.error(err);
  process.exit(1);
});
`

// writeBridge 向闭包写入 dsh-bridge 伪包。
func writeBridge(nmDir string) error {
	dir := filepath.Join(nmDir, bridgeName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(bridgePkgJSON), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.cjs"), []byte(bridgeIndex), 0o644)
}

// Build 执行一次完整的 SEA 打包，把产物写进 dst 目录（调用方提供的
// 暂存目录，完成后由调用方发布进缓存）。deployDir 是 deploy 闭包所在
// 目录（CLI 分步 DAG 的 deploy 步负责生成/复用），此处直接复用。
func Build(ws string, cfg *config.Config, deployDir, dst string) error {
	if _, err := tools.Ensure(ws); err != nil {
		return err
	}

	staging := dst
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("mkdir staging: %w", err)
	}
	fmt.Printf("==> SEA 暂存: %s\n", staging)

	// 1) 闭包：deployDir/node_modules（pnpm deploy --prod 生成的自包含
	// 生产闭包）。保留链接（deploy 闭包的相对链接指向自身 .pnpm，自包含
	// 无逃逸）。
	deployRoot := deployDir
	deployNM := filepath.Join(deployRoot, "node_modules")
	if _, err := os.Stat(deployNM); err != nil {
		return fmt.Errorf("deploy 闭包缺失 %s（先跑 pnpm deploy --filter=%s --prod %s）: %w", deployNM, cfg.Name, deployRoot, err)
	}
	fmt.Printf("==> 复用 deploy 闭包: %s\n", deployNM)
	nmDst := filepath.Join(staging, "node_modules")
	fmt.Printf("==> 复制闭包 node_modules → %s\n", nmDst)
	if err := fsutil.CopyDir(deployNM, nmDst); err != nil {
		return fmt.Errorf("copy closure: %w", err)
	}

	// 2) dsh-bridge：向闭包写入 CJS 桥（运行时从 exe 旁闭包解析加载）。
	fmt.Printf("==> 写入 dsh-bridge 桥（%s）\n", filepath.Join(nmDst, bridgeName))
	if err := writeBridge(nmDst); err != nil {
		return fmt.Errorf("write dsh-bridge: %w", err)
	}

	// 3) 资源：dsh 主包的 config/ 与 package.json（从闭包内 dsh 实体取，
	// 实体在 .pnpm 虚拟存储，顶层 @deepseek-ai/dsh 是指向它的链接）。
	// config/ 是可选资源目录：0.1.2-rc.1 起 agent-presets 的 presets 移入
	// @deepseek-ai/dsh-agent-presets 包（自带 presets/），dsh 主包不再发布
	// config/，存在才复制（兼容旧版本）。
	dshLink := filepath.Join(nmDst, "@deepseek-ai", "dsh")
	dshPkg, err := filepath.EvalSymlinks(dshLink)
	if err != nil {
		return fmt.Errorf("解析闭包内 dsh 实体: %w", err)
	}
	fmt.Printf("==> 复制 dsh 运行时资源（%s）\n", dshPkg)
	if info, err := os.Stat(filepath.Join(dshPkg, "config")); err == nil && info.IsDir() {
		if err := fsutil.CopyDir(filepath.Join(dshPkg, "config"), filepath.Join(staging, "config")); err != nil {
			return fmt.Errorf("copy config: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat config: %w", err)
	}
	if err := fsutil.CopyFile(filepath.Join(dshPkg, "package.json"), filepath.Join(staging, "package.json")); err != nil {
		return fmt.Errorf("copy package.json: %w", err)
	}

	// 4) 生成打包入口与配置（templates/ 内嵌）。
	entries, err := templates.ReadDir("templates")
	if err != nil {
		return fmt.Errorf("read embedded templates: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := templates.ReadFile("templates/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(staging, e.Name()), data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}

	// 5) 构建。
	if err := tools.Run(ws, staging, "tsdown", "-c", "tsdown.config.mjs"); err != nil {
		return err
	}

	// 校验产物不含未内联的裸导入（SEA blob 内只能解析 node 内置模块）。
	if err := checkBareImports(filepath.Join(staging, "dist", "sea-entry.mjs")); err != nil {
		return err
	}

	exe := filepath.Join(staging, "bin", "dsh")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("SEA 产物缺失: %w", err)
	}
	return nil
}

// bareImportRE 匹配产物里的 ESM 裸导入 specifier（from/import/import()）。
// CJS require 不在此列：动态 require 走 createRequire，从外部闭包解析。
var bareImportRE = regexp.MustCompile(`(?:from\s+"([^"]+)")|(?:import\s+"([^"]+)")|(?:import\("([^"]+)"\))`)

// checkBareImports 报告 bundle 中非 node: 前缀的 ESM 裸导入（去重）。
func checkBareImports(bundlePath string) error {
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	var bad []string
	seen := map[string]bool{}
	for _, m := range bareImportRE.FindAllStringSubmatch(string(data), -1) {
		spec := ""
		for _, g := range m[1:] {
			if g != "" {
				spec = g
				break
			}
		}
		if spec == "" || strings.HasPrefix(spec, "node:") || seen[spec] {
			continue
		}
		seen[spec] = true
		bad = append(bad, spec)
	}
	if len(bad) > 0 {
		return fmt.Errorf("SEA bundle 含未内联的 ESM 依赖导入 %v（闭包缺包？检查 %s）",
			bad, filepath.Join(filepath.Dir(bundlePath), "node_modules"))
	}
	return nil
}
