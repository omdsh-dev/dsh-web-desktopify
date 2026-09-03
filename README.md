# dsh-web-desktopify

把 [@deepseek-ai/dsh](https://www.npmjs.com/package/@deepseek-ai/dsh) 的
`--profile web` 与 `cordis.patch.yml` 打包为**独立自定义桌面**的 Go 单命令。
支持 macOS、Linux 与 Windows。

## Quick Start

```sh
go install github.com/omdsh-dev/dsh-web-desktopify/cmd/dsh-web-desktopify@latest
```

创建你的工作区（复制本仓库 [examples/custom](examples/custom) 即可起步），
然后：

```sh
dsh-web-desktopify dev examples/custom        # 基于工作区起 dsh web 并打开浏览器
dsh-web-desktopify bundle examples/custom     # 打包当前平台的应用（基于工作区 hash 增量）
dsh-web-desktopify bundle --force examples/custom      # 忽略缓存，全新打包
dsh-web-desktopify bundle --install examples/custom    # 打包并安装到当前平台
cd examples/custom && dsh-web-desktopify plugin add @foo/bar   # 向工作区加插件（代理 dsh plugin add）
```

## 命令

- `dev [<workspace>]` — 基于工作区直接起 `dsh web` 并打开浏览器页面
  （Ctrl+C 退出）。缺省当前目录；目录还不是工作区（缺 package.json）时
  自动从模板创建工程文件并安装依赖。运行时 DSH_HOME 为工作区本地目录
  `.dsh-store`（会话数据跨启动保留，不污染打包应用使用的全局数据目录），
  `profiles/web` 由 dsh 原生管理：工作区依赖以 `link:` 形式经
  `dsh plugin add` 装入 profile，工作区配置修改直接生效
- `bundle <workspace>` — 打包为平台应用。产物按输入指纹内容寻址缓存于
  `node_modules/.dsh-web-desktopify/cache/<step>/<digest>/`：输入无变化时
  直接复用上次产物；`--force` 忽略缓存全新打包；`--install` 打包后安装
  （macOS `/Applications`、Linux XDG data + `.desktop`、Windows
  `%LOCALAPPDATA%\Programs`）
- `plugin add <package...>` — 代理 `dsh plugin add`：在工作区跑 `pnpm add`，
  并把声明 `dsh.bundle` 的依赖自动加入 `dsh.profile.bundles`。不安装到全局
  `~/.dsh`，只改工作区（默认当前目录，`--workspace=<path>` 指定其他工作区）。
  与官方 dsh 的 reconcile 语义一致：bundle 包自动入层，普通依赖只警告不入层

## 工作区

工作区是拍平的 desktop 定义：`package.json`（name/version/dependencies +
`dsh.profile.bundles` + `dsh.desktop`）+ `cordis.patch.yml`（patch 层）+
`pnpm-workspace.yaml` + `.npmrc`。可先在工作区 `pnpm install`，再用官方
dsh 验证（见 [docs/workspace.md](docs/workspace.md)）。

构建依赖：`go`、`node`、`pnpm`（[mise.toml](mise.toml) 管理）。仓库内开发
用 `go tool dsh-web-desktopify`（go.mod `tool` 指令注册，无需
`go build`）。

## 文档

- [docs/workspace.md](docs/workspace.md) — 工作区结构与「先验证，再打包」流程
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — 产物、架构与构建原理
- [docs/CODING_GUIDELINE.md](docs/CODING_GUIDELINE.md) — 项目约定
- [docs/adr/](docs/adr/) — 关键决策记录（ADR）

## 开发

```sh
just test        # go test ./...（并发代码用 go test -race 验证）
just install     # go install ./cmd/dsh-web-desktopify
just dep         # go mod tidy
just custom::dev     # examples/custom 起 dsh web（go tool dsh-web-desktopify dev）
just custom::bundle  # examples/custom 打包（go tool dsh-web-desktopify bundle）
```
