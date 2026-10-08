# Linux x86_64 安装包

本包包含静态 Go 程序、systemd 服务、Nginx 示例和校验文件。以 root 运行 `./install.sh`，脚本核对 SHA-256，创建 private-sms 用户，安装程序和 systemd 单元；不生成密钥、不启动服务。

完整说明：https://github.com/hvsop/sms-forward-solutions/blob/main/docs/linux.md

首次初始化：`/opt/private-sms/bin/private-sms -init -config /etc/private-sms/server.env`。配置 DATA_DIR=/var/lib/private-sms、LISTEN=127.0.0.1:9080、自己的 APNs .p8/Key ID/Team ID/Bundle ID；配置文件 root:private-sms 0640。创建账户后 `systemctl enable --now private-sms`。

服务仅通过 HTTPS 代理向手机开放。已有数据升级时保留 STORAGE_KEY、完整数据和账户凭据，不能再次初始化。备份配置和数据后再升级；回滚程序前确认存储兼容性。包内不包含生产密钥、账户或短信。
