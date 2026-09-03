# 项目约定

本仓库是 Go 单命令仓库（module `github.com/omdsh-dev/dsh-web-desktopify`）。
所有命令入口见 [justfile](../justfile)，版本见 [mise.toml](../mise.toml)。

## 技术栈

| 项     | 值       | 位置      |
| ------ | -------- | --------- |
| go     | 1.27     | mise.toml |
| node   | latest   | mise.toml |
| pnpm   | 12       | mise.toml |
| just   | latest   | mise.toml |
| 构建   | go build | go.mod    |
| 测试   | go test  | 各包 *_test.go |

## 常用命令

```sh
just test        # go test ./...
just install     # go install ./cmd/dsh-web-desktopify
just dep         # go mod tidy
just clean       # 清理 target/
just custom::dev     # examples/custom 起 dsh web（go tool dsh-web-desktopify dev）
just custom::bundle  # examples/custom 打包（go tool dsh-web-desktopify bundle）
```

仓库内开发用 `go tool dsh-web-desktopify`（go.mod `tool` 指令注册），
无需 `go build`。

## 代码约定

- **产物与缓存不入库**：`node_modules/.dsh-web-desktopify/`（构建缓存）、
  `.dsh-store/`（dev 运行时目录）、`target/` 均被 .gitignore 排除，且不
  参与工作区 hash（白名单模式，见 docs/ARCHITECTURE.md 的构建原理）。
- **注释不做设计说明**：设计意图写进 docs/ARCHITECTURE.md；代码注释只
  说明必要的事实（如可选资源目录的版本边界）。
- **测试描述行为，不描述实现**：行为变更必须连同测试一起改。
- 文件以单个换行结尾。

## 测试约定

- 测试位于各包 `*_test.go`（`go test ./...`）。
- 不依赖外部服务；需要真实 pnpm / node 的路径（如 SEA 打包）由
  `shell_e2e_test.go` 显式标记，本地无工具链时跳过。

## 发布

- 发布走 CI：`.github/workflows/release.yml`（`v*` tag push → goreleaser
  构建 + 打包 + GitHub Releases）。
- 严禁本地私自发布（包括用 `--registry` 指向 GitHub Packages 的发布）。
  版本 bump 提交后由 CI 发布；本地只构建验证。
