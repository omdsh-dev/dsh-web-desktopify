# AGENTS.md

## 开始工作前必读

- 技术栈与版本（go / node / pnpm / just）：[mise.toml](./mise.toml)
- 常用命令：[justfile](./justfile)
- 整体架构（仓库布局 / 分层 / 核心设计 / 装配）：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- 项目约定（命令 / 代码 / 测试 / 发布）：[docs/CODING_GUIDELINE.md](docs/CODING_GUIDELINE.md)
- 工作区结构与「先验证，再打包」流程：[docs/workspace.md](docs/workspace.md)
- 关键决策记录：[docs/adr/](docs/adr/)

## 红线

- 产物与缓存（`node_modules/.dsh-web-desktopify/`、`.dsh-store/`）不入库，
  不参与工作区 hash（见 docs/ARCHITECTURE.md 的构建原理）
- 跨包共用的逻辑（向上查找、环境构造、种子白名单）收敛到工具包，不复制
- 测试不依赖外部服务；并发代码用 `go test -race` 验证
