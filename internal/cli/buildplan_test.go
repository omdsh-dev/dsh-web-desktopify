package cli

import (
	"testing"

	"github.com/omdsh-dev/dsh-web-desktopify/internal/build"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/bundle"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/config"
	"github.com/omdsh-dev/dsh-web-desktopify/internal/sea"
)

// TestBundleGraphTopology：打包 DAG 的依赖拓扑——deploy 无依赖；sea 依赖
// deploy（指纹 + 产物）；shell 无依赖（可与 deploy 并行）；assemble 依赖
// sea 与 shell（指纹 + 产物）。
func TestBundleGraphTopology(t *testing.T) {
	cfg := &config.Config{Name: "dsh-test"}
	g := bundleGraph(t.TempDir(), cfg, "h", "tool", "darwin/arm64", false)
	byID := map[string]build.Step{}
	for _, s := range g.Steps() {
		byID[s.ID()] = s
	}
	if len(byID) != 4 {
		t.Fatalf("应有 4 步，得到 %d", len(byID))
	}
	// deploy：无依赖。
	if len(byID["deploy"].Deps()) != 0 || len(byID["deploy"].Needs()) != 0 {
		t.Fatal("deploy 不应有依赖")
	}
	// sea：指纹 + 产物依赖 deploy。
	if len(byID["sea"].Deps()) != 1 || byID["sea"].Deps()[0].ID() != "deploy" {
		t.Fatal("sea 应指纹依赖 deploy")
	}
	if len(byID["sea"].Needs()) != 1 || byID["sea"].Needs()[0].ID() != "deploy" {
		t.Fatal("sea 应产物依赖 deploy")
	}
	// shell：无依赖（可与 deploy 并行）。
	if len(byID["shell"].Deps()) != 0 || len(byID["shell"].Needs()) != 0 {
		t.Fatal("shell 不应有依赖（可与 deploy 并行）")
	}
	// assemble：指纹 + 产物依赖 sea 与 shell。
	if len(byID["assemble"].Deps()) != 2 || len(byID["assemble"].Needs()) != 2 {
		t.Fatal("assemble 应依赖 sea 与 shell")
	}
}

// TestBundleGraphOutputs：各步产物类型化——deploy 产出 sea.Closure、
// sea 产出 sea.Output、shell 产出 shellOutput、assemble 产出
// bundle.Output。
func TestBundleGraphOutputs(t *testing.T) {
	cfg := &config.Config{Name: "dsh-test"}
	g := bundleGraph(t.TempDir(), cfg, "h", "tool", "darwin/arm64", false)
	byID := map[string]build.Step{}
	for _, s := range g.Steps() {
		byID[s.ID()] = s
	}
	if _, ok := byID["deploy"].Output("/x").(sea.Closure); !ok {
		t.Fatal("deploy 产物应为 sea.Closure")
	}
	if _, ok := byID["sea"].Output("/x").(sea.Output); !ok {
		t.Fatal("sea 产物应为 sea.Output")
	}
	if _, ok := byID["shell"].Output("/x").(shellOutput); !ok {
		t.Fatal("shell 产物应为 shellOutput")
	}
	if _, ok := byID["assemble"].Output("/x").(bundle.Output); !ok {
		t.Fatal("assemble 产物应为 bundle.Output")
	}
}
