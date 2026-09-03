# dsh-web-desktopify

把 dsh 的 `--profile web` 与 `cordis.patch.yml` 打包为独立自定义桌面应用的
Go 单命令工具：工作区是拍平的 desktop 定义，`bundle` 按内容寻址 DAG 产出
壳 + 后端 + 前端三层桌面，`dev` 用 dsh 原生 profile 机制直接起 web。

## 构建输入

**工作区（Workspace）**：
拍平的 desktop 定义：package.json（name/version/dependencies +
`dsh.profile.bundles` + `dsh.desktop`）+ cordis.patch.yml +
pnpm-workspace.yaml + .npmrc。是构建的输入单元，也是可安装、可验证的单元。
_避免使用_：项目、目录

**Profile**：
dsh 的 profile 机制；desktop 有且只有一个 web profile，其内容直接放在
工作区根（无目录嵌套、无独立配置文件）。
_避免使用_：profile 目录（实现细节）

**Bundle 包**：
`dsh.profile.bundles` 声明的 cordis bundle（如 @deepseek-ai/dsh-base、
@deepseek-ai/dsh-web-app），自带 patch 层，按序叠加后由 cordis.patch.yml
（用户层）收尾。
_避免使用_：插件（插件是安装动作的视角，见下）

**插件（Plugin）**：
经 `plugin add` 加入工作区的 npm 包；声明 `dsh.bundle` 的依赖自动入层
（进入 `dsh.profile.bundles`），被移除或失去声明的包出层。
_避免使用_：扩展、模块

**patch 层**：
cordis 配置的分层 patch 合成：bundle 包自带的 patch 层按序叠加，最后叠加
cordis.patch.yml（用户层）。CLI 只负责安装、打包与分发，不修改 patch 语义。

**依赖闭包（Closure）**：
自包含的依赖树（node_modules）。deploy 闭包是 `pnpm deploy --prod` 导出的
生产闭包，SEA 打包与 DSH_HOME 种子都从它取依赖。
_避免使用_：node_modules、依赖目录

## 产物与运行时

**桌面（Desktop）**：
打包产物：壳 + 后端 + 前端三层。`dsh.desktop` 配置描述其身份
（id/window/icon）与 DSH_HOME 策略（dshHome）。

**壳（Shell）**：
Wails v3 原生窗口 + WebView；承担窗口生命周期与后端守护（启动/就绪/退避
重启/退出清理），退出时终止后端进程组，不留孤儿 node。
_避免使用_：外壳、宿主、wrapper

**后端（dsh-server）**：
SEA 单文件可执行（内嵌 node 的 dsh CLI），跑 cordis 插件树，HTTP 伺服
前端与 API。就绪后把 `dsh web: http://127.0.0.1:<port>` 打到 stdout。
_避免使用_：服务端、server 进程

**网关（Gateway）**：
壳内 HTTP 反代：把后端（随机端口）经壳的稳定端口转发，并在 index.html
注入 wails runtime.js 与共享 localStorage bridge。页面 origin 是网关端口，
不受后端重启影响。
_避免使用_：代理（与反代混淆）

**共享存储（Shared Store）**：
壳级 localStorage 共享层：内存快照 + 落盘 DSH_HOME/storages/，页面的
bridge 经 wails IPC 把 localStorage 读写转接到这里——与页面 origin 无关，
跨启动延续。桌面单实例：无并发写者，写即覆盖。
_避免使用_：localStorage 层、存储服务

**appconfig**：
打包时写入壳同目录的运行时配置（appconfig.json）：name/id/version/
window/profile/dshHome。是打包与运行时之间的契约，壳启动时读取。
_避免使用_：配置、config（与工作区配置混淆）

**种子（Seed）**：
打包进应用的 DSH_HOME profile 实体副本（dsh-home/），带 .seed-hash 指纹。
壳启动时强制 profile 为实体种子：指纹不一致时覆盖重建，一致时跳过。
_避免使用_：模板、初始数据

**DSH_HOME**：
dsh 运行时数据目录：profiles/、settings.yaml、storages/、sessions/ 等。
打包应用按 dshHome 策略落位（xdg 数据目录 / 固定路径 / 继承环境）；dev
使用工作区本地 .dsh-store。
_避免使用_：数据目录、home 目录

## 构建机制

**构建步（Build Step）**：
打包链中的一步：deploy 闭包 → SEA 后端 → 壳二进制 → 平台组装。每步产物
按输入指纹内容寻址存放于 cache/<step>/<digest>/，命中检查只关心目录在不在。

**指纹（Fingerprint）**：
内容寻址的输入摘要（sha256）：工作区 hash、闭包清单指纹、工具链指纹、
种子指纹。依赖传导由 digest 链保证——依赖步重建后其指纹变化，下游必然
重建。
_避免使用_：hash、digest（代码内混用，领域统一为指纹）

**打包（bundle 命令）**：
把工作区打包为平台应用（--force 忽略缓存全新打包，--install 打包后安装
到当前平台）。与 Bundle 包是不同概念。

**安装（Install）**：
把打包产物安装到当前平台：macOS /Applications/<Name>.app、Linux
xdg.DataHome/<Name> + .desktop、Windows %LOCALAPPDATA%\Programs\<Name>。
