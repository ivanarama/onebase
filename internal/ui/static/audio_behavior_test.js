const fs = require('fs'), vm = require('vm'), assert = require('assert/strict');
const code = fs.readFileSync(__dirname + '/audio.js', 'utf8');
function deferred() { let resolve; const promise = new Promise(r => resolve = r); return {promise,resolve}; }
function fixture(attrs = {}) {
 const listeners = {}, mediaAttrs = {}, requests = [], prepared = [];
 const media = {preload:'', paused:true, currentTime:0, plays:0, loads:0,
  addEventListener(){}, getAttribute(k){return mediaAttrs[k] || null},
  setAttribute(k,v){mediaAttrs[k]=v}, removeAttribute(k){delete mediaAttrs[k]},
  load(){this.loads++}, pause(){this.paused=true}, play(){this.plays++;this.paused=false;return Promise.resolve()}};
 const play = {disabled:false,addEventListener(n,f){listeners[n]=f}};
 let stopClick;
 const stop = {addEventListener(n,f){stopClick=f}};
 const caption={textContent:'A'}, status={textContent:''};
 const attributes=Object.assign({'data-audio-name':'Player','data-audio-source-field':'Source','data-audio-title-field':'Caption','data-audio-play-event':'Prepare','data-audio-empty':'empty','data-audio-error':'error','data-audio-loading':'loading'},attrs);
 const root={style:{display:''},isConnected:true,getAttribute(k){return attributes[k]||''},closest(){return null},querySelector(k){return {'[data-ob-audio-media]':media,'[data-ob-audio-play]':play,'[data-ob-audio-stop]':stop,'[data-ob-audio-caption]':caption,'[data-ob-audio-status]':status}[k]}};
 const window={addEventListener(){},obFire(name,event){const d=deferred(),extra=window.obAudioBeforeEvent(name,event);prepared.push({name,event,extra,d});return d.promise}};
 const context={window,document:{querySelectorAll(){return [root]}},fetch(url){const d=deferred();requests.push({url,d});return d.promise}};
 vm.runInNewContext(code,context);
 function respond(item,data){window.obAudioFilterResponse(data,{extraParams:item.extra,eventName:item.event,elementName:item.name});window.obAudioApply(data);item.d.resolve();}
 return {window,media,play,caption,status,requests,prepared,click:()=>listeners.click(),stop:()=>stopClick(),respond};
}
(async()=>{
 const idA='11111111-1111-4111-8111-111111111111', idB='22222222-2222-4222-8222-222222222222';
 const f=fixture();
 assert.equal(f.requests.length,0);assert.equal(f.media.plays,0);assert.equal(f.media.getAttribute('src'),null);
 const a=f.click();assert.equal(f.prepared.length,1);
 const selection=f.window.obAudioBeforeEvent('Calls','ПриАктивизацииСтроки');
 assert.equal(f.media.getAttribute('src'),null);assert.equal(f.media.paused,true);
 const late={ok:true,values:{Source:idA,Caption:'A'}};
 f.respond(f.prepared[0],late);await a;
 assert.equal(late.values.Source,undefined);assert.equal(late.values.Caption,undefined);assert.equal(f.media.plays,0);
 f.window.obAudioFilterResponse({ok:true},{eventName:'ПриАктивизацииСтроки',extraParams:selection});
 f.window.obAudioApply({ok:true,values:{Source:'',Caption:'B'}});
 assert.equal(f.caption.textContent,'B');
 const b=f.click();f.respond(f.prepared[1],{ok:true,values:{Source:idB,Caption:'B'}});await b;
 assert.equal(f.media.getAttribute('src'),'/ui/attachments/'+idB+'/download');assert.equal(f.media.plays,1);
 await f.click();assert.equal(f.media.paused,true);await f.click();assert.equal(f.media.plays,2);assert.equal(f.prepared.length,2);
 f.stop();assert.equal(f.media.getAttribute('src'),null);assert.equal(f.media.currentTime,0);await f.click();assert.equal(f.prepared.length,2);assert.equal(f.media.plays,3);
 f.window.obAudioApply({ok:true,audioStops:['Player']});assert.equal(f.media.getAttribute('src'),null);
 f.play.disabled=true;await f.click();assert.equal(f.prepared.length,2);
 const stopped=fixture();const pending=stopped.click();stopped.stop();stopped.respond(stopped.prepared[0],{ok:true,values:{Source:idA}});await pending;assert.equal(stopped.media.plays,0);await stopped.click();assert.equal(stopped.media.plays,1);
 const changed=fixture();const oldPlay=changed.click();changed.window.obAudioApply({ok:true,values:{Source:idB}});changed.respond(changed.prepared[0],{ok:true,values:{Source:idA}});await oldPlay;assert.equal(changed.media.plays,0);
 const skipped=fixture();const row=skipped.window.obFire('Table','ПриАктивизацииСтроки');skipped.prepared[0].d.resolve();await row;const retry=skipped.click();assert.equal(skipped.prepared.length,2);skipped.respond(skipped.prepared[1],{ok:true,values:{Source:idB}});await retry;assert.equal(skipped.media.plays,1);
 const invalid=fixture({'data-audio-source':'https://bad.invalid/?token=x'});const i=invalid.click();assert.equal(invalid.prepared.length,1);invalid.respond(invalid.prepared[0],{ok:false,error:'failed',values:{Source:idA}});await i;assert.equal(invalid.media.plays,0);
 const auto=fixture({'data-audio-play-event':'','data-audio-metadata-url':'/ui/catalog/Recordings/'+idA+'/attachments'});
 assert.equal(auto.requests.length,0);const load=auto.click();assert.equal(auto.requests.length,1);assert.equal(auto.media.plays,0);
 auto.requests[0].d.resolve({ok:true,json:()=>Promise.resolve([{id:idB,mime_type:'audio/wav',filename:'b.wav'}])});await load;
 assert.equal(auto.media.getAttribute('src'),'/ui/attachments/'+idB+'/download');assert.equal(auto.media.plays,1);
 auto.stop();await auto.click();assert.equal(auto.requests.length,1);
 console.log('PASS: lazy init, late Play A/selection B, caption, source UUID, pause/resume/no duplicate audit, stop/retry, disabled/error, lazy auto metadata');
})().catch(e=>{console.error(e);process.exitCode=1});
