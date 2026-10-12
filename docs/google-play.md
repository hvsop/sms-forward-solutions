# Google Play 发布

维护者首发候选：private-sms-client 1.2.0（7），包名 `com.kuaapi.sms.forwarder`，Android 8.0+，target/compile SDK 36。保留原 APK 签名才能覆盖升级；不要卸载已有用户的安装。

## 构建与签名

安装 JDK 17、Android SDK 36 和 Build Tools 36.0.0。签名文件仍放仓库外或被忽略的 secrets 目录，不能提交密钥或密码。

```sh
cd android
./gradlew :app:testDebugUnitTest :app:lintRelease :app:bundleRelease :app:assembleRelease \
  -PandroidSigningProperties=/absolute/private/android-signing.properties \
  -PsmsDefaultEndpoint=https://your-trusted-service.example.com
```

不指定 endpoint 时默认示例地址；维护者构建使用 `https://smsfree.kuaapi.com`，它是邀请制服务，不是公开自助注册服务。已有配对设备保存的地址不会被覆盖。不要将签名属性内容打印到构建日志。

Google Play 上传 `app/build/outputs/bundle/release/app-release.aab`。官网分发使用同次构建的 APK。Play App Signing 应导入**原有**应用签名密钥并核对 SHA-256 证书指纹，以便与官网 APK 相互升级；不要直接接受 Google 生成另一把应用签名密钥。按 Play 提供的官方 PEPK 工具加密上传密钥，绝不公开 `.jks` 或签名属性。此项涉及将原签名密钥托管给 Google，需维护者明确授权。

如继续支持 GitHub/Cloudflare APK 渠道，应评估并关闭 Play 自动安装来源检查，避免官网用户被强制转向 Play；这不改变本应用的 TLS、加密或权限机制。

## 受限短信权限

应用只声明 `RECEIVE_SMS`，不申请 READ_SMS、SEND_SMS 或通话记录权限。Google 官方列出的例外用途是 **Cross-device synchronization or transfer of SMS or calls**。这是申请依据，不代表自动批准。

- 在主商店说明中突出安卓 → 用户选择服务 → 自己的 iPhone 短信同步。
- 演示用户主动配对、显著数据说明、同意后请求权限、开启/关闭转发、真实 SMS 同步、撤回权限/解除配对。
- SMS 权限声明选择跨设备同步例外，说明后台接收新短信是核心功能，其他更小范围 API 无法实现任意新 SMS 同步。不能声称默认短信 App 或把 SMS Retriever 当作该功能的等价替代。
- 录像使用专用虚构短信和隔离测试账户，不曝光生产令牌、真实短信或手机号。

政策依据（2026-10-12 核对）：
- https://support.google.com/googleplay/android-developer/answer/10208820
- https://support.google.com/googleplay/android-developer/answer/11926878

## 商店与 App 内容

- 免费、无广告、无内购。当前 UI 为简体中文；不能声称已有英文或日文 UI。
- 主分类：工具。首发地区沿用 iOS 的日本、美国，最终以维护者确认及 Console 为准。
- 安卓隐私政策：https://github.com/hvsop/sms-forward-solutions/blob/main/docs/android-privacy.md （须先合并并验证公开 URL）
- 数据安全：按实际服务评估 SMS/消息、账户/设备标识的数据收集；只用于功能，不作广告/追踪。传输 HTTPS，后端能解密，不宣称端到端加密。相机图像仅本机扫码，不上传。
- Android 不创建账户，只绑定既有账户。数据删除由接收端账户删除和管理员私下请求渠道提供，解除配对不等于删除整个账户。
- 提供可用的审核配对流程，配对码一次性且十分钟到期，不能把一个短期配对码当作永久审核凭据。审核应能经隔离接收端生成新配对码。
- 新个人开发者账号可能需要至少 12 位测试员连续加入封闭测试 14 天，并申请正式发布访问权限；具体限制以 Console 显示为准。内部测试不代替此要求。

## 发布前验证

1. 单元测试、Release Lint、AAB/APK 签名校验；核对 manifest 无额外 SMS/电话权限、target 36、正式包名、非 debuggable。
2. Android 16 与三星真机：覆盖升级保留绑定，权限前说明、拒绝路径、允许路径、开关关闭不上传、锁屏真实 SMS 与离线恢复重试。
3. 两端确认已读和推送；如 iOS 尚仅 TestFlight，商店说明和审核步骤须如实说明，不能宣称 iOS 已公开上架。
4. 完成 Play 的内容分级、数据安全、短信权限、广告、目标人群、App 访问权限等表单及商店素材。
5. 先完成 Console 要求的测试，再申请正式发布与审核。没有收到批准不能宣称已上架。
