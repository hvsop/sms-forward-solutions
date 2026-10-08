# Go / Linux 从零部署

Go 标准库实现，无第三方 Go 依赖。适合单进程、小规模使用；每个数据目录只能运行一个进程，不能把同一文件数据目录用于多副本横向扩展。

## 1. 本地构建与快速启动

安装 Go 1.24+：

```sh
go test -race ./...
go vet ./...
go build -trimpath -o bin/private-sms ./server
./bin/private-sms -init -config .env
./bin/private-sms -config .env -create-user owner \
  -endpoint https://sms.example.com -out invitations/owner.json
./bin/private-sms -config .env
```

首次 `-init` 生成随机 32-byte 存储密钥，拒绝覆盖现有文件。默认 `127.0.0.1:8080`，另一个终端访问 `curl http://127.0.0.1:8080/healthz`。手机需要公网可达的 HTTPS，可使用本地 Cloudflare Tunnel 转发至 8080 做调试；正式部署见下。

## 2. Linux x86_64 安装包

在仓库根目录：

```sh
./scripts/build-linux.sh
scp dist/private-sms-linux-amd64.tar.gz YOUR_USER@YOUR_SERVER:/tmp/
```

该脚本只打包源码编译出的静态程序、部署示例和说明，不包含短信或密钥。ARM64 可用 `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/private-sms ./server` 自行构建，并按同样的路径手工安装；附带安装脚本只接受 x86_64。

服务器上：

```sh
tar -xzf /tmp/private-sms-linux-amd64.tar.gz
cd private-sms-linux-amd64
sudo ./install.sh
sudo /opt/private-sms/bin/private-sms -init -config /etc/private-sms/server.env
```

编辑 `/etc/private-sms/server.env`（不要当 shell 脚本 source）：

```dotenv
LISTEN=127.0.0.1:9080
DATA_DIR=/var/lib/private-sms
# 保留 -init 生成的 STORAGE_KEY，不要替换成示例字符串
RETENTION_DAYS=7
APNS_KEY_FILE=/etc/private-sms/apns.p8
APNS_KEY_ID=YOUR_KEY_ID
APNS_TEAM_ID=YOUR_TEAM_ID
APNS_TOPIC=com.yourcompany.yourapp
APNS_ENVIRONMENT=production
```

将自己的 APNs .p8 私下传至 `/etc/private-sms/apns.p8`，然后：

```sh
sudo chown root:private-sms /etc/private-sms/server.env /etc/private-sms/apns.p8
sudo chmod 640 /etc/private-sms/server.env /etc/private-sms/apns.p8
sudo -u private-sms /opt/private-sms/bin/private-sms \
  -config /etc/private-sms/server.env -create-user owner \
  -endpoint https://sms.example.com -out /var/lib/private-sms/invitations/owner.json
sudo systemctl enable --now private-sms
curl --fail http://127.0.0.1:9080/healthz
```

没有配置 APNs 时，将 `APNS_KEY_FILE` 留空并暂不执行针对不存在密钥的 chmod/chown；其他功能可先联调。开启推送前按 [iOS/APNs](ios.md) 完成密钥配置。systemd 自带 9080 监听环境变量，覆盖文件中的 LISTEN。

## 3. 域名与 HTTPS

DNS A/AAAA 指向自己的服务器。若 Cloudflare 只管理 DNS，可用 DNS only，正文不经过 Cloudflare HTTPS 代理；橙云代理会终止 TLS，应理解这个处理边界。

推荐用 Caddy 自动申请和续期证书。安装 Caddy 后，把 `deploy/Caddyfile` 中 `sms.example.com` 替换为真实域名，合并到服务器 Caddy 配置，再验证重载。只对公网开放所需的 80/443；9080 继续仅监听回环地址。Caddy 及其他已有站点配置由你管理，不要覆盖服务器上其他站点。

使用 Nginx 时，参考 `deploy/nginx.conf`：先启用 80 端口 ACME webroot，使用 Certbot 为真实域名签证书，再启用 443 的证书路径与 `proxy_pass http://127.0.0.1:9080`。先 `nginx -t` 再重载。续期后调用 `deploy/renew-nginx.sh`；通过 `certbot renew --dry-run` 检查续期。

```sh
curl --fail https://sms.example.com/healthz
```

`healthz` 通过不代表推送可用；还需在 iPhone 配置令牌、注册通知并真机测试。

## 4. 账户与可选 Apple 登录

管理员每次使用不同别名和输出路径执行 `-create-user`。个人读取令牌位于邀请文件，分享给该用户本人即可；`ingest_token` 是旧 Webhook 上传令牌，不是 iPhone 读取令牌。原生安卓使用 iPhone 生成的配对码，无需输入长期上传令牌。

需要 Apple 登录/自助邀请码时，见 [Apple 自助开通](self-service.md)。不配置时使用读取令牌方式，Cloudflare 也是这个流程。

## 5. 发布 APK

```sh
sudo install -d -o private-sms -g private-sms -m 700 /var/lib/private-sms/downloads
sudo install -o private-sms -g private-sms -m 600 /path/to/signed.apk \
  /var/lib/private-sms/downloads/private-sms-android.apk.next
sudo mv /var/lib/private-sms/downloads/private-sms-android.apk.next \
  /var/lib/private-sms/downloads/private-sms-android.apk
```

固定地址 `/download/android.apk`，不开放目录列表；手机端下载链接需按自己的地址修改。也可以仅在 Cloudflare 或 GitHub Releases 托管 APK。

## 6. 维护

```sh
systemctl status private-sms
journalctl -u private-sms -n 50 --no-pager
```

配置与 APNs Secret 更新后重启服务。升级时备份、替换二进制再重启；不要重新初始化主密钥。备份、恢复、停机迁移见 [运维说明](operations.md)。
