# 运维、发布和备份

## 日常运行

健康检查只证明 HTTP 可达；检查真实上传、待发送数、iPhone 通知注册和后端推送错误。不要在日志中记录 Authorization、请求体或 SMS。Nginx/Caddy 默认访问日志可能含 IP/路径，按自己的隐私政策管理。

Cloudflare 的应用配额是全站共享。`admin.mjs usage` 返回用量；它不读取短信。达到配额后处理积压，不要通过解除配对来删除待发送队列。

## Linux 一致性备份

维护窗口中停止 private-sms，再将 `/etc/private-sms` 和 `/var/lib/private-sms` 一同保存到受限、加密备份，完成后启动。存储密钥和数据必须配套；只备份密文或只备份 `.env` 都不足以恢复。备份中包含账户和个人数据，不能上传到公开仓库。

在隔离环境演练恢复；不要让生产和恢复副本同时接收相同地址流量或给真实设备重复推送。首次初始化只用于新空数据，恢复时不执行 `-init`。

## Cloudflare 数据与 Secret

Worker 升级保持 Durable Object 类名、迁移历史和 `idFromName('private-sms-free-v1')` 不变，否则可能连接到新的空数据库。不要修改既有 STORAGE_KEY。私下备份 bootstrap.json、worker.json、.p8 和各用户自己的读取令牌。

Secret 备份不是 Durable Object 短信数据备份。本版本没有独立短信导出/恢复工具，不应作为长期短信档案；需要更强恢复保证时先实现并演练相应备份流程。删除 Worker/对象数据可能无法恢复。

## 更新 APK 和 iOS

- APK 使用同一签名且递增 versionCode。保存版本文件和 SHA-256，固定下载路径提供最新稳定版本；原子替换 Linux 文件，或随 Worker Assets 部署。手机覆盖安装保留配对。
- iOS 保持自己的 Bundle ID/签名 Team，递增构建号，经 TestFlight 验证后再考虑正式发布。
- 升级代码前运行 CI。后台、权限、实际 APNs 仍需要真机验收。
- GitHub Release 不含运行时配置、账户数据或签名密钥；用户下载的是维护者签名包还是自行签名包应标清。

## 停用 Linux

确认所有用户切换到 Cloudflare，待发送为 0，真实短信和锁屏推送通过，旧历史及必要配置已处理。若 iOS 内置下载按钮仍指向旧域名，先更新该 URL 或提供独立 Cloudflare 下载入口。原 Linux 的 Apple 登录与账户恢复不会自动迁移；Cloudflare 使用管理员创建的个人读取令牌。

切换完成后链路为 Android → Cloudflare → Apple APNs → iPhone，配对/读取与 APK 下载也由 Cloudflare 提供。无需让 Linux 保持运行。并非“Linux 关机前后所有旧功能完全相同”：Cloudflare 精简版不提供原有 Apple 登录或文件分享功能。
