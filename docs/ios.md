# iOS、Apple APNs 与 TestFlight

## Apple 开发者配置

1. 安装完整 Xcode（工程最低部署 iOS 17），在 Settings → Apple Accounts 登录自己的开发者账号。TestFlight 需要付费 Apple Developer Program。
2. 在 Apple Developer 创建唯一的显式 App ID，例如 `com.yourcompany.sms`，启用 Push Notifications。仓库示例 Bundle ID 不属于可直接使用的签名资源。
3. 创建 APNs .p8 密钥，保存私钥、Key ID、Team ID。若创建页面限制 topic/environment，确保授权自己的 App ID 与使用环境。
4. 按 [Linux](linux.md) 或 [Cloudflare](cloudflare.md) 将私钥保存在后端。不要放进 Xcode 工程、手机安装包或 GitHub。

## 修改工程

打开 `ios/PrivateSMS.xcodeproj`：

- Target → Signing & Capabilities：选择自己的 Team，Automatically manage signing。
- Bundle Identifier 修改为自己的 App ID；后端 `APNS_TOPIC` 必须完全一致。
- 在 Xcode Build Settings 的 User-Defined 设置发行版的 `SMS_DEFAULT_ENDPOINT`（默认服务地址，可留空）、`SMS_ANDROID_DOWNLOAD_URL`（可信 APK 下载或说明页，可留空）。不填写时用户自行输入服务地址，下载入口指向源码构建文档。
- 仅使用 Cloudflare/读取令牌时，保持 `SMS_APPLE_LOGIN_ENDPOINT` 为空；首页和设置不会展示 Apple 登录入口。保留 Go Apple 登录的发行版可将此值设为自己已配置 Apple 登录的 HTTPS 服务 origin；后端与 App ID 必须匹配。
- 这些值也可通过 `xcodebuild SMS_DEFAULT_ENDPOINT=...` 等参数传入。不要将 Team、令牌、私钥写进公开源码。App 内隐私和帮助链接默认指向本仓库，fork 发行者须替换为自己的政策及支持入口。
- 默认工程声明 Push Notifications 和 Sign in with Apple。只使用令牌连接、不使用 Apple 登录时，可移除 Apple 登录 entitlement 和对应 Xcode capability；请不要移除推送能力。若保留 Apple 登录，需要在自己的 App ID 上启用该能力。

Cloudflare 精简版只使用“读取令牌连接”，不要点击 Apple 登录开通。`private-sms-server` 是 iPhone 显示名称，可以自行修改 `Info.plist`。

## 构建与检查

```sh
xcodebuild -project ios/PrivateSMS.xcodeproj -scheme PrivateSMS \
  -configuration Debug -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath /tmp/private-sms-ios-build CODE_SIGNING_ALLOWED=NO build
mkdir -p bin
swiftc ios/PrivateSMS/Models.swift ios/Tests/ReadStateTests/main.swift -o bin/read-state-tests
./bin/read-state-tests
```

模拟器/未签名构建不证明 APNs 真机可用。真实设备运行需要正确签名与通知权限。

## TestFlight 发布

1. 在 App Store Connect 创建自己的 App 记录，Bundle ID 与工程保持一致。
2. `Info.plist` 中修改版本号/构建号；同版本每次上传构建号必须递增。
3. Xcode 选择 Any iOS Device → Product → Archive → Distribute App → App Store Connect → Upload，使用 Release。
4. 等待 Apple 处理。按实际使用情况填写加密/出口合规、隐私和审核信息；当前 iOS 客户端调用 Apple 系统 HTTPS、钥匙串和设备认证，未内置自定义加密算法，但发布者应自行核对其修改后的实现。
5. 添加内部测试员；外部测试可能需要 Beta 审核，提供独立测试账户和测试说明，不提供真实短信账户。
6. 用户通过 TestFlight 安装后，在 App 中使用自己的服务器地址及读取令牌，再注册通知和配对安卓。源码开源不等于他人自动获得你的 TestFlight 资格。

| 构建 | APNs 环境 |
| --- | --- |
| Xcode Debug 真机 | sandbox |
| TestFlight / App Store Release | production |

后端环境必须相同；建议调试与正式服务分开，避免混用设备令牌。正式 App Store 还需真实隐私政策、审核材料、地区发布与商店要求，本仓库不保证通过审核。

## App Store 正式版准备

当前源代码版本为 1.2（9）。正式版提供读取令牌连接、可选 Apple 登录配置、隐私/帮助入口，以及无网络的虚构短信演示。PrivacyInfo.xcprivacy 声明短信、账户标识和推送设备标识用于 App 功能，不用于追踪；App Store Connect 隐私标签仍需按实际发行版及服务配置单独填写。

正式上架材料和检查点见 [App Store 发布](app-store.md)。发布者不能把模拟器构建成功或演示模式截图当作 APNs 真机验收。
