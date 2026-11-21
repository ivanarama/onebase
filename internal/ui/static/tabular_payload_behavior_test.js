'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
function app(){const window={};vm.runInNewContext(fs.readFileSync(path.join(__dirname,'tabular.js'),'utf8'),{window});return window;}
test('tabular question preserves opaque snapshot coordinates through native answer',()=>{
 const w=app();let handlers,posted;
 w.obOpenQuestion=(question,element,h)=>{assert.equal(element,'Sheet');handlers=h;};
 const source={_tabular_token:'own-token',_tabular_row:'2',_tabular_col:'3'};
 assert.equal(w.obOpenTabularQuestion({text:'Создать?'},'Sheet','ОбработкаРасшифровки',source,(...args)=>posted=args),true);
 source._tabular_row='9';handlers.onAnswer('Да',{_question_answer:'Да',_tabular_token:'spoof',_dialog_fields:'{"Note":"own"}'});
 assert.equal(posted[0],'Sheet');assert.equal(posted[1],'Ответ');assert.equal(posted[2]._tabular_token,'own-token');assert.equal(posted[2]._tabular_row,'2');assert.equal(posted[2]._tabular_col,'3');assert.equal(posted[2]._question_answer,'Да');assert.equal(posted[2]._dialog_fields,'{"Note":"own"}');
});
test('ordinary form question keeps its existing path',()=>{
 const w=app();w.obOpenQuestion=()=>{throw Error('unexpected interception');};
 assert.equal(w.obOpenTabularQuestion({},'Button','Нажатие',{},()=>{}),false);
 assert.equal(w.obOpenTabularQuestion({},'Button','Ответ',null,()=>{}),false);
});
