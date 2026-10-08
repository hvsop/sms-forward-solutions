export const CHUNK = 1024 * 1024;
export const MAX_FILE = 100 * CHUNK;
export function b64(bytes) { let s=''; for(const b of new Uint8Array(bytes)) s+=String.fromCharCode(b); return btoa(s).replaceAll('+','-').replaceAll('/','_').replaceAll('=',''); }
export function unb64(value) { if(!/^[A-Za-z0-9_-]+$/.test(value)||value.length>6000) throw Error('无效的密钥或文件信息'); return Uint8Array.from(atob(value.replaceAll('-','+').replaceAll('_','/')),c=>c.charCodeAt(0)); }
export function randomHex() { return Array.from(crypto.getRandomValues(new Uint8Array(32)),b=>b.toString(16).padStart(2,'0')).join(''); }
export function iv(prefix,index) { if(prefix.length!==8||!Number.isInteger(index)||index<0||index>0xffffffff) throw Error('无效的分块编号'); const v=new Uint8Array(12);v.set(prefix);new DataView(v.buffer).setUint32(8,index,false);return v; }
function aad(id,index) { if(!/^[a-f0-9]{32}$/.test(id)) throw Error('无效的分享标识');return new TextEncoder().encode(`private-sms/share/v1|${id}|${index}`); }
export async function importKey(raw) { if(raw.length!==32) throw Error('解密密钥长度不正确');return crypto.subtle.importKey('raw',raw,'AES-GCM',false,['encrypt','decrypt']); }
export function seal(key,prefix,id,index,plain) {return crypto.subtle.encrypt({name:'AES-GCM',iv:iv(prefix,index),additionalData:aad(id,index),tagLength:128},key,plain);}
export function open(key,prefix,id,index,cipher) {return crypto.subtle.decrypt({name:'AES-GCM',iv:iv(prefix,index),additionalData:aad(id,index),tagLength:128},key,cipher);}
export function validateManifest(meta,server) {
 if(meta.v!==1 || typeof meta.name!=='string' || !meta.name || meta.name.length>512 || !Number.isSafeInteger(meta.size) || meta.size<0 || meta.size>MAX_FILE || meta.size!==server.size || meta.chunk_size!==CHUNK || server.chunk_size!==CHUNK || server.chunks!==Math.max(1,Math.ceil(meta.size/CHUNK))) throw Error('文件信息校验失败');
 return {...meta,name:meta.name.replace(/[\x00-\x1f/\\]/g,'_')};
}
