# 工作区是拍平的 desktop 定义

desktop 有且只有一个 profile（web），dsh 的 profile 机制天然支持目录
嵌套，但这里选择拍平：profile 内容直接放在工作区根（package.json 含
`dsh.profile.bundles` 与 `dsh.desktop`，另有 cordis.patch.yml、
pnpm-workspace.yaml、.npmrc），无目录嵌套、无独立配置文件。后果：
工作区本身是可安装、可验证的单元（官方 dsh 流程直接可用）；DSH_HOME
种子只复制白名单条目（package.json + files + node_modules），其余工作区
内容不进产物。
