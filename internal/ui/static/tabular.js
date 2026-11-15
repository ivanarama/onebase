(function(){
 'use strict';
 if(window.obApplyTabularDocuments)return;
 window.obOpenTabularQuestion=function(question,elementName,eventName,extra,fire){
  if(!extra||typeof extra._tabular_token!=='string'||(eventName!=='ОбработкаРасшифровки'&&eventName!=='Ответ')||typeof window.obOpenQuestion!=='function')return false;
  var captured=Object.assign({},extra);
  window.obOpenQuestion(question,elementName,{onAnswer:function(variant,fields){
   var next=Object.assign({},captured,fields||{_question_answer:variant});
   ['_tabular_token','_tabular_row','_tabular_col'].forEach(function(key){next[key]=captured[key];});
   return fire(elementName,'Ответ',next);
  }});
  return true;
 };
 window.obApplyTabularDocuments=function(results){
  if(!results)return;
  document.querySelectorAll('iframe[data-ob-tabular-field]').forEach(function(frame){
   var key=frame.getAttribute('data-ob-tabular-field').toLowerCase();
   if(!Object.prototype.hasOwnProperty.call(results,key))return;
   var path=results[key];
   if(!path){frame.removeAttribute('src');frame.hidden=true;return;}
   if(!/^\/ui\/tabular\/[0-9a-f-]{36}$/.test(path))return;
   frame.src=path;frame.hidden=false;
  });
 };
 window.obBindTabularHost=function(fire){
  window.addEventListener('message',function(event){
   if(event.origin!==location.origin||!event.data||event.data.type!=='ob-tabular-cell'||typeof event.data.url!=='string')return;
   document.querySelectorAll('iframe[data-ob-tabular-field]').forEach(function(frame){
    if(event.source!==frame.contentWindow)return;
    var url,current;try{url=new URL(event.data.url);current=new URL(frame.src);}catch(_){return;}
    if(url.origin!==location.origin||url.pathname!==current.pathname+'/cell'||url.hash||!/^\/ui\/tabular\/[0-9a-f-]{36}$/.test(current.pathname))return;
    var row=url.searchParams.get('row'),col=url.searchParams.get('col');if(!/^\d+$/.test(row||'')||!/^\d+$/.test(col||''))return;
    var host=frame.closest('[data-ob-el]');if(!host)return;
    fire(host.getAttribute('data-ob-el'),'ОбработкаРасшифровки',{_tabular_token:current.pathname.split('/').pop(),_tabular_row:row,_tabular_col:col});
   });
  });
 };
})();
