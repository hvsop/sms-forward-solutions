# GitHub 仓库建议

单仓库即可，默认分支 main、Public、MIT，不必拆成四个仓库。发布者自己的 Cloudflare/Apple/Linux 密钥不属于读者构建源码所需信息。

## 建议手工配置

1. 仓库首页 About → Edit：简介可填 `Self-hosted Android-to-iPhone SMS forwarding with Go/Linux and Cloudflare backends.`；Topics 可加 android、ios、sms、self-hosted、cloudflare-workers、golang、swift、kotlin。
2. Settings → Code security / Advanced Security（GitHub UI 名称可能变化）：启用 Secret scanning / Push protection 和 Dependabot alerts；选项是否免费/可用以账户页面为准。
3. Settings → Security → Private vulnerability reporting：启用私密漏洞报告。
4. Actions → General：保持工作流默认只读权限；本仓库 CI 不部署、不需要生产 Secrets。不要为了公开构建上传 .p8 或签名文件。
5. 首次 CI 成功后，Settings → Rules → Rulesets，为 main 设置需要 PR 和通过 `Go`、`Cloudflare`、`Android`、`iOS` 检查，阻止 force-push/删除；按维护人数决定是否强制他人审核。空仓库首次提交之前不必设置保护规则。
6. Issues 用于缺陷和功能请求；文档保存在 docs，不依赖 Wiki。Discussions 可按社区需求再开。
7. Releases 按实际发布版本创建，附件提供已签名 APK、Linux 安装包及 SHA-256；保留不同组件版本与源码提交的对应关系。iOS 通过自己的 TestFlight/App Store，不在 Git 中放签名归档。

首次发布不需要公开你自己的在线短信服务。仓库地址与个人服务地址用途不同，README 指导读者自行部署。
