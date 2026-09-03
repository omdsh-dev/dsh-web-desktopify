# 整体架构

把 dsh 的 `--profile web` 打包为独立自定义桌面的 Go 单命令：工作区是拍平
的 desktop 定义，`bundle` 按内容寻址 DAG 依次产出依赖闭包 → SEA 后端 →
壳二进制 → 平台应用，`dev` 用 dsh 原生 profile 机制直接起 web。

## 仓库布局

```
.
├── cmd/dsh-web-desktopify/       # CLI 入口（go.mod tool 指令注册）
├── internal/
│   ├── cli/                      # 命令编排：dev / bundle / plugin add + 打包 DAG 装配
│   ├── build/                    # 内容寻址构建 DAG 执行器（Step 接口 + 并行调度）
│   ├── config/                   # 工作区配置解析（package.json 的 dsh.* 字段）
│   ├── profile/                  # 工作区工程文件兜底 + 依赖闭包安装
│   ├── sea/                      # SEA 打包（deploy 闭包 → dsh-bridge → tsdown）
│   ├── bundle/                   # 平台组装（macOS / Linux / Windows）+ 安装
│   ├── tools/                    # 构建工具链（tsdown）按需安装
│   ├── fsutil/                   # 目录复制 / 解引用 / hash / 向上查找 / 环境工具
│   └── pm/                       # pnpm 定位与命令构造
├── pkg/shell/                    # 壳源码（Wails v3），go:embed 内嵌进 CLI
├── examples/                     # 工作区示例（official / custom）
├── docs/                         # 本目录：架构与约定
├── AGENTS.md                     # agent 工作指引（索引）
├── justfile                      # 常用命令
└── mise.toml                     # 工具链版本（go / node / pnpm / just）
```

## 分层模型

桌面应用三层，每一层都是 dsh 运行机制的必然结果：

| 层   | 产物                                        | 职责                                                             |
| ---- | ------------------------------------------- | ---------------------------------------------------------------- |
| 壳   | `dsh-shell`（Wails v3，Go）                 | 原生窗口 + WebView；后端进程守护（启动/就绪/退避重启/退出清理）   |
| 后端 | `dsh-server`（SEA，内嵌 node 的 `dsh --profile web`） | 跑 dsh 的 cordis 插件树，HTTP 伺服前端与 API                     |
| 前端 | dsh 内置 web 前端（`@deepseek-ai/dsh-web-app`） | 浏览器 UI，由后端经 HTTP 伺服，WebView 加载                      |

三个关键点：

- **必须依赖 node 运行时**：cordis 插件树（TS/ESM/npm 生态）只能在 node 上
  跑，桌面 app 不假设用户装了 node，因此用 SEA（Node Single Executable
  Application）把 node 内嵌进单文件可执行。构建期 node 由 mise 提供。
- **必须走 HTTP**：`dsh --profile web` 以 HTTP 伺服前端与 API
  （`__DSH_BOOT__` 注入），壳的 WebView 加载 `http://127.0.0.1:<port>`，
  端口由 OS 分配避免冲突。
- **壳必须存在**：node 后端不提供原生窗口；壳承担窗口生命周期、后端守护，
  并在退出时终止后端进程组，不留孤儿 node。

## 核心设计

- **内容寻址构建 DAG**（`internal/build` + `internal/cli/buildplan.go`）：
  deploy 闭包 → SEA 后端 → 壳二进制 → 平台组装。每步实现 `build.Step`
  接口：`Deps()`（指纹依赖，digest 传导）与 `Needs()`（产物依赖，执行
  顺序）分离，产物依赖就绪即并行执行——壳二进制与 deploy 闭包无产物
  依赖，可与 deploy 并行构建。每步产物按输入指纹存放于
  `node_modules/.dsh-web-desktopify/cache/<step>/<digest>/`，命中检查只
  关心目录在不在；依赖传导由 digest 链保证——deploy 重建后其 digest
  变化，下游 digest 随之变化，必然重建。`--force` 全部重建。
- **构建状态记录**：每次 bundle 在 `build/` 下写一份
  `build-<ts>.json`（各步 digest / 复用 / 产物路径，保留最近 10 份），
  便于回溯与清理；工具链目录带 `tools/state.json`（安装时间 + 工具
  版本）。`build/` 下只留状态记录与用完即删的暂存目录——壳源码解出
  （shell-src）构建完成即清理，不留中间产物。
- **类型化产物契约**：步间传递的不是目录路径而是类型化产物
  （`sea.Closure` / `sea.Output` / `shellOutput` / `bundle.Output`），
  SEA 布局（bin/dsh、config/、node_modules/）不再由 bundle 按路径约定
  隐式读取——布局变更在编译期暴露。
- **SEA 薄入口 + 外置闭包**（`internal/sea`）：blob 内只留 node: builtin
  导入的 `sea-entry.mjs`，dsh CLI 与全部依赖经闭包内 CJS 桥 `dsh-bridge`
  从可执行文件旁的 node_modules 走正常 Node ESM loader 解析——依赖闭包、
  原生模块（Node-API addon）与顶层 await 均不经过 SEA blob。
