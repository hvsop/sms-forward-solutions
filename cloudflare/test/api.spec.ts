import { env } from 'cloudflare:workers';
import { runInDurableObject, runDurableObjectAlarm } from 'cloudflare:test';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { Push } from '../src/apns';
import { base64, Vault } from '../src/crypto';
import worker, { type Env, LIMITS } from '../src/index';
const e = env as unknown as Env;
const admin = 'a'.repeat(64);
async function api(path: string, method = 'GET', token?: string, data?: unknown, key?: string) {
  const headers: Record<string, string> = { 'content-type': 'application/json' };
  if (token) headers.authorization = 'Bearer ' + token;
  if (key) headers['idempotency-key'] = key;
  return worker.fetch(new Request('https://free.example' + path, { method, headers, body: data === undefined ? undefined : JSON.stringify(data) }), e);
}
async function owner(label = 'test') {
  const r = await api('/admin/accounts', 'POST', admin, { label });
  expect(r.status).toBe(201);
  return r.json<{ read_token: string; account_id: string }>();
}
async function pairing(token: string) {
  const p = await (await api('/v1/android/pairing', 'POST', token)).json<{ code: string }>();
  return p.code;
}
async function phone(token: string) {
  const r = await api('/v1/android/pair', 'POST', undefined, { code: await pairing(token) });
  expect(r.status).toBe(201);
  return (await r.json<{ token: string }>()).token;
}
const text = { sender: 'TEST', content: 'synthetic secret 987654', sim: 'test', source_time: '2026-10-09T00:00:00Z' };
async function send(token: string, key: string) {
  const r = await api('/v1/ingest', 'POST', token, text, key);
  expect([200, 201]).toContain(r.status);
  return r.json<{ id: string; duplicate: boolean }>();
}
const stub = () => e.INBOX.get(e.INBOX.idFromName('private-sms-free-v1'));
beforeEach(async () => {
  await runInDurableObject(stub(), async (instance: any, state) => {
    await state.storage.deleteAlarm();
    state.storage.sql.exec('DELETE FROM messages; DELETE FROM accounts; DELETE FROM counters;');
    instance.push = new Push(e);
  });
});
afterEach(() => { vi.restoreAllMocks(); });

