import { DurableObject } from 'cloudflare:workers';
import { hash, newCode, normalizeCode, randomHex, Vault } from './crypto';
import { Push, type PushEnv } from './apns';
import { apnsRequest } from './http2';

export interface Env extends PushEnv {
  INBOX: DurableObjectNamespace<Inbox>;
  STORAGE_KEY: string;
  ADMIN_TOKEN_HASH: string;
}
// Deliberately bounded, invitation-only personal deployment. These are app limits,
// not a guarantee against exhaustion of Cloudflare's account-wide free quotas.
export const LIMITS = { accounts: 10, inbox: 500, smsPerDay: 500, requestsPerDay: 6000, pushesPerDay: 1000, retention: 7 * 86400_000 };
type Account = {
  id: string; label: string; read_hash: string; android_hash: string | null;
  android_id: string; paired_at: string; last_seen: string;
  device: string | null; device_hash: string | null;
  pairing_hash: string | null; pairing_expires: number;
  sequence: number; revision: number; badge: number; messages: number;
  push_due: number | null; attempts: number; last_error: string;
};
type Message = { id: string; account_id: string; seq: number; payload: string; read: number; pending: number; expires: number; received_at: string };
type Content = { sender: string; content: string; sim: string; source_time: string };
class APIError extends Error { constructor(readonly status: number, message: string) { super(message); } }
function json(value: unknown, status = 200): Response {
  return Response.json(value, { status, headers: { 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff', 'Referrer-Policy': 'no-referrer', ...(status === 429 ? { 'Retry-After': '60' } : {}) } });
}
async function body(request: Request, allowed: string[]): Promise<Record<string, unknown>> {
  if (!request.headers.get('content-type')?.toLowerCase().startsWith('application/json')) throw new APIError(415, 'use JSON');
  const reader = request.body?.getReader();
  let value = '', bytes = 0;
  const decoder = new TextDecoder();
  if (reader) {
    for (;;) {
      const part = await reader.read();
      if (part.done) break;
      bytes += part.value.byteLength;
      if (bytes > 24 * 1024) { await reader.cancel(); throw new APIError(413, 'body too large'); }
      value += decoder.decode(part.value, { stream: true });
    }
    value += decoder.decode();
  }
  let parsed;
  try { parsed = JSON.parse(value); } catch { throw new APIError(400, 'invalid JSON'); }
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object' || Object.keys(parsed).some(k => !allowed.includes(k))) throw new APIError(400, 'invalid JSON fields');
  return parsed;
}
function field(data: Record<string, unknown>, key: string, max: number, required = false): string {
  const value = data[key] ?? '';
  if (typeof value !== 'string' || new TextEncoder().encode(value).length > max || (required && !value.trim())) throw new APIError(400, `invalid ${key}`);
  return value;
}
function credential(request: Request): string {
  const value = request.headers.get('authorization') || '';
  if (!/^Bearer [a-f0-9]{64}$/.test(value)) throw new APIError(401, 'unauthorized');
  return value;
}
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname === '/healthz' && request.method === 'GET') return json({ ok: true, edition: 'cloudflare-free' });
    if (!url.pathname.startsWith('/v1/') && !url.pathname.startsWith('/admin/')) return json({ error: 'not found' }, 404);
    if (!env.STORAGE_KEY || !env.ADMIN_TOKEN_HASH) return json({ error: 'service not configured' }, 503);
    // One bounded SQLite object is intentional for this small free edition.
    try { return await env.INBOX.get(env.INBOX.idFromName('private-sms-free-v1')).fetch(request); }
    catch { return json({ error: 'storage unavailable; retry later' }, 503); }
  }
} satisfies ExportedHandler<Env>;

