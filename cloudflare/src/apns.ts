import { base64url, unbase64 } from './crypto';
import { apnsRequest } from './http2';
export interface PushEnv {
  APNS_KEY?: string; APNS_KEY_ID?: string; APNS_TEAM_ID?: string;
  APNS_TOPIC: string; APNS_ENVIRONMENT: string;
}
export interface PushResult { ok: boolean; invalid: boolean; error: string }
export class Push {
  private cached?: { token: string; until: number };
  constructor(private env: PushEnv, private request = apnsRequest) {}
  get configured(): boolean { return !!(this.env.APNS_KEY && this.env.APNS_KEY_ID && this.env.APNS_TEAM_ID); }
  async token(): Promise<string> {
    if (this.cached && this.cached.until > Date.now()) return this.cached.token;
    const der = unbase64(this.env.APNS_KEY!.replace(/-----[^-]+-----|\s/g, ''));
    const key = await crypto.subtle.importKey('pkcs8', der, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign']);
    const unsigned = base64url(JSON.stringify({ alg: 'ES256', kid: this.env.APNS_KEY_ID })) + '.' +
      base64url(JSON.stringify({ iss: this.env.APNS_TEAM_ID, iat: Math.floor(Date.now() / 1000) }));
    const signature = await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, key, new TextEncoder().encode(unsigned));
    const token = unsigned + '.' + base64url(signature);
    this.cached = { token, until: Date.now() + 40 * 60_000 };
    return token;
  }
  async send(device: string, badge: number): Promise<PushResult> {
    const host = this.env.APNS_ENVIRONMENT === 'sandbox' ? 'api.sandbox.push.apple.com' : 'api.push.apple.com';
    try {
      const response = await this.request(host, `/3/device/${device}`, {
          authorization: `bearer ${await this.token()}`, 'apns-topic': this.env.APNS_TOPIC,
          'apns-push-type': 'alert', 'apns-priority': '10',
          'apns-collapse-id': 'private-sms-inbox',
          'apns-expiration': String(Math.floor(Date.now() / 1000) + 3600),
          'content-type': 'application/json'
        }, JSON.stringify({ aps: { alert: { title: 'private-sms-server', body: '收到新短信，打开应用查看' }, sound: 'default', 'thread-id': 'inbox', badge: Math.max(0, badge) } }));
      if (response.status === 200) { await response.body?.cancel(); return { ok: true, invalid: false, error: '' }; }
      const body = await response.json<{ reason?: string }>().catch(() => ({} as { reason?: string }));
      if (body.reason === 'ExpiredProviderToken') this.cached = undefined;
      return { ok: false, invalid: ['Unregistered', 'BadDeviceToken', 'DeviceTokenNotForTopic'].includes(body.reason || ''), error: `APNs HTTP ${response.status}` };
    } catch { return { ok: false, invalid: false, error: 'APNs transport unavailable' }; }
  }
}
