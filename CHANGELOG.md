# 更新记录

## Android 1.2.0 (7) — Google Play candidate

- Target Android 16 / API 36 for new Play submissions.
- Explain background SMS upload before requesting permission and provide the Android privacy policy.
- Support release-specific default endpoints and external signing properties without changing saved pairings.

## iOS 1.2（9）· App Store 候选版 · 2026-10-10

- 首屏优先连接个人读取令牌，明确需要安卓发送端和兼容服务。
- 发行配置通过 Xcode 参数提供；未配置 Apple 登录服务时不展示登录入口。
- 增加不联网的虚构短信演示、帮助与隐私政策入口、隐私清单及正式发布文档。
- 保留现有钥匙串结构、逐条已读和 APNs 协议；候选版尚不代表正式上架。

## 初始开源快照 · 2026-10-09

- Android 1.1.1（6）：短信转发开关、配对、可配置服务器、加密队列与重试。
- iOS 1.1（8）：独立读取令牌、配对管理、通用通知、逐条已读和角标、固定安卓下载链接。
- Go/Linux：多账户加密文件存储、APNs、可选 Apple 登录邀请开通、systemd/HTTPS 部署。
- Cloudflare：Workers + SQLite Durable Object + 静态 APK 托管，小规模免费计划部署。
- 配置模板使用示例域名和 Bundle ID，移除生产 Team/域名路由及个人运维记录。
- 维护者确认 Cloudflare 切换与真机测试通过；Go 文件分享保留为暂停开发的实验代码。
