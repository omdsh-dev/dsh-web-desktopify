# dev 回归 dsh 原生 profile 机制

dev 需要让工作区配置修改直接生效，同时不污染工作区工程文件、不触碰打包
应用使用的全局数据目录。因此 DSH_HOME 为工作区本地 `.dsh-store`，
`profiles/web` 由 dsh 自己初始化，工作区依赖以 `link:` 形式经
`dsh plugin add` 装入 profile——不软链工作区，不污染工作区工程文件。
dev 会话数据（settings.yaml、认证、storages 等）在 `.dsh-store` 根，
跨启动保留；`plugin add` 复用同一布局。
