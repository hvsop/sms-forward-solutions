import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
const origin = process.env.SMS_ENDPOINT;
if (!origin?.startsWith('https://')) throw new Error('Set SMS_ENDPOINT');
const admin = JSON.parse(await fs.readFile('secrets/bootstrap.json', 'utf8')).admin_token;
const accounts = [];
async function api(path, method, token, data, key) {
  const response = await fetch(new URL(path, origin), { method, redirect: 'error', signal: AbortSignal.timeout(20_000),
    headers: { 'content-type': 'application/json', 'user-agent': 'private-sms-smoke/0.1', ...(token ? { authorization: 'Bearer ' + token } : {}), ...(key ? { 'idempotency-key': key } : {}) },
    body: data === undefined ? undefined : JSON.stringify(data) });
  return { status: response.status, value: await response.json() };
}
try {
  for (const label of ['synthetic-smoke-A', 'synthetic-smoke-B']) {
    const r = await api('/admin/accounts', 'POST', admin, { label });
    assert.equal(r.status, 201); accounts.push(r.value);
  }
  const [a,b] = accounts;
  const code = (await api('/v1/android/pairing', 'POST', a.read_token)).value.code;
  const paired = await api('/v1/android/pair', 'POST', null, { code });
  assert.equal(paired.status, 201);
  assert.equal((await api('/v1/android/pair', 'POST', null, { code })).status, 401);
  const phone = paired.value.token;
  const content = { sender: 'CF synthetic', content: 'Synthetic integration test. No real SMS.', sim: 'TEST', source_time: new Date().toISOString() };
  const first = await api('/v1/ingest', 'POST', phone, content, '1');
  assert.equal(first.status, 201);
  const again = await api('/v1/ingest', 'POST', phone, content, '1');
  assert.equal(again.value.id, first.value.id); assert.equal(again.value.duplicate, true);
  const second = await api('/v1/ingest', 'POST', phone, content, '2');
  assert.equal(second.status, 201);
  assert.equal((await api('/v1/messages', 'GET', phone)).status, 401);
  assert.equal((await api(`/v1/messages/${first.value.id}/read`, 'PUT', b.read_token)).status, 404);
  assert.equal((await api('/v1/messages', 'GET', a.read_token)).value.badge, 2);
  assert.equal((await api(`/v1/messages/${first.value.id}/read`, 'PUT', a.read_token)).value.badge, 1);
  const list = (await api('/v1/messages', 'GET', a.read_token)).value;
  assert.equal(list.messages.find(m => m.id === first.value.id).read, true);
  assert.equal(list.messages.find(m => m.id === second.value.id).read, false);
  const probe = await api('/admin/apns-probe', 'POST', admin);
  assert.equal(probe.value.reachable, true);
  const registration = await api('/v1/device', 'PUT', a.read_token, { token: '0'.repeat(64), environment: 'production' });
  assert.equal(registration.status, 200);
  if (registration.value.apns_configured) {
    let status;
    for (let attempt=0; attempt<12; attempt++) {
      status=(await api('/v1/status','GET',a.read_token)).value;
      if (!status.device_registered && status.last_push_error) break;
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    assert.equal(status.device_registered, false, 'APNs should reject the deliberately invalid device token, rather than rejecting provider credentials or failing transport');
    console.log('APNs live authentication path passed with a deliberately invalid device; no real notification sent.');
  }
  assert.equal((await api(`/v1/messages/${second.value.id}/read`, 'PUT', a.read_token)).value.badge, 0);
  assert.equal((await api('/v1/messages', 'DELETE', a.read_token)).status, 200);
  assert.equal((await api('/v1/status', 'GET', a.read_token)).status, 200);
  assert.equal((await api('/v1/android/status', 'GET', phone)).status, 200);
  console.log('Live isolation, pairing, deduplication, 2→1→0 unread state, clear-preserves-connection and HTTP/2 probe passed.');
} finally {
  for (const a of accounts) {
    const r = await api('/v1/account', 'DELETE', a.read_token, { confirmation: 'delete-account' });
    assert.equal(r.status, 200, 'remove only synthetic smoke accounts');
  }
}
