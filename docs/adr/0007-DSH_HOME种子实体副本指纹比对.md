# DSH_HOME 种子：实体副本 + 指纹比对

打包应用必须独立于工作区：启动时强制目标 DSH_HOME 的 profile 为来自
bundle 种子的实体副本，dev/旧版本残留的 symlink 或旧实体拷贝都被替换。
备选方案是每次启动全量复制闭包（慢）或软链到安装闭包（不独立、可被
用户改动破坏）。选择实体副本 + .seed-hash 指纹比对：指纹不一致时覆盖
重建，一致时跳过——避免每次启动全量复制 node_modules 闭包。用户数据
（settings.yaml、storages/、sessions/）位于 home 根，不受 profile 替换
影响。
