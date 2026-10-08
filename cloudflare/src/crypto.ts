const enc = new TextEncoder();
export function hex(bytes: ArrayBuffer | Uint8Array): string {
  return [...new Uint8Array(bytes)].map(b => b.toString(16).padStart(2, '0')).join('');
}
export function randomHex(size = 32): string { return hex(crypto.getRandomValues(new Uint8Array(size))); }
export async function hash(value: string): Promise<string> {
  return hex(await crypto.subtle.digest('SHA-256', enc.encode(value)));
}
export function base64(bytes: ArrayBuffer | Uint8Array): string {
  return btoa(String.fromCharCode(...new Uint8Array(bytes)));
}
export function unbase64(value: string): Uint8Array<ArrayBuffer> {
  return Uint8Array.from(atob(value), c => c.charCodeAt(0));
}
export function base64url(value: string | ArrayBuffer): string {
  return base64(typeof value === 'string' ? enc.encode(value) : value).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
}
export function newCode(): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  return [...crypto.getRandomValues(new Uint8Array(16))].map(b => alphabet[b & 31]).join('').match(/.{4}/g)!.join('-');
}
export function normalizeCode(code: string): string { return code.replace(/[-\s]/g, '').toUpperCase(); }
export class Vault {
  private key: Promise<CryptoKey>;
  constructor(secret: string) {
    if (!/^[0-9a-f]{64}$/.test(secret)) throw new Error('storage key missing');
    this.key = crypto.subtle.importKey('raw', Uint8Array.from(secret.match(/../g)!, b => parseInt(b, 16)), 'AES-GCM', false, ['encrypt', 'decrypt']);
  }
  async seal(value: unknown, context: string): Promise<string> {
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const data = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: enc.encode(context) }, await this.key, enc.encode(JSON.stringify(value)));
    return base64(iv) + '.' + base64(data);
  }
  async open<T>(value: string, context: string): Promise<T> {
    const [iv, data] = value.split('.');
    return JSON.parse(new TextDecoder().decode(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: unbase64(iv), additionalData: enc.encode(context) }, await this.key, unbase64(data))));
  }
}