describe('compatible SMS API in real Workers/SQLite runtime', () => {
  it('isolates roles and accounts, persists individual read state and revisions', async () => {
    const a = await owner('A'), b = await owner('B'), p = await phone(a.read_token);
    const first = await send(p, '1'), second = await send(p, '2');
    expect((await api('/v1/messages', 'GET', p)).status).toBe(401);
    expect((await api('/v1/ingest', 'POST', a.read_token, text)).status).toBe(404);
    expect((await api(`/v1/messages/${first.id}/read`, 'PUT', b.read_token)).status).toBe(404);
    const before = await (await api('/v1/messages', 'GET', a.read_token)).json<any>();
    expect(before.badge).toBe(2); expect(before.messages.every((m: any) => !m.read)).toBe(true);
    const receipt = await (await api(`/v1/messages/${first.id}/read`, 'PUT', a.read_token)).json<any>();
    expect(receipt.badge).toBe(1); expect(+receipt.revision).toBeGreaterThan(+before.revision);
    const after = await (await api('/v1/messages', 'GET', a.read_token)).json<any>();
    expect(after.messages.find((m: any) => m.id === first.id).read).toBe(true);
    expect(after.messages.find((m: any) => m.id === second.id).read).toBe(false);
    expect(await (await api(`/v1/messages/${first.id}/read`, 'PUT', a.read_token)).json()).toEqual(receipt);
    expect((await (await api('/v1/messages/seen', 'POST', a.read_token, { through: '2' })).json<any>()).badge).toBe(1);
    expect((await (await api(`/v1/messages/${second.id}/read`, 'PUT', a.read_token)).json<any>()).badge).toBe(0);
    expect((await (await api('/v1/messages', 'GET', b.read_token)).json<any>()).messages).toEqual([]);
  });
  it('deduplicates concurrent uploads and consumes pairing codes once', async () => {
    const a = await owner(), code = await pairing(a.read_token);
    const results = await Promise.all([api('/v1/android/pair', 'POST', undefined, { code }), api('/v1/android/pair', 'POST', undefined, { code })]);
    expect(results.map(r => r.status).sort()).toEqual([201, 401]);
    const p = (await results.find(r => r.status === 201)!.json<any>()).token;
    const messages = await Promise.all([send(p, 'same'), send(p, 'same')]);
    expect(messages[0].id).toBe(messages[1].id);
    expect(messages.filter(m => m.duplicate).length).toBe(1);
    const next = await phone(a.read_token);
    expect((await api('/v1/android/status', 'GET', p)).status).toBe(401);
    expect((await api('/v1/android/status', 'GET', next)).status).toBe(200);
  });
  it('clear preserves credentials and pairing; account deletion requires confirmation', async () => {
    const a = await owner(), p = await phone(a.read_token);
    await send(p, '1');
    expect((await api('/v1/account', 'DELETE', a.read_token, {})).status).toBe(400);
    expect((await (await api('/v1/messages', 'DELETE', a.read_token)).json<any>()).badge).toBe(0);
    expect((await api('/v1/status', 'GET', a.read_token)).status).toBe(200);
    await send(p, '2');
    expect((await api('/v1/account', 'DELETE', a.read_token, { confirmation: 'delete-account' })).status).toBe(200);
    expect((await api('/v1/status', 'GET', a.read_token)).status).toBe(401);
    expect((await api('/v1/android/status', 'GET', p)).status).toBe(401);
  });
  it('encrypts SMS and devices, rejects cross-context ciphertext', async () => {
    const a = await owner(), p = await phone(a.read_token); await send(p, '1');
    const device = 'c'.repeat(64);
    expect((await api('/v1/device', 'PUT', a.read_token, { token: device, environment: 'production' })).status).toBe(200);
    await runInDurableObject(stub(), async (_instance, state) => {
      const stored = JSON.stringify(state.storage.sql.exec('SELECT * FROM messages').toArray());
      const accounts = JSON.stringify(state.storage.sql.exec('SELECT * FROM accounts').toArray());
      expect(stored).not.toContain(text.content); expect(stored).not.toContain(text.sender);
      expect(accounts).not.toContain(device); expect(accounts).not.toContain(a.read_token); expect(accounts).not.toContain(p);
    });
    const vault = new Vault(e.STORAGE_KEY), encrypted = await vault.seal(text, 'A');
    await expect(vault.open(encrypted, 'B')).rejects.toThrow();
  });
  it('enforces device ownership and environment and does not mark data read on refresh', async () => {
    const a = await owner(), b = await owner(), device = 'd'.repeat(64);
    expect((await api('/v1/device', 'PUT', a.read_token, { token: device, environment: 'sandbox' })).status).toBe(409);
    expect((await api('/v1/device', 'PUT', a.read_token, { token: device, environment: 'production' })).status).toBe(200);
    expect((await api('/v1/device', 'PUT', b.read_token, { token: device, environment: 'production' })).status).toBe(409);
    await api('/v1/device', 'DELETE', a.read_token, { token: 'e'.repeat(64) });
    expect((await (await api('/v1/status', 'GET', a.read_token)).json<any>()).device_registered).toBe(true);
    await api('/v1/device', 'DELETE', a.read_token, { token: device });
    expect((await api('/v1/device', 'PUT', b.read_token, { token: device, environment: 'production' })).status).toBe(200);
  });
  it('expires messages durably, updates unread count and has no idle polling alarm', async () => {
    const a = await owner(), p = await phone(a.read_token); await send(p, '1');
    await runInDurableObject(stub(), async (_i, state) => {
      expect(await state.storage.getAlarm()).not.toBeNull();
      state.storage.sql.exec('UPDATE messages SET expires=?', Date.now() - 1);
    });
    await runDurableObjectAlarm(stub());
    const result = await (await api('/v1/status', 'GET', a.read_token)).json<any>();
    expect(result.badge).toBe(0); expect(result.messages).toBe(0);
    await runInDurableObject(stub(), async (_i, state) => expect(await state.storage.getAlarm()).toBeNull());
  });
  it('limits pagination, payloads, expired pairing codes and free-budget writes', async () => {
    const a = await owner(), p = await phone(a.read_token);
    await send(p, '1'); await send(p, '2');
    const first = await (await api('/v1/messages?limit=1', 'GET', a.read_token)).json<any>();
    const second = await (await api('/v1/messages?limit=1&before=' + first.next_before, 'GET', a.read_token)).json<any>();
    expect(first.messages[0].id).not.toBe(second.messages[0].id); expect(second.next_before).toBe('');
    expect((await api('/v1/messages?before=NaN', 'GET', a.read_token)).status).toBe(400);
    expect((await api('/v1/ingest', 'POST', p, { ...text, content: 'x'.repeat(25000) })).status).toBe(413);
    const code = await pairing(a.read_token);
    await runInDurableObject(stub(), async (_i, state) => {
      state.storage.sql.exec('UPDATE accounts SET pairing_expires=0');
      state.storage.sql.exec('INSERT OR REPLACE INTO counters VALUES (?,?,?)', 'sms', Math.floor(Date.now()/86400_000), LIMITS.smsPerDay);
    });
    expect((await api('/v1/android/pair', 'POST', undefined, { code })).status).toBe(401);
    expect((await api('/v1/ingest', 'POST', p, text, 'over-budget')).status).toBe(429);
    expect((await send(p, '1')).duplicate).toBe(true);
  });
  it('keeps failed pushes durable, retries with latest unread badge and cancels read-message alerts', async () => {
    const a = await owner(), p = await phone(a.read_token);
    const first = await send(p, '1'); await send(p, '2');
    await api('/v1/device', 'PUT', a.read_token, { token: 'f'.repeat(64), environment: 'production' });
    await api(`/v1/messages/${first.id}/read`, 'PUT', a.read_token);
    await runInDurableObject(stub(), async (instance: any, state) => {
      instance.push = { configured: true, send: async (_device: string, badge: number) => {
        expect(badge).toBe(1);
        return { ok: false, invalid: false, error: 'APNs HTTP 503' };
      } };
      await state.storage.setAlarm(Date.now());
    });
    await runDurableObjectAlarm(stub());
    const failed = await (await api('/v1/status', 'GET', a.read_token)).json<any>();
    expect(failed.pending_notifications).toBe(1); expect(failed.last_push_error).toBe('APNs HTTP 503');
    await runInDurableObject(stub(), async (instance: any, state) => {
      expect(await state.storage.getAlarm()).not.toBeNull();
      state.storage.sql.exec('UPDATE accounts SET push_due=?', Date.now()-1);
      instance.push = { configured: true, send: async (_device: string, badge: number) => {
        expect(badge).toBe(1); return { ok: true, invalid: false, error: '' };
      } };
    });
    await runDurableObjectAlarm(stub());
    const success = await (await api('/v1/status', 'GET', a.read_token)).json<any>();
    expect(success.pending_notifications).toBe(0); expect(success.badge).toBe(1);
    expect(success.last_push_error).toBe('');
  });
});