- **dev 回归 dsh 原生 profile 机制**（`internal/cli/dev.go`）：DSH_HOME 为
  工作区本地 `.dsh-store`，`profiles/web` 由 dsh 自己初始化，工作区依赖以
  `link:` 形式经 `dsh plugin add` 装入 profile——不软链工作区，不污染
  工作区工程文件。
- **壳源码内嵌**（`pkg/shell/embed.go`）：壳构建输入以 go:embed 内嵌在 CLI
  二进制，运行时解出为独立模块根再 `go build`——CLI 不依赖仓库 checkout，
  `go install` 后也能 bundle。
- **工作区白名单**（`internal/bundle`）：DSH_HOME 种子与工作区 hash 共用
  同一份白名单判定（`SeedAllow` / `SeedIgnored`）——package.json + files
  字段 + cordis.patch.yml 进种子与 hash，node_modules 单独指纹
  （`profile.ClosureFingerprint`），其余工作区内容（构建产物、缓存、
  `.dsh-store`、锁文件）一律排除。种子复制与 hash 的忽略语义一致，避免
  两处判定漂移。

## 装配（bundle 流程）

1. **deploy 闭包**（`internal/sea/deploy.go`）：在 workspace 根跑
   `pnpm deploy --prod` 导出工作区生产依赖闭包，修正 deploy 生成的
   workspace 配置（allowBuilds / minimumReleaseAge / autoInstallPeers /
   nodeLinker: hoisted）后补 `pnpm install`。
2. **SEA 打包**（`internal/sea/sea.go`）：复用 deploy 闭包复制
   node_modules，写入 `dsh-bridge` 桥，复制 `@deepseek-ai/dsh` 的
   `config/`（可选，0.1.2-rc.1 起不再发布）与 `package.json`，生成
   `sea-entry.mjs` 与 `tsdown.config.mjs`（`deps.onlyBundle: /^$/`，不内联
   任何依赖），调用工具链 tsdown 产出 `bin/dsh`，并校验产物不含未内联的
   ESM 裸导入。
3. **壳构建**（`internal/cli/run.go`）：解出内嵌壳源码到
   `node_modules/.dsh-web-desktopify/build/shell-src/`，动态生成 go.mod
   （module 名复用仓库路径使壳内 import 解析到本地子包）后 `go build`。
4. **平台组装**（`internal/bundle`）：macOS `.app`（Info.plist、icns）、
   Linux 目录 + `.desktop`（hicolor 图标集）、Windows 目录（ico），写入
   壳同目录 `appconfig.json` 与 DSH_HOME 种子 `dsh-home/`（工作区白名单
   解引用复制；`settings.yaml`、`storages/`、`sessions/` 等用户运行时数据
   不进种子）。种子写入 `.seed-hash` 指纹（工作区内容 hash）：壳每次启动
   强制 profile 为实体种子——指纹不一致时覆盖重建，一致时跳过（避免每次
   启动全量复制 node_modules 闭包）。

图标渲染不依赖外部工具：SVG 源用 oksvg/rasterx（纯 Go）渲染为白底图，
PNG 源直接解码，各平台尺寸由 `golang.org/x/image/draw` 缩放；macOS icns
用系统 iconutil 打包。

## 环境变量

- 构建期：`DSH_DESKTOP_ROOT`（仓库根；`go install` 到 PATH 后使用，
  仓库内 `go tool` 时自动定位）。壳构建不依赖它（源码由 CLI 内嵌），
  仅用于把 `examples/<name>` 相对路径解析到仓库内工作区
- 运行时（壳）：`DSH_APP_DSH_HOME`（显式覆盖 DSH_HOME，开发/测试用）、
  `DSH_APP_WORKSPACE`（工作目录，默认用户主目录）、`DSH_APP_PORT`
  （后端端口，默认 `0` 由 OS 分配）
- 透传给后端：`DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL`（LLM 凭据，Unix
  上启动前按 `$SHELL` source 用户 shell 配置继承）

## 测试

- 单元测试位于各包 `*_test.go`，`go test ./...` 全量运行（CI 与本地
  `just test`）。不依赖外部服务；需要真实 pnpm / node 的路径（如 SEA
  打包、壳构建）由 `internal/cli/shell_e2e_test.go` 显式标记，本地无工具
  链时跳过。
- 行为契约测试：网关接线（`pkg/shell/gateway/wire_test.go`）、appconfig
  打包/读取契约（`internal/bundle` 与 `pkg/shell/appconfig` 双侧）、
  构建 DAG 缓存语义（`internal/cli/buildplan_test.go`）。
- 并发安全：`sharedstore` 的并发写测试配合 `go test -race` 运行。

## 深入阅读

- 工作区结构与「先验证，再打包」：[docs/workspace.md](workspace.md)
- 项目约定：[docs/CODING_GUIDELINE.md](CODING_GUIDELINE.md)
- 关键决策记录：[docs/adr/](adr/)
