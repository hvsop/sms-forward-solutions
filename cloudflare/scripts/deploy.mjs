import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
if (!process.argv.includes('--free-plan-confirmed')) {
  console.error('Deployment stopped. Confirm Workers FREE in the Cloudflare dashboard, then run npm run deploy -- --free-plan-confirmed. Do not upgrade or add billing for this edition.');
  process.exit(1);
}
const config = JSON.parse(fs.readFileSync('wrangler.jsonc', 'utf8'));
for (const key of ['containers', 'r2_buckets', 'queues', 'd1_databases', 'kv_namespaces', 'triggers']) {
  if (config[key]) throw new Error('Free edition does not need ' + key);
}
for (const args of [['run', 'check'], ['test']]) {
  const result = spawnSync('npm', args, { stdio: 'inherit' });
  if (result.status !== 0) process.exit(result.status ?? 1);
}
const args = ['deploy'];
if (fs.existsSync('secrets/worker.json')) args.push('--secrets-file', 'secrets/worker.json');
const result = spawnSync('node_modules/.bin/wrangler', args, { stdio: 'inherit' });
process.exit(result.status ?? 1);
