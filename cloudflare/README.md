# Cloudflare 后端

完整部署步骤见 [docs/cloudflare.md](../docs/cloudflare.md)。此目录可以独立构建，使用 Workers + SQLite Durable Object + Static Assets，无 Linux 或 R2 依赖。

```sh
npm ci
npm run check
npm test
npm run build
```

不要将 `secrets/`、`invitations/`、`.dev.vars` 或 `.wrangler/` 上传到 GitHub。APNs .p8 仅上传为 Worker Secret，绝不放入 `public/`。`wrangler.jsonc` 不绑定维护者账户或域名，部署到你自己的 Cloudflare 账户。
