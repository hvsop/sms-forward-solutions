import fs from 'node:fs/promises';
import path from 'node:path';
const [command, ...args] = process.argv.slice(2);
const option = (key) => args[args.indexOf(key) + 1];
try {
  const endpoint = process.env.SMS_ENDPOINT;
  if (!endpoint || new URL(endpoint).protocol !== 'https:') throw new Error('Set SMS_ENDPOINT to the HTTPS Worker origin.');
  const adminFile = process.env.SMS_ADMIN_FILE || 'secrets/bootstrap.json';
  const token = JSON.parse(await fs.readFile(adminFile, 'utf8')).admin_token;
  if (!/^[a-f0-9]{64}$/.test(token)) throw new Error('Invalid admin file.');
  let route, method = 'GET', data, output;
  if (command === 'create-account') {
    if (!args.includes('--label') || !args.includes('--out')) throw new Error('create-account --label NAME --out PRIVATE_FILE');
    output = option('--out');
    await fs.mkdir(path.dirname(output), { recursive: true, mode: 0o700 });
    // Reserve before requesting creation; never overwrite an existing invitation.
    const f = await fs.open(output, 'wx', 0o600); await f.close();
    route = '/admin/accounts'; method = 'POST'; data = { label: option('--label') };
  } else if (command === 'usage') route = '/admin/usage';
  else if (command === 'probe') { route = '/admin/apns-probe'; method = 'POST'; }
  else throw new Error('Commands: create-account, usage, probe');
  const response = await fetch(new URL(route, endpoint), { method, redirect: 'error', signal: AbortSignal.timeout(20_000),
    headers: { authorization: 'Bearer ' + token, 'content-type': 'application/json', 'user-agent': 'private-sms-admin/0.1' },
    body: data ? JSON.stringify(data) : undefined });
  const value = await response.json();
  if (!response.ok) throw new Error(`HTTP ${response.status}: ${value.error || 'request failed'}`);
  if (output) {
    await fs.writeFile(output, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
    console.log(`Account invitation saved to ${output}; credentials were not printed.`);
  } else console.log(JSON.stringify(value, null, 2));
} catch (error) { console.error(error.message); process.exitCode = 1; }
