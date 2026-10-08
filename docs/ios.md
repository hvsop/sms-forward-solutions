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
- `PrivateSMS/APIClient.swift` 中 `serviceEndpoint` 是 **Apple 登录服务**地址，若使用该功能，改成自己的 Go 后端。
- `PrivateSMS/InboxView.swift` 的初始服务器地址可改为自己的默认地址；已有连接会以钥匙串保存值为准。用户也可在设置中手工填写。
- `PrivateSMS/AccountViews.swift` 的 APK 下载 URL 和 Apple 登录隐私说明域名应改成自己的发布地址。
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
