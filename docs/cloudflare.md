# Cloudflare 从零部署

## 1. 准备

需要自己的 Cloudflare 账户、Node.js 24/npm。先确认 Workers Free，本文不要求升级付费或开通 R2。官方价格可能变化，部署前检查 [Workers](https://developers.cloudflare.com/workers/platform/pricing/)、[Durable Objects](https://developers.cloudflare.com/durable-objects/platform/pricing/)、[Static Assets](https://developers.cloudflare.com/workers/static-assets/billing-and-limitations/)。

免费计划的资源配额是账户共享；如果人为升级 Paid，超额用量可能收费。本项目应用上限：10 账户、500 条/账户、保留 7 天、全站 500 条新 SMS/天、6,000 次 API 请求/天、1,000 次推送尝试/天、配对 30 次/分钟。日额度按 UTC 午夜重置。达到限制可能返回 429/507，安卓保留队列重试。

```sh
git clone https://github.com/hvsop/sms-forward-solutions.git
cd sms-forward-solutions/cloudflare
npm ci
npx wrangler login
```

登录你自己的 Cloudflare 账户。若有多个账户，在 `wrangler.jsonc` 设置自己的 `account_id`。`name` 必须在本账户中唯一。

## 2. 初始化独立密钥

```sh
node scripts/bootstrap.mjs
```

生成 `secrets/bootstrap.json`（管理令牌）与 `secrets/worker.json`（存储密钥、管理令牌摘要）。文件权限 0600，脚本拒绝覆盖；不要为已有数据重新生成 `STORAGE_KEY`。私下备份这两个文件。

## 3. 配置 Apple 推送

先按 [iOS/APNs 文档](ios.md) 创建自己的 Bundle ID、APNs 密钥。把 `wrangler.jsonc` 的 `vars.APNS_TOPIC` 改为自己的 Bundle ID，`APNS_ENVIRONMENT` 对 TestFlight/App Store 使用 `production`。Debug 使用 `sandbox`，应另部署调试 Worker。

将 .p8 文件私下保存到 `secrets/apns.p8`，在 `cloudflare/` 下运行以下命令，替换 Key ID 与 Team ID（它们不是私钥）：

```sh
node --input-type=module - <<'JS'
import fs from 'node:fs';
const file = 'secrets/worker.json';
const config = JSON.parse(fs.readFileSync(file, 'utf8'));
Object.assign(config, {
  APNS_KEY: fs.readFileSync('secrets/apns.p8', 'utf8'),
  APNS_KEY_ID: 'YOUR_KEY_ID',
  APNS_TEAM_ID: 'YOUR_TEAM_ID'
});
fs.writeFileSync(file, JSON.stringify(config, null, 2) + '\n', {mode: 0o600});
JS
```

保留已有 `STORAGE_KEY` 和 `ADMIN_TOKEN_HASH`。私钥不能放在公开 `vars`、源码或下载目录。未配置 APNs 时收件箱可同步，但不会发送通知。

## 4. 检查并部署

```sh
npm run deploy -- --free-plan-confirmed
```

此命令先执行类型检查和 11 项测试，再上传源码、静态资源及 `secrets/worker.json` 中的 Secret。首次创建免费 SQLite Durable Object。记录 Wrangler 输出的 HTTPS 地址：

```sh
export SMS_ENDPOINT=https://YOUR-WORKER.YOUR-SUBDOMAIN.workers.dev
curl --fail "$SMS_ENDPOINT/healthz"
node scripts/admin.mjs usage
node scripts/admin.mjs probe
```

`probe` 只验证到 Apple 的网络通路，不代表真实 iPhone 投递成功。本实现从 Worker 通过证书验证的 TLS socket 直连 Apple HTTP/2，使用 HPACK 解析；不通过 Linux 中转。真实推送仍要按 [验收步骤](usage.md) 测试。

## 5. 创建用户

```sh
node scripts/admin.mjs create-account --label owner --out invitations/owner.json
```

输出文件包含个人 `read_token`，私下交给对应 iPhone 用户，不要放入下载目录或 Issue。每位朋友分别创建独立账户；此精简版没有 Apple 自助注册。换 iPhone 时，需要再次输入自己的读取令牌。这里说的是登录/账户恢复方式，不影响 Apple APNs 推送，也不是 Cloudflare 免费计划禁止实现 Apple 登录。iPhone 用读取令牌连接，然后生成一次性安卓配对码。

## 6. 可选短域名

已有托管在 Cloudflare 的域名时，在 Worker → Settings → Domains & Routes → Add → Custom Domain，输入自己的 `sms.example.com`。Cloudflare 自动创建 DNS 和证书。不要只随意添加一个指向 workers.dev 的 CNAME。

同时在 `wrangler.jsonc` 记录配置，防止以后部署丢失：

```json
"routes": [{"pattern": "sms.example.com", "custom_domain": true, "zone_name": "example.com"}]
```

先确认该子域名没有在承载其他服务。短域名与原 workers.dev 域名连接同一个 Worker，账户与令牌相同。HTTPS `/healthz` 正常后，手机只填 `https://sms.example.com`。

## 7. 发布 APK 固定下载

按 [安卓文档](android.md) 用自己的签名生成 Release APK，再在 `cloudflare/` 执行：

```sh
mkdir -p public/download
cp ../android/app/build/outputs/apk/release/app-release.apk public/download/android.apk
npm run deploy -- --free-plan-confirmed
```

有签名配置时文件名为 `app-release.apk`；未签名产物不能作为正式安装包发布。地址固定为 `https://你的Worker域名/download/android.apk`，响应设置 `Cache-Control: no-store`；后续替换文件并部署，地址不变。APK 和密钥不进 Git，安装包可另作为 GitHub Release 附件。仓库中的下载页在未放置 APK 前会出现下载 404，属于尚未发布安装包。

Workers 静态资源单文件上限为 25 MiB；当前 APK 约 2 MiB。静态资源请求免费、不执行本项目 Worker API；若以后 APK 超限或需要大量历史版本，再评估 R2。R2 Standard 有免费额度但超额会计费，不是无限免费对象存储。

## 8. 可选隔离线上检查

```sh
node scripts/smoke.mjs
```

使用 `SMS_ENDPOINT` 指定的服务，创建两个虚构账户，验证配对、隔离、去重、已读 2→1→0 和 APNs 提供者连接，结束删除这两个测试账户。会消耗请求额度及短暂占用两个账户名额，不能证明真实 iPhone 已收到提醒。满额服务先不要运行。

如某些命令行客户端收到 Cloudflare 1010，先使用脚本自带 User-Agent 诊断；不要为排错关闭 HTTPS 校验或全站防护。
