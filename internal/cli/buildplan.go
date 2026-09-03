// 打包链 DAG 装配：deploy 闭包 → SEA 后端 → 壳二进制 → 平台组装。
// 每步实现 build.Step（指纹依赖 Deps / 产物依赖 Needs / 类型化产物
// Output），由 internal/build 执行器并行调度：产物依赖就绪即执行，
// 仅指纹依赖的步可与依赖步并行。
package cli

import (
	"fmt"
	"path/filepath"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/build"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/bundle"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/sea"
)

// deployStep 产出 deploy 闭包（pnpm deploy --prod 的自包含生产闭包）。
type deployStep struct {
	ws          string
	cfg         *config.Config
	wsHash      string
	skipInstall bool
}

func (s *deployStep) ID() string                   { return "deploy" }
func (s *deployStep) Label() string                { return "deploy 闭包" }
func (s *deployStep) Deps() []build.Step           { return nil }
func (s *deployStep) Needs() []build.Step          { return nil }
func (s *deployStep) Fingerprint() (string, error) { return s.wsHash, nil }
func (s *deployStep) Output(dir string) build.Output {
	return sea.NewClosure(dir)
}
func (s *deployStep) Run(dst string, _ map[string]build.Output) error {
	if s.skipInstall {
		return fmt.Errorf("deploy 缓存缺失（--skip-install 下不自动 deploy；先跑一次不带 --skip-install 的 bundle 或 pnpm deploy --filter=%s --prod）", s.cfg.Name)
	}
	return sea.DeployClosure(s.ws, s.cfg, dst)
}

// seaStep 产出 SEA 后端（bin/dsh + 运行时资源）。
type seaStep struct {
	ws   string
	cfg  *config.Config
	tool string
	dep  *deployStep
}

func (s *seaStep) ID() string                   { return "sea" }
func (s *seaStep) Label() string                { return "SEA 后端" }
func (s *seaStep) Deps() []build.Step           { return []build.Step{s.dep} }
func (s *seaStep) Needs() []build.Step          { return []build.Step{s.dep} }
func (s *seaStep) Fingerprint() (string, error) { return s.tool, nil }
func (s *seaStep) Output(dir string) build.Output {
	return sea.NewOutput(dir)
}
func (s *seaStep) Run(dst string, deps map[string]build.Output) error {
	closure := deps["deploy"].(sea.Closure)
	return sea.Build(s.ws, s.cfg, closure, dst)
}

// shellStep 产出壳二进制（Wails v3）。与 deploy/SEA 无产物依赖，可与
// deploy 并行。
type shellStep struct {
	ws   string
	cfg  *config.Config
	tool string
}

func (s *shellStep) ID() string                   { return "shell" }
func (s *shellStep) Label() string                { return "壳二进制" }
func (s *shellStep) Deps() []build.Step           { return nil }
func (s *shellStep) Needs() []build.Step          { return nil }
func (s *shellStep) Fingerprint() (string, error) { return s.tool, nil }
func (s *shellStep) Output(dir string) build.Output {
	return shellOutput{dir: dir}
}
func (s *shellStep) Run(dst string, _ map[string]build.Output) error {
	return buildShell(s.ws, s.cfg, dst)
}

// shellOutput 是壳二进制产物（bin 文件位于产物根）。
type shellOutput struct{ dir string }

func (o shellOutput) Dir() string { return o.dir }

// Bin 返回壳可执行文件路径。
func (o shellOutput) Bin() string { return filepath.Join(o.dir, binName()) }

// assembleStep 产出平台应用（bundle.Output）。
type assembleStep struct {
	ws       string
	cfg      *config.Config
	wsHash   string
	platform string
	seaDep   *seaStep
	shDep    *shellStep
}

func (s *assembleStep) ID() string          { return "assemble" }
func (s *assembleStep) Label() string       { return "平台组装" }
func (s *assembleStep) Deps() []build.Step  { return []build.Step{s.seaDep, s.shDep} }
func (s *assembleStep) Needs() []build.Step { return []build.Step{s.seaDep, s.shDep} }
func (s *assembleStep) Fingerprint() (string, error) {
	return s.platform + ":" + s.wsHash, nil
}
func (s *assembleStep) Output(dir string) build.Output {
	return bundle.NewOutput(dir)
}
func (s *assembleStep) Run(dst string, deps map[string]build.Output) error {
	seaOut := deps["sea"].(sea.Output)
	shellOut := deps["shell"].(shellOutput)
	_, err := bundle.Assemble(bundle.Inputs{
		Workspace: s.ws,
		Cfg:       s.cfg,
		Sea:       seaOut,
		ShellBin:  shellOut.Bin(),
		SeedHash:  s.wsHash,
	}, dst)
	return err
}

// bundleGraph 构造完整打包 DAG（deploy / sea / shell / assemble）。
// 装配期错误（重复 ID / Needs 越界）是编程错误，直接 panic。
func bundleGraph(ws string, cfg *config.Config, wsHash, tool, platform string, skipInstall bool) *build.Graph {
	deploy := &deployStep{ws: ws, cfg: cfg, wsHash: wsHash, skipInstall: skipInstall}
	sea := &seaStep{ws: ws, cfg: cfg, tool: tool, dep: deploy}
	shell := &shellStep{ws: ws, cfg: cfg, tool: tool}
	assemble := &assembleStep{ws: ws, cfg: cfg, wsHash: wsHash, platform: platform, seaDep: sea, shDep: shell}
	g, err := build.NewGraph(ws, deploy, sea, shell, assemble)
	if err != nil {
		panic(err)
	}
	return g
}
