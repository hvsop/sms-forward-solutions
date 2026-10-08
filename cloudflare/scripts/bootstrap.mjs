import fs from 'node:fs/promises';
import { randomBytes, createHash } from 'node:crypto';
await fs.mkdir('secrets', { recursive: true, mode: 0o700 });
const token = randomBytes(32).toString('hex');
// Exclusive writes: running again must not rotate keys and orphan stored SMS.
await fs.writeFile('secrets/bootstrap.json', JSON.stringify({ admin_token: token }, null, 2), { flag: 'wx', mode: 0o600 });
await fs.writeFile('secrets/worker.json', JSON.stringify({ STORAGE_KEY: randomBytes(32).toString('hex'), ADMIN_TOKEN_HASH: createHash('sha256').update('Bearer ' + token).digest('hex') }, null, 2), { flag: 'wx', mode: 0o600 });
console.log('Independent secrets created in secrets/. Back up privately; never publish or regenerate STORAGE_KEY.');
