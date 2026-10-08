import { cloudflareTest } from '@cloudflare/vitest-plugin';
import { defineConfig } from 'vitest/config';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
export default defineConfig({
  // Prebundle the legacy CommonJS cycle exactly as the production bundler does.
  resolve: { alias: { 'hpack.js': fileURLToPath(new URL('./.test/hpack.mjs', import.meta.url)) } },
  plugins: [cloudflareTest({
    wrangler: { configPath: './wrangler.jsonc' },
    miniflare: { bindings: {
      STORAGE_KEY: '11'.repeat(32),
      ADMIN_TOKEN_HASH: createHash('sha256').update('Bearer ' + 'a'.repeat(64)).digest('hex')
    } }
  })],
  test: { fileParallelism: false }
});
