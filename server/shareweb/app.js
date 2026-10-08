import {CHUNK,MAX_FILE,b64,unb64,randomHex,importKey,seal,open,validateManifest} from '/share/crypto.js';
const $=id=>document.getElementById(id);
const ACCESS='private-sms.share.access.v1', LINKS='private-sms.share.links.v1';
const getStore=(s,k)=>{try{return s.getItem(k);}catch{return null;}};
const putStore=(s,k,v)=>{try{if(v===null)s.removeItem(k);else s.setItem(k,v);}catch{}};
let access=getStore(sessionStorage,ACCESS)||getStore(localStorage,ACCESS)||'';
let recent={};try{recent=JSON.parse(getStore(sessionStorage,LINKS)||'{}');}catch{}
let selected=null,current=null,transfer=null,receiving=false,downloadURL=null,readyFile=null,receiveData=null;
const params=new URLSearchParams(location.hash.slice(1));
if(params.has('access')){
 const value=params.get('access');if(/^[a-f0-9]{64}$/.test(value))access=value;
 history.replaceState(null,'',location.pathname);
}
$('access').value=access;
$('remember').checked=!!getStore(localStorage,ACCESS);
function show(text,error=false){$('message').hidden=false;$('message').textContent=text;$('message').classList.toggle('error',error);}
function hideMessage(){$('message').hidden=true;}
function size(n){return n<1024?`${n} B`:n<CHUNK?`${(n/1024).toFixed(1)} KB`:`${(n/CHUNK).toFixed(1)} MB`;}
function date(s){return new Date(s).toLocaleString();}
class HTTPError extends Error{constructor(status){super(`HTTP ${status}`);this.status=status;}}
function explain(e){
 if(e.name==='AbortError')return '操作已取消，或连接超时。';
 if(e instanceof HTTPError)return ({401:'分享权限无效，请重新使用私人启用链接。',403:'服务器拒绝连接，请检查访问权限或网络设置。',404:'文件不存在、链接不完整，或分享已过期 / 被撤销。',409:'上传未完成、文件状态冲突或个人配额已满，请刷新有效分享列表。',413:'文件分块超出限制。',429:'请求较多，请稍后重试。',507:'服务器空间配额已满，请先撤销不再需要的分享。',503:'服务暂不可用，请稍后重试。'})[e.status]||`请求失败（${e.status}）。`;
 if(e.name==='OperationError')return '解密校验失败。请确认链接完整，文件可能已损坏。';
 if(e instanceof TypeError)return '网络连接失败，请保持页面打开并检查网络。';
 return e.message||'操作失败，请重试。';
}
async function api(path,{method='GET',token=access,body,signal,binary=false,retry=false}={}){
 const attempts=retry?3:1;
 for(let n=0;n<attempts;n++){
  if(signal?.aborted)throw new DOMException('Cancelled','AbortError');
  const control=new AbortController(),cancel=()=>control.abort();signal?.addEventListener('abort',cancel,{once:true});
  const timer=setTimeout(cancel,90000);
  try{
   const headers={};if(token)headers.Authorization=`Bearer ${token}`;
   let payload=body;if(body!==undefined && !(body instanceof ArrayBuffer)&&!ArrayBuffer.isView(body)){headers['Content-Type']='application/json';payload=JSON.stringify(body);}else if(body!==undefined)headers['Content-Type']='application/octet-stream';
   const response=await fetch('/v1/share/'+path,{method,headers,body:payload,signal:control.signal,credentials:'omit',cache:'no-store',redirect:'error',referrerPolicy:'no-referrer'});
   if(!response.ok)throw new HTTPError(response.status);
   return binary?await response.arrayBuffer():await response.json();
  }catch(e){
   if(signal?.aborted||n===attempts-1||(e instanceof HTTPError&&e.status<500&&e.status!==429))throw e;
   await new Promise(resolve=>setTimeout(resolve,600*(n+1)));
  }finally{clearTimeout(timer);signal?.removeEventListener('abort',cancel);}
 }
}
function saveRecent(){putStore(sessionStorage,LINKS,JSON.stringify(recent));}
async function connect(){
 $('login-button').disabled=true;$('login-button').textContent='正在连接…';hideMessage();
 try{
  const candidate=$('access').value.trim();if(!/^[a-f0-9]{64}$/.test(candidate))throw Error('请使用私人启用链接，或输入完整的分享访问密钥。');
  await api('session',{token:candidate});access=candidate;putStore(sessionStorage,ACCESS,access);putStore(localStorage,ACCESS,$('remember').checked?access:null);
  $('login').hidden=true;$('upload-card').hidden=false;$('history-card').hidden=false;await refreshList();
 }catch(e){show(explain(e),true);}finally{$('login-button').disabled=false;$('login-button').textContent='开启文件分享';}
}
$('login-form').addEventListener('submit',e=>{e.preventDefault();connect();});
$('logout').addEventListener('click',()=>{if(transfer)return;access='';recent={};putStore(sessionStorage,ACCESS,null);putStore(localStorage,ACCESS,null);putStore(sessionStorage,LINKS,null);$('access').value='';$('login').hidden=false;$('upload-card').hidden=true;$('history-card').hidden=true;$('result').hidden=true;hideMessage();});
function selectFile(file){
 if(transfer||!file)return;
 if(file.size>MAX_FILE){show('首版支持单个文件最高 100 MB，请压缩文件或选择较小的文件。',true);return;}
 if(file.name.length>512){show('文件名过长，请缩短后重试。',true);return;}
 selected=file;$('selection-title').textContent=file.name;$('selection-detail').textContent=size(file.size);$('upload').disabled=false;hideMessage();
}
$('choose-file').addEventListener('click',()=>$('file').click());$('file').addEventListener('change',()=>selectFile($('file').files[0]));
for(const name of ['dragenter','dragover'])$('drop-zone').addEventListener(name,e=>{e.preventDefault();$('drop-zone').classList.add('drag');});
for(const name of ['dragleave','drop'])$('drop-zone').addEventListener(name,e=>{e.preventDefault();$('drop-zone').classList.remove('drag');});
$('drop-zone').addEventListener('drop',e=>selectFile(e.dataTransfer.files[0]));
$('cancel-upload').addEventListener('click',()=>transfer?.abort());
function uploadBusy(value){$('upload').disabled=value||!selected;$('choose-file').disabled=value;$('logout').disabled=value;$('refresh-list').disabled=value;}
$('upload').addEventListener('click',async()=>{
 if(!selected||transfer)return;
 const file=selected;transfer=new AbortController();const signal=transfer.signal;let id=null;hideMessage();uploadBusy(true);
 $('result').hidden=true;$('upload-progress').hidden=false;$('upload-bar').value=0;$('upload-status').textContent='正在生成加密密钥…';
 try{
  const raw=crypto.getRandomValues(new Uint8Array(32)),key=await importKey(raw),prefix=crypto.getRandomValues(new Uint8Array(8)),readToken=randomHex();
  const created=await api('files',{method:'POST',body:{size:file.size,read_token:readToken},signal});id=created.id;
  const metadata=new TextEncoder().encode(JSON.stringify({v:1,name:file.name,size:file.size,type:file.type,chunk_size:CHUNK}));
  const encrypted=await seal(key,prefix,id,0xffffffff,metadata);
  await api(`files/${id}/manifest`,{method:'PUT',body:{nonce:b64(prefix),manifest:b64(encrypted)},signal,retry:true});
  const chunks=Math.max(1,Math.ceil(file.size/CHUNK));
  for(let n=0;n<chunks;n++){
   const plain=await file.slice(n*CHUNK,Math.min(file.size,(n+1)*CHUNK)).arrayBuffer();
   const ciphertext=await seal(key,prefix,id,n,plain);
   await api(`files/${id}/chunks/${n}`,{method:'PUT',body:ciphertext,signal,retry:true});
   $('upload-bar').value=100*(n+1)/chunks;$('upload-status').textContent=`已加密上传 ${size(Math.min(file.size,(n+1)*CHUNK))} / ${size(file.size)}`;
  }
  const complete=await api(`files/${id}/complete`,{method:'POST',body:{},signal,retry:true});
  const fragment=new URLSearchParams({f:id,t:readToken,k:b64(raw)});
  const entry={id,name:file.name,url:location.origin+'/share#'+fragment.toString(),expires_at:complete.expires_at};
  recent[id]=entry;saveRecent();showResult(entry);$('upload-status').textContent='上传完成';show('文件已准备好，把链接或二维码发给接收方即可。');
 }catch(e){
  if(id){try{await api(`files/${id}`,{method:'DELETE'});}catch{}}
  show(explain(e)+' 未完成的上传会自动过期，也可在下方列表中撤销。',true);
 }finally{transfer=null;uploadBusy(false);$('upload-progress').hidden=true;await refreshList();}
});
function showResult(entry){
 current=entry;$('result').hidden=false;$('result-name').textContent=entry.name;$('result-expiry').textContent='有效至 '+date(entry.expires_at);$('share-link').value=entry.url;
 const qr=window.qrcode(0,'M');qr.addData(entry.url);qr.make();$('qr').innerHTML=qr.createSvgTag({cellSize:4,margin:16,scalable:true});
 $('native-share').hidden=!navigator.share;
}
$('copy-link').addEventListener('click',async()=>{try{await navigator.clipboard.writeText(current.url);show('链接已复制。');}catch{$('share-link').focus();$('share-link').select();show('请长按下载链接并复制。');}});
$('native-share').addEventListener('click',async()=>{try{await navigator.share({title:current.name,url:current.url});}catch(e){if(e.name!=='AbortError')show(explain(e),true);}});
async function revoke(id){
 if(!confirm('撤销后，对方将无法继续下载。已经保存的文件副本不会被删除。确定撤销？'))return;
 try{await api(`files/${id}`,{method:'DELETE'});delete recent[id];saveRecent();if(current?.id===id){current=null;$('result').hidden=true;}show('分享已撤销。');await refreshList();}catch(e){show(explain(e),true);}
}
$('revoke-current').addEventListener('click',()=>{if(current)revoke(current.id);});
$('refresh-list').addEventListener('click',refreshList);
async function refreshList(){
 if(!access)return;
 try{
  const data=await api('files');$('history').replaceChildren();
  const ids=new Set(data.files.map(f=>f.id));for(const id of Object.keys(recent))if(!ids.has(id))delete recent[id];saveRecent();
  if(!data.files.length){const p=document.createElement('p');p.className='muted';p.textContent='还没有有效分享。';$('history').append(p);return;}
  for(const f of data.files.sort((a,b)=>b.created_at.localeCompare(a.created_at))){
   const item=document.createElement('div');item.className='history-item';const title=document.createElement('strong');title.textContent=recent[f.id]?.name||'文件 · '+date(f.created_at);item.append(title);
   const info=document.createElement('p');info.className='small';info.textContent=`${size(f.size)} · ${f.ready?'有效至 '+date(f.expires_at):'上传未完成，创建后 1 小时过期'}`;item.append(info);
   const row=document.createElement('div');row.className='row';
   if(f.ready&&recent[f.id]){const button=document.createElement('button');button.className='text-button';button.textContent='查看链接';button.onclick=()=>{showResult(recent[f.id]);$('result').scrollIntoView({behavior:'smooth'});};row.append(button);}
   const remove=document.createElement('button');remove.className='text-button';remove.textContent='撤销分享';remove.onclick=()=>revoke(f.id);row.append(remove);item.append(row);$('history').append(item);
  }
 }catch(e){show(explain(e),true);}
}
async function setupReceiver(){
 $('sender').hidden=true;$('receiver').hidden=false;
 try{
  const id=params.get('f'),token=params.get('t'),raw=params.get('k');
  if(!/^[a-f0-9]{32}$/.test(id||'')||!/^[a-f0-9]{64}$/.test(token||'')||!raw)throw Error('下载链接不完整，请让发送方重新复制完整链接。');
  const key=await importKey(unb64(raw)),server=await api(`files/${id}`,{token,retry:true});
  const prefix=unb64(server.nonce),bytes=await open(key,prefix,id,0xffffffff,unb64(server.manifest));
  const meta=validateManifest(JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes)),server);
  receiveData={id,token,key,prefix,server,meta};$('receive-name').textContent=meta.name;$('receive-detail').textContent=`${size(meta.size)} · 有效至 ${date(server.expires_at)}`;$('download').disabled=false;
 }catch(e){$('receive-name').textContent='暂时无法接收文件';show(explain(e),true);}
}
$('download').addEventListener('click',async()=>{
 if(!receiveData||receiving)return;receiving=true;$('download').disabled=true;$('download-progress').hidden=false;$('download-bar').value=0;hideMessage();
 try{
  const {id,token,key,prefix,server,meta}=receiveData,parts=[];let total=0;
  for(let n=0;n<server.chunks;n++){
   $('download-status').textContent=`正在下载并解密 ${n+1} / ${server.chunks}`;
   const ciphertext=await api(`files/${id}/chunks/${n}`,{token,binary:true,retry:true});
   const plain=await open(key,prefix,id,n,ciphertext),expected=Math.min(CHUNK,Math.max(0,meta.size-n*CHUNK));
   if(plain.byteLength!==expected)throw Error('文件分块长度校验失败');parts.push(plain);total+=plain.byteLength;$('download-bar').value=100*(n+1)/server.chunks;
  }
  if(total!==meta.size)throw Error('文件大小校验失败');
  if(downloadURL)URL.revokeObjectURL(downloadURL);
  // Never render a downloaded HTML/SVG document in this trusted origin.
  readyFile=new File(parts,meta.name,{type:'application/octet-stream'});downloadURL=URL.createObjectURL(readyFile);
  $('save-file').href=downloadURL;$('save-file').download=meta.name;$('save-actions').hidden=false;$('download').hidden=true;
  $('save-native').hidden=!(navigator.canShare&&navigator.canShare({files:[readyFile]}));$('download-status').textContent='解密校验完成，点击下方保存文件。';
 }catch(e){show(explain(e),true);$('download').disabled=false;}finally{receiving=false;}
});
$('save-native').addEventListener('click',async()=>{try{await navigator.share({files:[readyFile],title:readyFile.name});}catch(e){if(e.name!=='AbortError')show(explain(e),true);}});
window.addEventListener('beforeunload',e=>{if(transfer||receiving){e.preventDefault();e.returnValue='';}});
if(!crypto.subtle){show('此浏览器不支持安全加密，请使用最新版 Safari 或 Chrome，并通过 HTTPS 打开页面。',true);$('login-button').disabled=true;}
else if(params.has('f'))setupReceiver();
else if(access && !params.has('access'))connect();
