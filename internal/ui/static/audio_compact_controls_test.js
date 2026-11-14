const fs = require('fs'), vm = require('vm'), assert = require('assert/strict');
function element(attrs = {}) {
  const listeners = {}, styles = {}, node = {attrs, listeners, textContent:'', title:'', style:{setProperty:(k,v)=>styles[k]=v},
    addEventListener:(k,fn)=>listeners[k]=fn, setAttribute:(k,v)=>attrs[k]=String(v), getAttribute:k=>attrs[k] || null,
    getBoundingClientRect:()=>({left:100,width:100}), focus(){}, setPointerCapture(id){this.pointer=id},
    hasPointerCapture(id){return this.pointer===id}, releasePointerCapture(){delete this.pointer}};
  node.emit=(name,values={})=>{const e={button:0,preventDefault(){this.defaultPrevented=true},stopPropagation(){this.stopped=true},...values};listeners[name]?.(e);return e};
  return node;
}
(async()=>{
  const id='11111111-1111-4111-8111-111111111111', media=element();
  Object.assign(media,{paused:true,currentTime:0,duration:NaN,volume:1,muted:false,error:null,
    pause(){this.paused=true;this.emit('pause')},play(){this.paused=false;this.emit('play');return Promise.resolve()},
    load(){this.duration=NaN;this.currentTime=0;this.emit('emptied')},removeAttribute(k){delete this.attrs[k]}});
  const play=element(),stop=element(),caption=element(),status=element(),time=element(),seek=element(),volume=element({'aria-label':'Volume'});
  const root=element({'data-audio-name':'Player','data-audio-source':id,'data-audio-source-field':'Source','data-audio-title-field':'Caption','data-audio-play-label':'Play','data-audio-pause-label':'Pause','data-audio-loading':'Loading','data-audio-error':'Error'});
  root.isConnected=true;root.closest=()=>null;
  root.querySelector=k=>({'[data-ob-audio-media]':media,'[data-ob-audio-play]':play,'[data-ob-audio-stop]':stop,'[data-ob-audio-caption]':caption,'[data-ob-audio-status]':status,'[data-ob-audio-time]':time,'[data-ob-audio-seek]':seek,'[data-ob-audio-volume]':volume}[k]);
  let requests=0;const window={addEventListener(){}};
  vm.runInNewContext(fs.readFileSync(__dirname+'/audio.js','utf8'),{window,document:{querySelectorAll:()=>[root]},fetch(){requests++;throw Error('unexpected request')}});
  assert.equal(time.textContent,'0:00 / 0:00');assert.equal(seek.attrs['aria-disabled'],'true');
  seek.emit('pointerdown',{pointerId:1,clientX:150});assert.equal(media.currentTime,0);
  await play.emit('click');assert.equal(play.textContent,'⏸');assert.equal(play.attrs['aria-label'],'Pause');
  media.duration=120;media.currentTime=10;media.emit('loadedmetadata');media.emit('timeupdate');
  assert.equal(time.textContent,'0:10 / 2:00');assert.equal(seek.attrs['aria-disabled'],'false');
  let e=seek.emit('keydown',{key:'End'});assert.equal(media.currentTime,120);assert.equal(e.defaultPrevented,true);
  seek.emit('keydown',{key:'Home'});assert.equal(media.currentTime,0);
  seek.emit('keydown',{key:'ArrowRight'});assert.equal(media.currentTime,6);
  seek.emit('pointerdown',{pointerId:2,clientX:150});assert.equal(media.currentTime,60);
  seek.emit('pointermove',{pointerId:2,clientX:175});assert.equal(media.currentTime,90);
  seek.emit('pointerup',{pointerId:2});seek.emit('pointermove',{pointerId:2,clientX:120});assert.equal(media.currentTime,90);
  volume.emit('keydown',{key:'ArrowLeft'});assert.equal(volume.attrs['aria-valuenow'],'95');
  volume.emit('pointerdown',{pointerId:3,clientX:150});assert.equal(media.volume,0.5);assert.equal(volume.title,'Volume: 50%');
  volume.emit('keydown',{key:'Home'});assert.equal(media.volume,0);
  volume.emit('keydown',{key:'End'});assert.equal(media.volume,1);
  media.pause();assert.equal(play.textContent,'▶');assert.equal(play.title,'Play');
  stop.emit('click');assert.equal(media.getAttribute('src'),null);assert.equal(time.textContent,'0:00 / 0:00');
  window.obAudioApply({ok:true,values:{Caption:'Full long caption'}});assert.equal(caption.title,'Full long caption');
  window.obAudioBeforeEvent('Calls','ПриАктивизацииСтроки');assert.equal(status.title,'Loading');
  assert.equal(requests,0);assert.equal(media.preload,'none');
  assert.equal(seek.listeners.input,undefined);assert.equal(volume.listeners.change,undefined);
  console.log('PASS: compact public controls, pause labels, time, keyboard/pointer seek, volume, stop, tooltip, no form input/change or metadata requests');
})().catch(e=>{console.error(e);process.exitCode=1});
