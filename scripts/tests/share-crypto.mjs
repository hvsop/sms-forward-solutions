import {readFile} from 'node:fs/promises';
import assert from 'node:assert/strict';
import {webcrypto} from 'node:crypto';
if(!globalThis.crypto)globalThis.crypto=webcrypto;
const source=await readFile(new URL('../../server/shareweb/crypto.js',import.meta.url),'utf8');
const c=await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
const raw=crypto.getRandomValues(new Uint8Array(32)),key=await c.importKey(raw),prefix=crypto.getRandomValues(new Uint8Array(8)),id='a'.repeat(32);
for(const size of [0,1,c.CHUNK,c.CHUNK+17]){
 const input=Buffer.alloc(size,0x42),output=[];
 for(let n=0;n<Math.max(1,Math.ceil(size/c.CHUNK));n++){
  const part=input.subarray(n*c.CHUNK,(n+1)*c.CHUNK),encrypted=await c.seal(key,prefix,id,n,part);
  assert.equal(encrypted.byteLength,part.byteLength+16);
  output.push(Buffer.from(await c.open(key,prefix,id,n,encrypted)));
  await assert.rejects(()=>c.open(key,prefix,id,n+1,encrypted));
  await assert.rejects(()=>c.open(key,prefix,'b'.repeat(32),n,encrypted));
  const changed=new Uint8Array(encrypted.slice(0));changed[changed.length-1]^=1;
  await assert.rejects(()=>c.open(key,prefix,id,n,changed));
 }
 assert.deepEqual(Buffer.concat(output),input);
}
const sealed=await c.seal(key,prefix,id,0xffffffff,new TextEncoder().encode('metadata'));
const wrong=await c.importKey(crypto.getRandomValues(new Uint8Array(32)));
await assert.rejects(()=>c.open(wrong,prefix,id,0xffffffff,sealed));
assert.deepEqual(c.unb64(c.b64(raw)),raw);
assert.throws(()=>c.validateManifest({v:1,name:'x',size:1,chunk_size:c.CHUNK},{size:2,chunk_size:c.CHUNK,chunks:1}));
console.log('Crypto round-trip, chunk boundaries, empty file, wrong key, tampering, file ID/index binding, and manifest validation: PASS');
