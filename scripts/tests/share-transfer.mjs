import {readFile} from 'node:fs/promises';
import assert from 'node:assert/strict';
import {webcrypto} from 'node:crypto';
if(!globalThis.crypto)globalThis.crypto=webcrypto;
const [base,invitationPath]=process.argv.slice(2);
if(!base||!invitationPath)throw Error('usage: node scripts/tests/share-transfer.mjs BASE_URL INVITATION_PATH');
const invitation=JSON.parse(await readFile(invitationPath,'utf8'));
const moduleSource=await readFile(new URL('../../server/shareweb/crypto.js',import.meta.url),'utf8');
const c=await import('data:text/javascript;base64,'+Buffer.from(moduleSource).toString('base64'));
async function req(path,{method='GET',token=invitation.upload_token,body,binary=false}={}){
 const headers={'Authorization':'Bearer '+token,'User-Agent':'PrivateSMS-Share-Test/1.0'};
 let payload=body;if(body!==undefined&&!(body instanceof ArrayBuffer)){headers['Content-Type']='application/json';payload=JSON.stringify(body);}
 const r=await fetch(base+'/v1/share/'+path,{method,headers,body:payload});
 if(!r.ok)throw new Error('HTTP '+r.status);
 return binary?r.arrayBuffer():r.json();
}
const plain=Buffer.alloc(c.CHUNK*2+137,0x53),raw=crypto.getRandomValues(new Uint8Array(32)),key=await c.importKey(raw),prefix=crypto.getRandomValues(new Uint8Array(8)),readToken=c.randomHex();
let id;
try{
 const created=await req('files',{method:'POST',body:{size:plain.length,read_token:readToken}});id=created.id;
 const manifest={v:1,name:'synthetic-transfer-test.bin',size:plain.length,type:'application/octet-stream',chunk_size:c.CHUNK};
 await req(`files/${id}/manifest`,{method:'PUT',body:{nonce:c.b64(prefix),manifest:c.b64(await c.seal(key,prefix,id,0xffffffff,new TextEncoder().encode(JSON.stringify(manifest))))}});
 for(let n=0;n<3;n++)await req(`files/${id}/chunks/${n}`,{method:'PUT',body:await c.seal(key,prefix,id,n,plain.subarray(n*c.CHUNK,(n+1)*c.CHUNK))});
 await req(`files/${id}/complete`,{method:'POST',body:{}});
 const info=await req(`files/${id}`,{token:readToken});
 const decoded=JSON.parse(new TextDecoder().decode(await c.open(key,c.unb64(info.nonce),id,0xffffffff,c.unb64(info.manifest))));
 c.validateManifest(decoded,info);
 const parts=[];for(let n=0;n<3;n++)parts.push(Buffer.from(await c.open(key,prefix,id,n,await req(`files/${id}/chunks/${n}`,{token:readToken,binary:true}))));
 assert.deepEqual(Buffer.concat(parts),plain);
 console.log('Encrypted multi-chunk transfer and exact downloaded content: PASS');
}finally{
 if(id){await req(`files/${id}`,{method:'DELETE'});await assert.rejects(()=>req(`files/${id}`,{token:readToken}),/HTTP 404/);console.log('Revocation and subsequent download denial: PASS');}
}