export class Inbox extends DurableObject<Env> {
  private vault: Vault;
  private push: Push;
  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    this.vault = new Vault(env.STORAGE_KEY);
    this.push = new Push(env);
    ctx.storage.sql.exec(`
      CREATE TABLE IF NOT EXISTS accounts (
        id TEXT PRIMARY KEY, label TEXT NOT NULL, read_hash TEXT NOT NULL UNIQUE,
        android_hash TEXT UNIQUE, android_id TEXT NOT NULL DEFAULT '',
        paired_at TEXT NOT NULL DEFAULT '', last_seen TEXT NOT NULL DEFAULT '',
        device TEXT, device_hash TEXT UNIQUE,
        pairing_hash TEXT UNIQUE, pairing_expires INTEGER NOT NULL DEFAULT 0,
        sequence INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL DEFAULT 1,
        badge INTEGER NOT NULL DEFAULT 0, messages INTEGER NOT NULL DEFAULT 0,
        push_due INTEGER, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT ''
      );
      CREATE TABLE IF NOT EXISTS messages (
        id TEXT PRIMARY KEY, account_id TEXT NOT NULL, seq INTEGER NOT NULL,
        payload TEXT NOT NULL, read INTEGER NOT NULL DEFAULT 0,
        pending INTEGER NOT NULL DEFAULT 1, dedupe TEXT,
        expires INTEGER NOT NULL, received_at TEXT NOT NULL,
        UNIQUE(account_id, dedupe)
      );
      CREATE INDEX IF NOT EXISTS messages_page ON messages(account_id, seq DESC);
      CREATE INDEX IF NOT EXISTS messages_expiry ON messages(expires);
      CREATE INDEX IF NOT EXISTS messages_pending ON messages(account_id, pending);
      CREATE TABLE IF NOT EXISTS counters (name TEXT PRIMARY KEY, window INTEGER NOT NULL, count INTEGER NOT NULL);
    `);
  }
  private rows<T extends Record<string, SqlStorageValue>>(sql: string, ...values: SqlStorageValue[]): T[] {
    return this.ctx.storage.sql.exec<T>(sql, ...values).toArray();
  }
  private one<T extends Record<string, SqlStorageValue>>(sql: string, ...values: SqlStorageValue[]): T | undefined { return this.rows<T>(sql, ...values)[0]; }
  private account(id: string): Account { return this.one<Account>('SELECT * FROM accounts WHERE id=?', id)!; }
  private badge(id: string) { const a = this.account(id); return { badge: a.badge, revision: String(a.revision) }; }
  private transaction<T>(action: () => T): T { return this.ctx.storage.transactionSync(action); }
  private consume(name: string, period: number, limit: number): boolean {
    const window = Math.floor(Date.now() / period);
    const c = this.one<{ window: number; count: number }>('SELECT window,count FROM counters WHERE name=?', name);
    if (c && c.window === window && c.count >= limit) return false;
    this.ctx.storage.sql.exec('INSERT INTO counters VALUES (?,?,1) ON CONFLICT(name) DO UPDATE SET window=excluded.window,count=CASE WHEN counters.window=excluded.window THEN counters.count+1 ELSE 1 END', name, window);
    return true;
  }
  private purge(): void {
    const now = Date.now();
    const expired = this.rows<{ account_id: string; unread: number; n: number }>('SELECT account_id, SUM(1-read) unread, COUNT(*) n FROM messages WHERE expires<=? GROUP BY account_id', now);
    if (!expired.length) return;
    this.transaction(() => {
      this.ctx.storage.sql.exec('DELETE FROM messages WHERE expires<=?', now);
      for (const row of expired) this.ctx.storage.sql.exec('UPDATE accounts SET badge=MAX(0,badge-?),messages=MAX(0,messages-?),revision=revision+1 WHERE id=?', row.unread, row.n, row.account_id);
      this.ctx.storage.sql.exec('UPDATE accounts SET push_due=NULL,attempts=0 WHERE NOT EXISTS (SELECT 1 FROM messages WHERE messages.account_id=accounts.id AND pending=1)');
    });
  }
  private async schedule(): Promise<void> {
    const expiry = this.one<{ due: number | null }>('SELECT MIN(expires) due FROM messages')?.due;
    const push = this.push.configured ? this.one<{ due: number | null }>('SELECT MIN(push_due) due FROM accounts WHERE device IS NOT NULL')?.due : null;
    const dates = [expiry, push].filter((v): v is number => v != null);
    if (dates.length) await this.ctx.storage.setAlarm(Math.max(Date.now() + 100, Math.min(...dates)));
    else await this.ctx.storage.deleteAlarm();
  }
  async fetch(request: Request): Promise<Response> {
    // Serializes crypto awaits, read acknowledgement and APNs submission. No
    // request can apply a stale badge or use a credential revoked mid-request.
    return this.ctx.blockConcurrencyWhile(async () => {
      try {
        if (!this.consume('requests', 86400_000, LIMITS.requestsPerDay)) throw new APIError(429, 'daily request budget reached');
        this.purge();
        const response = await this.route(request);
        await this.schedule();
        return response;
      } catch (e) {
        if (e instanceof APIError) return json({ error: e.message }, e.status);
        return json({ error: 'service unavailable; retry later' }, 503);
      }
    });
  }
  private async route(request: Request): Promise<Response> {
    const url = new URL(request.url), path = url.pathname, method = request.method;
    if (path.startsWith('/admin/')) {
      if (await hash(credential(request)) !== this.env.ADMIN_TOKEN_HASH) throw new APIError(401, 'unauthorized');
      if (path === '/admin/accounts' && method === 'POST') {
        const input = await body(request, ['label']);
        const label = field(input, 'label', 80, true);
        if (this.one<{ n: number }>('SELECT COUNT(*) n FROM accounts')!.n >= LIMITS.accounts) throw new APIError(409, 'account limit reached');
        const id = randomHex(16), token = randomHex();
        this.ctx.storage.sql.exec('INSERT INTO accounts(id,label,read_hash) VALUES(?,?,?)', id, label, await hash('Bearer ' + token));
        return json({ account_id: id, label, read_token: token, endpoint: url.origin }, 201);
      }
      if (path === '/admin/usage' && method === 'GET') return json({ limits: LIMITS, counters: this.rows('SELECT * FROM counters'), accounts: this.one<{ n: number }>('SELECT COUNT(*) n FROM accounts')!.n, apns_configured: this.push.configured });
      // Tests connectivity without using any private key or device token.
      if (path === '/admin/apns-probe' && method === 'POST') {
        try {
          const res = await apnsRequest('api.push.apple.com', '/3/device/' + '0'.repeat(64), { 'content-type': 'application/json' }, '{"aps":{"badge":0}}');
          const value = await res.json<{ reason?: string }>().catch(() => ({} as { reason?: string }));
          return json({ reachable: res.status === 403 && value.reason === 'MissingProviderToken', status: res.status });
        } catch { return json({ reachable: false }, 502); }
      }
      throw new APIError(404, 'not found');
    }
    if (path.startsWith('/v1/auth/')) throw new APIError(503, 'apple_not_configured');
    if (path === '/v1/android/pair' && method === 'POST') {
      if (!this.consume('pair-attempts', 60_000, 30)) throw new APIError(429, 'try again later');
      const input = await body(request, ['code']);
      const code = normalizeCode(field(input, 'code', 100));
      if (!/^[A-Z2-7]{16}$/.test(code)) throw new APIError(401, 'pairing code invalid or expired');
      const a = this.one<Account>('SELECT * FROM accounts WHERE pairing_hash=? AND pairing_expires>?', await hash(code), Date.now());
      if (!a) throw new APIError(401, 'pairing code invalid or expired');
      const token = randomHex(), id = randomHex(16);
      this.ctx.storage.sql.exec('UPDATE accounts SET android_hash=?,android_id=?,paired_at=?,last_seen=\'\',pairing_hash=NULL,pairing_expires=0 WHERE id=?', await hash('Bearer ' + token), id, new Date().toISOString(), a.id);
      return json({ token, device_id: id, label: a.label }, 201);
    }
    const tokenHash = await hash(credential(request));
    const a = this.one<Account>('SELECT * FROM accounts WHERE read_hash=? OR android_hash=?', tokenHash, tokenHash);
    if (!a) throw new APIError(401, 'unauthorized');
    const android = a.android_hash === tokenHash;
    if (android) {
      if (path === '/v1/android/device' && method === 'DELETE') return this.unpair(a.id);
      if (path !== '/v1/ingest' && path !== '/v1/android/status') throw new APIError(401, 'unauthorized');
      if (path === '/v1/ingest' && method === 'POST') return this.ingest(request, a);
      if (path === '/v1/android/status' && method === 'GET') {
        this.touch(a); return json({ ok: true, device_id: a.android_id });
      }
      throw new APIError(405, 'method not allowed');
    }
    if (path === '/v1/android/pairing' && method === 'POST') {
      const code = newCode(), expires = Date.now() + 600_000;
      this.ctx.storage.sql.exec('UPDATE accounts SET pairing_hash=?,pairing_expires=? WHERE id=?', await hash(normalizeCode(code)), expires, a.id);
      return json({ code, expires_at: new Date(expires).toISOString() }, 201);
    }
    if (path === '/v1/android/device' && method === 'DELETE') return this.unpair(a.id);
    if (path === '/v1/account/profile' && method === 'GET') return json({ apple_linked: false, apple_available: false, android_paired: !!a.android_hash, android_device_id: a.android_id, android_paired_at: a.paired_at || '0001-01-01T00:00:00Z', android_last_seen: a.last_seen || '0001-01-01T00:00:00Z' });
    if (path === '/v1/account/logout' && method === 'POST') throw new APIError(409, 'apple_link_required');
    if (path === '/v1/account' && method === 'DELETE') {
      const input = await body(request, ['confirmation']);
      if (input.confirmation !== 'delete-account') throw new APIError(400, 'explicit account deletion confirmation required');
      this.transaction(() => { this.ctx.storage.sql.exec('DELETE FROM messages WHERE account_id=?', a.id); this.ctx.storage.sql.exec('DELETE FROM accounts WHERE id=?', a.id); });
      return json({ ok: true });
    }
    if (path === '/v1/status' && method === 'GET') return json({ ...this.badge(a.id), messages: a.messages, pending_notifications: this.one<{ n: number }>('SELECT COUNT(*) n FROM messages WHERE account_id=? AND pending=1', a.id)!.n, device_registered: !!a.device, apns_configured: this.push.configured, last_push_error: a.last_error });
    if (path === '/v1/device') {
      if (method !== 'PUT' && method !== 'DELETE') throw new APIError(405, 'method not allowed');
      const input = await body(request, method === 'PUT' ? ['token', 'environment'] : ['token']);
      const token = field(input, 'token', 512).toLowerCase();
      if (!/^(?:[a-f0-9]{2}){16,256}$/.test(token)) throw new APIError(400, 'invalid device token');
      const deviceHash = await hash(token);
      if (method === 'DELETE') {
        this.ctx.storage.sql.exec('UPDATE accounts SET device=NULL,device_hash=NULL,push_due=NULL,last_error=\'\' WHERE id=? AND device_hash=?', a.id, deviceHash);
      } else {
        if (input.environment !== this.env.APNS_ENVIRONMENT) throw new APIError(409, 'APNs environment mismatch');
        if (this.one('SELECT id FROM accounts WHERE device_hash=? AND id<>?', deviceHash, a.id)) throw new APIError(409, 'device already belongs to another account');
        const encrypted = await this.vault.seal(token, `device:${a.id}`);
        this.ctx.storage.sql.exec('UPDATE accounts SET device=?,device_hash=?,push_due=?,attempts=0,last_error=\'\' WHERE id=?', encrypted, deviceHash, Date.now(), a.id);
      }
      return json({ ok: true, apns_configured: this.push.configured });
    }
    if (path === '/v1/messages' && method === 'GET') {
      const limitString = url.searchParams.get('limit') || '50', beforeString = url.searchParams.get('before');
      if (!/^\d+$/.test(limitString) || +limitString < 1 || +limitString > 100 || (beforeString !== null && (!/^\d+$/.test(beforeString) || +beforeString < 1 || !Number.isSafeInteger(+beforeString)))) throw new APIError(400, 'invalid pagination');
      const limit = +limitString;
      const page = this.rows<Message>('SELECT * FROM messages WHERE account_id=? AND seq<? ORDER BY seq DESC LIMIT ?', a.id, beforeString ? +beforeString : Number.MAX_SAFE_INTEGER, limit + 1);
      const more = page.length > limit; if (more) page.pop();
      const messages = await Promise.all(page.map(async m => ({ ...await this.vault.open<Content>(m.payload, `message:${a.id}:${m.id}`), id: m.id, received_at: m.received_at, read: !!m.read })));
      return json({ messages, next_before: more ? String(page.at(-1)!.seq) : '', snapshot_sequence: String(a.sequence), ...this.badge(a.id) });
    }
    if (path === '/v1/messages' && method === 'DELETE') {
      this.transaction(() => { this.ctx.storage.sql.exec('DELETE FROM messages WHERE account_id=?', a.id); this.ctx.storage.sql.exec('UPDATE accounts SET messages=0,badge=0,revision=revision+1,push_due=NULL,attempts=0,last_error=\'\' WHERE id=?', a.id); });
      return json(this.badge(a.id));
    }
    if (path === '/v1/messages/seen' && method === 'POST') {
      const input = await body(request, ['through']);
      if (typeof input.through !== 'string' || !/^\d+$/.test(input.through) || +input.through > a.sequence) throw new APIError(400, 'invalid sequence');
      return json(this.badge(a.id));
    }
    const match = /^\/v1\/messages\/([a-f0-9]{48})(\/read)?$/.exec(path);
    if (match && ((method === 'PUT' && match[2]) || (method === 'DELETE' && !match[2]))) {
      const m = this.one<Message>('SELECT * FROM messages WHERE id=? AND account_id=?', match[1], a.id);
      if (!m) throw new APIError(404, 'message not found');
      this.transaction(() => {
        if (method === 'DELETE') {
          this.ctx.storage.sql.exec('DELETE FROM messages WHERE id=?', m.id);
          this.ctx.storage.sql.exec('UPDATE accounts SET messages=messages-1,badge=badge-?,revision=revision+1 WHERE id=?', 1 - m.read, a.id);
        } else if (!m.read) {
          this.ctx.storage.sql.exec('UPDATE messages SET read=1,pending=0 WHERE id=?', m.id);
          this.ctx.storage.sql.exec('UPDATE accounts SET badge=badge-1,revision=revision+1 WHERE id=?', a.id);
        }
        this.ctx.storage.sql.exec('UPDATE accounts SET push_due=NULL,attempts=0 WHERE id=? AND NOT EXISTS (SELECT 1 FROM messages WHERE account_id=? AND pending=1)', a.id, a.id);
      });
      return json(this.badge(a.id));
    }
    throw new APIError(404, 'not found');
  }
  private touch(a: Account): void {
    if (!a.last_seen || Date.now() - Date.parse(a.last_seen) >= 60_000) this.ctx.storage.sql.exec('UPDATE accounts SET last_seen=? WHERE id=?', new Date().toISOString(), a.id);
  }
  private unpair(id: string): Response {
    this.ctx.storage.sql.exec('UPDATE accounts SET android_hash=NULL,android_id=\'\',paired_at=\'\',last_seen=\'\',pairing_hash=NULL,pairing_expires=0 WHERE id=?', id);
    return json({ ok: true });
  }
  private async ingest(request: Request, a: Account): Promise<Response> {
    const input = await body(request, ['sender', 'content', 'sim', 'source_time']);
    const content: Content = { sender: field(input, 'sender', 256, true), content: field(input, 'content', 16 * 1024, true), sim: field(input, 'sim', 256), source_time: field(input, 'source_time', 100) };
    const key = request.headers.get('idempotency-key') || '';
    if (key.length > 256) throw new APIError(400, 'idempotency key too long');
    const dedupe = key ? await hash('key:' + key) : content.source_time ? await hash(JSON.stringify(content)) : null;
    if (dedupe) {
      const old = this.one<{ id: string }>('SELECT id FROM messages WHERE account_id=? AND dedupe=?', a.id, dedupe);
      if (old) return json({ ok: true, id: old.id, duplicate: true });
    }
    if (a.messages >= LIMITS.inbox) throw new APIError(507, 'inbox full; delete messages');
    if (!this.consume('sms', 86400_000, LIMITS.smsPerDay)) throw new APIError(429, 'daily SMS budget reached');
    const id = randomHex(24), now = Date.now(), payload = await this.vault.seal(content, `message:${a.id}:${id}`);
    this.transaction(() => {
      this.ctx.storage.sql.exec('INSERT INTO messages(id,account_id,seq,payload,dedupe,expires,received_at) VALUES(?,?,?,?,?,?,?)', id, a.id, a.sequence + 1, payload, dedupe, now + LIMITS.retention, new Date(now).toISOString());
      this.ctx.storage.sql.exec('UPDATE accounts SET sequence=sequence+1,revision=revision+1,badge=badge+1,messages=messages+1,push_due=?,attempts=0,last_error=\'\',last_seen=? WHERE id=?', now, new Date(now).toISOString(), a.id);
    });
    return json({ ok: true, id, duplicate: false }, 201);
  }
  async alarm(): Promise<void> {
    await this.ctx.blockConcurrencyWhile(async () => {
      this.purge();
      const a = this.push.configured ? this.one<Account>('SELECT * FROM accounts WHERE device IS NOT NULL AND push_due<=? ORDER BY push_due LIMIT 1', Date.now()) : undefined;
      if (a) {
        const pending = this.one<{ n: number }>('SELECT COUNT(*) n FROM messages WHERE account_id=? AND pending=1', a.id)!.n;
        if (!pending) this.ctx.storage.sql.exec('UPDATE accounts SET push_due=NULL WHERE id=?', a.id);
        else if (!this.consume('pushes', 86400_000, LIMITS.pushesPerDay)) {
          this.ctx.storage.sql.exec('UPDATE accounts SET push_due=?,last_error=\'daily push budget reached\' WHERE id=?', (Math.floor(Date.now() / 86400_000) + 1) * 86400_000, a.id);
        } else {
          // Persist retry before external I/O. A crash after APNs accepts may
          // repeat a generic alert, but cannot lose the persisted SMS.
          const next = Date.now() + Math.min(3600_000, 5000 * 2 ** Math.min(a.attempts, 10));
          this.ctx.storage.sql.exec('UPDATE accounts SET attempts=attempts+1,push_due=? WHERE id=?', next, a.id);
          await this.ctx.storage.setAlarm(next);
          const device = await this.vault.open<string>(a.device!, `device:${a.id}`);
          const result = await this.push.send(device, a.badge);
          this.transaction(() => {
            if (result.ok) {
              this.ctx.storage.sql.exec('UPDATE messages SET pending=0 WHERE account_id=? AND pending=1', a.id);
              this.ctx.storage.sql.exec('UPDATE accounts SET attempts=0,push_due=NULL,last_error=\'\' WHERE id=?', a.id);
            } else if (result.invalid) {
              this.ctx.storage.sql.exec('UPDATE accounts SET device=NULL,device_hash=NULL,push_due=NULL,last_error=? WHERE id=?', result.error, a.id);
            } else this.ctx.storage.sql.exec('UPDATE accounts SET last_error=? WHERE id=?', result.error, a.id);
          });
        }
      }
      await this.schedule();
    });
  }
}
