# private-sms-client 隐私政策 / Privacy Policy

生效日期 / Effective date: 2026-10-12

本政策适用于 hvsop 发布的 Android 应用 **private-sms-client**（`com.kuaapi.sms.forwarder`）。这是用户主动配对、控制开关的个人短信同步工具。安装应用不会自动创建公共托管账户。开源项目与支持入口：https://github.com/hvsop/sms-forward-solutions 。

## 处理的数据与用途

- **新收到的短信**：仅在用户同意、授予接收短信权限、完成配对并开启转发后，应用在后台接收所有 SIM 卡的新 SMS，并把发件人、正文、接收时间及 SIM/订阅标识通过 HTTPS 上传到用户选择的服务。短信可能包含验证码或其他敏感信息。用途仅为在同一用户已配对的接收端同步查看。
- **账户与设备信息**：服务分配的设备 ID、设备凭据、账户标签及服务地址用于配对、鉴权和同步。应用不读取 IMEI、广告 ID、通讯录或手机号码资料。
- **相机**：只在用户选择扫码配对时用于本机识别二维码。不保存或上传相机图像；可以手动输入配对码而不授予相机权限。
- **网络信息**：服务及网络提供商可能处理 IP 地址、连接时间和安全日志以提供传输、限流与安全防护。hvsop 邀请制服务由 Cloudflare 托管。
- **本机状态**：保存转发开关、成功计数、时间、错误状态和加密待上传队列，以便重试和展示同步状态。没有广告、追踪、第三方行为分析或出售数据。

应用不读取历史短信，不读取通讯录，不发送运营商短信，不访问通话记录，不提供电话、MMS 或 RCS 同步。可选“发送测试消息”仅上传虚构内容，不产生运营商短信费用。

## 选择服务、存储与共享

请仅连接你自己或可信管理员提供的服务。短信和设备凭据经 HTTPS 传输；本机凭据和待上传短信使用 Android Keystore 保护的密钥加密，系统备份关闭。此方案**不是端到端加密**，服务运营者及运行环境有能力解密短信。

数据仅按用户指示发送到所选服务，供同一账户的接收端读取。hvsop 邀请制 Cloudflare 服务（smsfree.kuaapi.com）将数据存储在 Cloudflare。独立部署服务由相应管理员负责，其保留规则可能不同。接收端的 APNs 推送只含通用提醒和未读角标，不含短信正文、发件人或验证码。

本机队列最多 1,000 条；成功上传后删除本机队列中的正文；超过 7 天的条目在下次队列处理时清理。hvsop Cloudflare 服务默认保留短信 7 天，每账户最多 500 条。自建服务可以采用其他配置或备份规则，请向管理员确认。

## 撤回授权与删除

1. 关闭“短信转发”后，新短信不再进入队列，已有队列暂停上传；已经在途的请求可能完成。重新开启不会补传关闭期间的短信。
2. 在 Android 系统设置中撤回短信或相机权限，可停止相应功能。
3. “解除配对”撤销该安卓设备凭据并清除本机待上传队列，需要网络。连接失效时可以“清除本地数据”；仅本地清除不会撤销服务器凭据。
4. 服务器上已收到的短信可在配对的 iPhone 接收端删除或清空。接收端“设置 → 账户管理 → 注销账户”可删除账户及关联数据并使令牌、配对与推送注册失效；也可私下联系向你提供账户的管理员申请删除。
5. Android 客户端不创建账户。卸载它会移除本机数据，但不会自动删除服务器上的账户或短信，也不删除系统短信 App 中的原短信。

## 联系与更新

维护者：hvsop。隐私、账户及数据删除请求可发送至 **dwhou@icloud.com**；邮件中不要发送短信正文或令牌，管理员会通过私下流程核实账户。一般问题可通过 [项目支持页面](https://github.com/hvsop/sms-forward-solutions/issues) 联系。公开 Issue 不应包含短信、手机号、验证码、令牌或配对码。账户删除请求请使用管理员发放凭据时的私下联系渠道；安全问题可使用 [私密漏洞报告](https://github.com/hvsop/sms-forward-solutions/security/advisories/new)。政策变更会更新本页与生效日期。

---

## English

This policy applies to **private-sms-client**, the Android app published by hvsop (`com.kuaapi.sms.forwarder`). It synchronizes your own SMS with a paired receiver through a trusted backend. Installing the app does not create a public hosted-service account.

After you consent, pair the device, grant SMS permission and enable forwarding, the app receives new SMS in the background from all SIMs and uploads the sender, message text, received time and SIM/subscription label to your chosen HTTPS server. Messages may include verification codes or sensitive information. These data are used only for synchronization with your paired receiver. The app does not read past SMS, contacts, IMEI, advertising identifiers or call logs, and cannot send carrier SMS or synchronize calls, MMS or RCS.

The app uses the service-issued device ID, credential, account label and server address for authentication and synchronization. Camera access is optional and used only to scan a pairing QR code on the device; images are neither saved nor uploaded. Manual code entry works without camera permission. The app stores local status and an encrypted retry queue. There are no advertising or behavioral analytics SDKs, cross-app tracking or sale of data. Network and service providers may process IP addresses, connection times and security logs to provide transmission, rate limiting and security.

Data are transmitted via HTTPS to the service you choose. Local credentials and queued messages are encrypted using keys protected by Android Keystore, with Android backup disabled. This is **not end-to-end encryption**: the backend operator and hosting environment can decrypt messages. Only use a service you trust. hvsop's invitation-only service at smsfree.kuaapi.com uses Cloudflare for hosting and storage. Self-hosted services have their own operators and policies. Receiver push notifications carry a generic alert and unread count, not message text, sender details or verification codes.

The local queue holds at most 1,000 messages; message content is removed after successful upload. Entries older than seven days are removed during subsequent queue processing. The hvsop Cloudflare service retains SMS for seven days by default, up to 500 per account. Other administrators may set different retention or backup rules.

Turn off forwarding to stop capturing new SMS and pause queued uploads; an in-flight request may finish. SMS received while forwarding is off are not imported later. You can revoke SMS or camera permission in Android settings. Unpairing revokes this device credential and clears the local queue, and requires a network connection. Clearing local data alone does not revoke server credentials. Delete server messages or the entire account through the paired iPhone app, or contact your service administrator privately. The Android app does not create accounts. Uninstalling it does not delete server data or the original messages in the system SMS app.

For privacy and data deletion requests, contact **dwhou@icloud.com**. Do not email SMS content or credentials; the administrator will verify the account privately. Contact hvsop through the [project support page](https://github.com/hvsop/sms-forward-solutions/issues) for general support. Never post SMS, phone numbers, verification codes or credentials in public issues. Use your administrator's private account-provisioning contact channel for account data requests, and [private security reporting](https://github.com/hvsop/sms-forward-solutions/security/advisories/new) for security issues. Policy updates will be posted here with an updated effective date.