describe('APNs privacy and errors', () => {
  it('signs ES256, sends only generic alert and count, classifies invalid devices', async () => {
    const key = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
    const pem = '-----BEGIN PRIVATE KEY-----\n' + base64(await crypto.subtle.exportKey('pkcs8', key.privateKey)) + '\n-----END PRIVATE KEY-----';
    const fetchSpy = vi.fn(async (host: string, path: string, headers: Record<string,string>, body: string) => {
      expect(host).toBe('api.push.apple.com');
      expect(path).toBe('/3/device/' + 'c'.repeat(64));
      const value = JSON.parse(body);
      expect(value).toEqual({ aps: { alert: { title: 'private-sms-server', body: '收到新短信，打开应用查看' }, sound: 'default', 'thread-id': 'inbox', badge: 2 } });
      const jwt = headers.authorization.slice(7);
      const parts = jwt.split('.');
      const signature = Uint8Array.from(atob(parts[2].replace(/-/g,'+').replace(/_/g,'/')), c => c.charCodeAt(0));
      expect(await crypto.subtle.verify({name:'ECDSA',hash:'SHA-256'}, key.publicKey, signature, new TextEncoder().encode(parts[0]+'.'+parts[1]))).toBe(true);
      return new Response(null, {status:200});
    });
    const push = new Push({ APNS_KEY: pem, APNS_KEY_ID: 'TEST', APNS_TEAM_ID: 'TEST', APNS_TOPIC: 'com.example.privatesms', APNS_ENVIRONMENT: 'production' }, fetchSpy);
    expect((await push.send('c'.repeat(64), 2)).ok).toBe(true);
    expect(fetchSpy).toHaveBeenCalledOnce();
    fetchSpy.mockResolvedValue(Response.json({reason:'Unregistered'}, {status:410}));
    expect((await push.send('c'.repeat(64), 2)).invalid).toBe(true);
  });
});
