import { connect } from 'cloudflare:sockets';
import { Buffer } from 'node:buffer';
import hpack from 'hpack.js';

const utf8 = new TextEncoder();
function concat(...values: Uint8Array[]): Uint8Array<ArrayBuffer> {
  const result = new Uint8Array(values.reduce((sum, b) => sum + b.length, 0));
  let pos = 0; for (const value of values) { result.set(value, pos); pos += value.length; }
  return result;
}
function string(value: string): Uint8Array {
  const bytes = utf8.encode(value), prefix: number[] = [];
  let n = bytes.length;
  if (n < 127) prefix.push(n);
  else { prefix.push(127); n -= 127; while (n >= 128) { prefix.push((n & 127) | 128); n >>>= 7; } prefix.push(n); }
  return concat(new Uint8Array(prefix), bytes);
}
// One fresh TLS connection and one stream per APNs submission. Literal
// never-indexed fields keep bearer and device credentials out of HPACK tables.
export function encodeHeaders(headers: Record<string, string>): Uint8Array {
  return concat(...Object.entries(headers).map(([name, value]) => concat(new Uint8Array([0x10]), string(name), string(value))));
}
export function frame(type: number, flags: number, stream: number, data: Uint8Array = new Uint8Array()): Uint8Array {
  const head = new Uint8Array(9); head[0] = data.length >>> 16; head[1] = data.length >>> 8; head[2] = data.length;
  head[3] = type; head[4] = flags; new DataView(head.buffer).setUint32(5, stream);
  return concat(head, data);
}
export class ResponseFrames {
  private buffer: Uint8Array<ArrayBuffer> = new Uint8Array();
  private headerBlock: Uint8Array<ArrayBuffer> = new Uint8Array();
  private body: Uint8Array<ArrayBuffer> = new Uint8Array();
  private continuing = false;
  private ended = false;
  private status = 0;
  private frames = 0;
  private decoder = hpack.decompressor.create({ table: { size: 4096 } });
  constructor() { this.decoder.on('error', () => { throw new Error('invalid APNs headers'); }); }
  accept(chunk: Uint8Array): { response?: Response; replies: Uint8Array[] } {
    this.buffer = concat(this.buffer, chunk);
    if (this.buffer.length > 65536) throw new Error('APNs response too large');
    const replies: Uint8Array[] = [];
    while (this.buffer.length >= 9) {
      const length = this.buffer[0] * 65536 + this.buffer[1] * 256 + this.buffer[2];
      if (length > 16384) throw new Error('APNs frame too large');
      if (this.buffer.length < length + 9) break;
      if (++this.frames > 128) throw new Error('too many APNs frames');
      const type = this.buffer[3], flags = this.buffer[4];
      const stream = new DataView(this.buffer.buffer, this.buffer.byteOffset).getUint32(5) & 0x7fffffff;
      let payload = this.buffer.slice(9, 9 + length);
      this.buffer = this.buffer.slice(9 + length);
      if (this.continuing && (type !== 9 || stream !== 1)) throw new Error('interrupted APNs headers');
      if (type === 4) { // SETTINGS
        if (stream !== 0 || (flags & 1 ? length !== 0 : length % 6 !== 0)) throw new Error('invalid APNs settings');
        if (!(flags & 1)) replies.push(frame(4, 1, 0));
      } else if (type === 6) { // PING
        if (stream !== 0 || length !== 8) throw new Error('invalid APNs ping');
        if (!(flags & 1)) replies.push(frame(6, 1, 0, payload));
      } else if (type === 7 || type === 3) throw new Error('APNs closed stream');
      else if (stream === 1 && (type === 1 || type === 9)) {
        if ((type === 9) !== this.continuing) throw new Error('invalid APNs continuation');
        if (type === 1) {
          if (flags & 8) { const padding = payload[0]; if (padding >= payload.length) throw new Error('invalid padding'); payload = payload.slice(1, payload.length - padding); }
          if (flags & 32) { if (payload.length < 5) throw new Error('invalid priority'); payload = payload.slice(5); }
          if (flags & 1) this.ended = true;
        }
        this.headerBlock = concat(this.headerBlock, payload);
        if (this.headerBlock.length > 8192) throw new Error('APNs headers too large');
        this.continuing = !(flags & 4);
        if (!this.continuing) {
          this.decoder.write(Buffer.from(this.headerBlock)); this.decoder.execute();
          let h, decoded = 0;
          while ((h = this.decoder.read())) {
            decoded += h.name.length + h.value.length;
            if (decoded > 16384) throw new Error('APNs decoded headers too large');
            if (h.name === ':status') this.status = Number(h.value);
          }
          this.headerBlock = new Uint8Array();
        }
      } else if (stream === 1 && type === 0) {
        if (flags & 8) { const padding = payload[0]; if (padding >= payload.length) throw new Error('invalid padding'); payload = payload.slice(1, payload.length - padding); }
        this.body = concat(this.body, payload);
        if (this.body.length > 4096) throw new Error('APNs body too large');
        if (flags & 1) this.ended = true;
      }
      if (this.ended && !this.continuing) {
        if (!Number.isInteger(this.status) || this.status < 200 || this.status > 599) throw new Error('invalid APNs status');
        return { response: new Response(this.body, { status: this.status }), replies };
      }
    }
    return { replies };
  }
}
export async function apnsRequest(host: string, path: string, headers: Record<string, string>, body: string): Promise<Response> {
  if (!['api.push.apple.com', 'api.sandbox.push.apple.com'].includes(host) || !/^\/3\/device\/[a-f0-9]{32,512}$/.test(path)) throw new Error('invalid APNs destination');
  const socket = connect({ hostname: host, port: 443 }, { secureTransport: 'on', allowHalfOpen: false });
  void socket.closed.catch(() => {});
  let timer: ReturnType<typeof setTimeout>;
  const timeout = new Promise<never>((_resolve, reject) => { timer = setTimeout(() => { void socket.close().catch(() => {}); reject(new Error('APNs timeout')); }, 8000); });
  try {
    return await Promise.race([timeout, (async () => {
      await socket.opened;
      const writer = socket.writable.getWriter(), reader = socket.readable.getReader();
      // Disable server push. APNs accepts the HTTP/2 connection preface over TLS.
      await writer.write(concat(utf8.encode('PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n'), frame(4, 0, 0, new Uint8Array([0, 2, 0, 0, 0, 0])),
        frame(1, 4, 1, encodeHeaders({ ':method': 'POST', ':scheme': 'https', ':authority': host, ':path': path, ...headers })),
        frame(0, 1, 1, utf8.encode(body))));
      const parser = new ResponseFrames();
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) throw new Error('incomplete APNs response');
        const result = parser.accept(chunk.value);
        for (const reply of result.replies) await writer.write(reply);
        if (result.response) return result.response;
      }
    })()]);
  } finally { clearTimeout(timer!); await socket.close().catch(() => {}); }
}
