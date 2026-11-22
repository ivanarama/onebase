const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('fs'),vm=require('vm');
const source=fs.readFileSync('static/ui.js','utf8');
const code=source.slice(source.indexOf('function openRefCurrent('),source.indexOf('function openRefCreate('));
function env(page,embedded=true){
    const calls=[],sel={value:'chat-uuid',getAttribute:k=>({'data-ref-entity':'Диалоги','data-ref-page':page})[k]};
    const window={__obEmbedded:embedded,parent:{obOpenTab(){},postMessage:msg=>calls.push(msg.url)},open:url=>calls.push(url)};
    const ctx={window,document:{getElementById:()=>sel}};vm.createContext(ctx);vm.runInContext(code,ctx);
    return {calls,sel,open:()=>ctx.openRefCurrent(sel)};
}
test('custom page opens inside shell with current UUID and encoded page name',()=>{
    const e=env('Переписка');e.open();
    assert.equal(e.calls[0],'/ui/page/'+encodeURIComponent('Переписка')+'?ref=chat-uuid');
    e.sel.value='another&ref=wrong';e.open();
    assert.equal(new URL(e.calls[1],'http://local').searchParams.get('ref'),'another&ref=wrong');
});
test('normal cards, empty references and standalone navigation keep their contract',()=>{
    const e=env('');e.open();assert(e.calls[0].startsWith('/ui/_ref-open/'));
    e.sel.value='';e.open();assert.equal(e.calls.length,1);
    const standalone=env('Переписка',false);standalone.open();assert(standalone.calls[0].startsWith('/ui/page/'));
});
test('page value cannot supply an external URL or inject query parameters',()=>{
    const e=env('https://evil.example/?x=1');e.open();
    const u=new URL(e.calls[0],'http://local');assert.equal(u.origin,'http://local');assert.deepEqual([...u.searchParams.keys()],['ref']);
});
