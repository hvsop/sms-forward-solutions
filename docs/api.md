# 短信 API 与兼容边界

除健康检查与配对兑换外，使用 `Authorization: Bearer <对应角色的令牌>`。令牌不放 URL。全部手机连接使用 HTTPS；用户身份由服务端令牌确定，客户端不传 account_id 选择其他账户。

| 方法与路径 | 凭据 | 用途 |
| --- | --- | --- |
| GET /healthz | 无 | 存活检查 |
| POST /v1/android/pairing | 读取令牌 | 生成单次配对码 |
| POST /v1/android/pair | JSON code，无 Bearer | 兑换安卓上传凭据 |
| GET /v1/android/status | 安卓凭据 | 检查当前配对 |
| DELETE /v1/android/device | 读取或安卓凭据 | 撤销安卓配对 |
| POST /v1/ingest | 安卓凭据 | 上传 SMS；JSON sender/content/sim/source_time |
| GET /v1/messages | 读取令牌 | 分页短信、badge、revision |
| PUT /v1/messages/{id}/read | 读取令牌 | 单条标为已读 |
| DELETE /v1/messages/{id} | 读取令牌 | 删除单条 |
| DELETE /v1/messages | 读取令牌 | 清空，保留账户与设备 |
| GET /v1/status | 读取令牌 | 未读、推送状态 |
| GET /v1/account/profile | 读取令牌 | 安卓配对/账户能力 |
| PUT /v1/device | 读取令牌 | 注册 APNs token/environment |
| DELETE /v1/device | 读取令牌 | 撤销该 APNs token |
| DELETE /v1/account | 读取令牌 | 注销；JSON confirmation=delete-account |

安卓上传发送 `Idempotency-Key`，重试避免重复入库。revision 为可比较的十进制字符串，iOS 拒绝过时响应覆盖较新的未读计数。错误、上限、原子性等行为以两后端测试为准。

Go 额外支持旧 `ingest_token` 与 SMSForwarder 表单；Cloudflare 只接收原生安卓 JSON。Go 的 /v1/auth/*、/v1/account/logout、/v1/share/* 不属于两版本共同接口。Cloudflare /admin/* 仅管理员使用，不给手机个人账户。

只通过一次 healthz 或 APNs probe 不等于端到端验收；见 [usage.md](usage.md)。
