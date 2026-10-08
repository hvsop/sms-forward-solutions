import { describe, it, expect } from 'vitest';
import { ResponseFrames, encodeHeaders, frame } from '../src/http2';
describe('bounded HTTP/2 transport', () => {
  it('accepts fragmented frames, continuation headers, settings, ping and error JSON', async () => {
    const p = new ResponseFrames();
    expect(p.accept(frame(4, 0, 0)).replies[0]).toEqual(frame(4, 1, 0));
    const ping = new Uint8Array(8).fill(1);
    expect(p.accept(frame(6, 0, 0, ping)).replies[0]).toEqual(frame(6, 1, 0, ping));
    const headers = encodeHeaders({ ':status': '403', 'apns-id': 'synthetic' });
    expect(p.accept(frame(1, 0, 1, headers.slice(0, 7))).response).toBeUndefined();
    p.accept(frame(9, 4, 1, headers.slice(7)));
    const data = frame(0, 1, 1, new TextEncoder().encode('{"reason":"MissingProviderToken"}'));
    for (const byte of data.slice(0, -1)) expect(p.accept(new Uint8Array([byte])).response).toBeUndefined();
    const result = p.accept(data.slice(-1)).response!;
    expect(result.status).toBe(403); expect(await result.json()).toEqual({ reason: 'MissingProviderToken' });
  });
  it('handles indexed success headers and refuses oversized or interrupted frames', () => {
    expect(new ResponseFrames().accept(frame(1, 5, 1, new Uint8Array([0x88]))).response!.status).toBe(200);
    expect(() => new ResponseFrames().accept(frame(0, 1, 1, new Uint8Array(17000)))).toThrow();
    const p = new ResponseFrames(); p.accept(frame(1, 0, 1, new Uint8Array([0x88])));
    expect(() => p.accept(frame(0, 1, 1))).toThrow();
  });
});
