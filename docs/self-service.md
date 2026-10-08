# Linux 可选 Apple 自助开通

仅 Go/Linux 实现。Cloudflare 精简版使用管理员创建的读取令牌，不启用此流程。

1. Apple Developer 的自己的 App ID 开启 Sign in with Apple。
2. 创建独立的 Sign in with Apple .p8 密钥，关联这个 App ID。它与 APNs 密钥用途不同。
3. 在 Go 配置加入全部四项：

```dotenv
APPLE_CLIENT_ID=com.yourcompany.yourapp
APPLE_TEAM_ID=YOUR_TEAM_ID
APPLE_KEY_ID=YOUR_SIGNIN_KEY_ID
APPLE_KEY_FILE=/etc/private-sms/apple-signin.p8
```

私钥 root:private-sms 0640。`APPLE_CLIENT_ID` 与 iOS Bundle ID 相同，原生登录不需要网页 Services ID。iOS 的 `APIClient.serviceEndpoint` 和 Apple 登录界面域名修改为自己的 Go 服务，签名能力配置相同。

4. 重启 Go，生成单次、7 天有效邀请码：

```sh
sudo -u private-sms /opt/private-sms/bin/private-sms \
  -config /etc/private-sms/server.env -create-signup-invite \
  -out /var/lib/private-sms/invitations/friend-signup.json
```

5. 用户在自己的 iPhone 输入邀请码并通过 Apple 登录，再生成安卓配对码。已有 Apple 账户登录不需要新邀请码。

旧令牌用户先连接原收件箱再绑定 Apple 账户，避免另开空账户。换 iPhone 用同一 Apple 身份会轮换读取令牌并转移推送注册。服务端不按邮箱合并账户、不索取姓名和邮箱。

JWT 签名、issuer/audience、nonce、授权码兑换及身份一致性会验证；挑战限时限量。Apple refresh token 加密保存，注销时尝试撤销，失败保留账户以便重试。Apple 账户状态检查不能替代实时服务器变更通知；这不是不受限制的公开注册平台。
