package ui

import "net/http"

// Оболочка вкладок рантайма (issue #129 переключение форм / #130 несколько
// экземпляров), фаза 1.
//
// Идея: вместо навигации «одна форма на страницу» — одно окно с внутренними
// вкладками. Оболочка /ui/app переиспользует head+nav (топбар, подсистемы,
// боковое меню объектов), а на месте <main> держит полосу вкладок и стек
// <iframe>. Каждая открытая форма/список/отчёт — отдельная вкладка; страницы
// внутри фреймов рендерятся как есть, но прячут свой хром (см. window.__obEmbedded
// в партиале head). Несколько экземпляров одного объекта = несколько вкладок
// (изоляция фреймов) — это закрывает #130.
//
// Прямые страницы /ui/... продолжают работать без изменений: вся embedded-логика
// активна только во фрейме оболочки.

// appShell отдаёт страницу-оболочку. Данные навигации (Nav/Subsystems/…)
// заполняет s.render как для обычных страниц, а рабочий стол раздела рендерим
// прямо в оболочке — вкладки остаются для форм/списков, но активная подсистема
// не выглядит пустой.
func (s *Server) appShell(w http.ResponseWriter, r *http.Request) {
	// Скрытая глобальная «Главная» (issue #304): прямой заход на /ui/app?home=1
	// без раздела тоже уводим на первый раздел, а не показываем скрытый стол.
	if target, ok := s.hiddenHomeRedirect(r, "/ui/app"); ok {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if !s.requireSubsystemVisible(w, r) {
		return
	}
	s.render(w, r, "page-app-shell", s.homeDashboardData(r))
}

const tplAppShell = `{{define "page-app-shell"}}
{{template "head" .}}{{template "nav" .}}
{{template "dashboard-style" .}}
<style>
.ob-shell-main{flex:1;display:flex;flex-direction:column;min-width:0;overflow:hidden;padding:0}
.ob-tabstrip{display:flex;gap:2px;background:#e2e8f0;padding:4px 6px 0;overflow-x:auto;flex-shrink:0;min-height:34px}
.ob-tab{display:inline-flex;align-items:center;gap:6px;background:#f1f5f9;border:1px solid #cbd5e1;border-bottom:none;border-radius:6px 6px 0 0;padding:5px 8px 5px 12px;font-size:12px;color:#334155;cursor:pointer;white-space:nowrap;max-width:230px}
.ob-tab.active{background:#fff;color:#1a4a80;font-weight:600}
.ob-tab.dirty .ob-tab-label::after{content:" •";color:#e8b400;font-weight:700}
.ob-tab-label{overflow:hidden;text-overflow:ellipsis}
.ob-tab-dup{color:#94a3b8;font-size:12px;line-height:1;border-radius:3px;padding:0 3px}
.ob-tab-dup:hover{color:#1a4a80;background:#dbeafe}
.ob-tab-close{color:#94a3b8;font-size:14px;line-height:1;border-radius:3px;padding:0 3px}
.ob-tab-close:hover{color:#dc2626;background:#fee2e2}
.ob-tabbody{flex:1;position:relative;background:#f5f5f5;min-height:0}
.ob-tabhome{position:absolute;inset:0;overflow:auto;padding:28px;background:#f5f5f5}
.ob-tabbody iframe{position:absolute;inset:0;width:100%;height:100%;border:0;display:none}
.ob-tabbody iframe.active{display:block}
.ob-tabempty{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;color:#94a3b8;font-size:14px;padding:20px;text-align:center}
.ob-tabmenu{position:fixed;z-index:10000;background:#fff;border:1px solid #cbd5e1;border-radius:6px;box-shadow:0 4px 16px rgba(0,0,0,.18);padding:4px 0;min-width:150px;font-size:12px}
.ob-tabmenu-item{padding:6px 14px;cursor:pointer;color:#334155}
.ob-tabmenu-item:hover{background:#f0f4ff;color:#1a4a80}
@media (max-width:820px){.ob-tabhome{padding:14px}}
</style>
<main class="ob-shell-main" id="ob-shell-main">
  <div class="ob-tabstrip" id="ob-tabstrip" role="tablist"></div>
  <div class="ob-tabbody" id="ob-tabbody">
    <div class="ob-tabhome" id="ob-tabhome">{{template "dashboard-body" .}}</div>
    <div class="ob-tabempty" id="ob-tabempty" style="display:none">{{t $.Lang "Откройте объект из меню слева"}}</div>
  </div>
</main>
{{template "dashboard-scripts" .}}
<script>
(function(){
  var strip=document.getElementById('ob-tabstrip');
  var body=document.getElementById('ob-tabbody');
  var empty=document.getElementById('ob-tabempty');
  var home=document.getElementById('ob-tabhome');
  var tabs=[]; var active=null; var tabSeq=0; var STORE='obTabs'; var STORE_ACTIVE='obTabsActive';
  var SHOW_HOME=false;
  try{ SHOW_HOME = new URLSearchParams(location.search).get('home')==='1'; }catch(e){}

  function tabByID(id){ for(var i=0;i<tabs.length;i++){ if(tabs[i].id===id)return tabs[i]; } return null; }
  function freshTabID(){
    var id='';
    do{
      try{ if(window.crypto && typeof window.crypto.randomUUID==='function') id='tab:'+window.crypto.randomUUID(); }catch(e){ id=''; }
      if(!id){ tabSeq++; id='tab:'+Date.now().toString(36)+':'+tabSeq.toString(36)+':'+Math.random().toString(36).slice(2); }
    }while(tabByID(id));
    return id;
  }
  function uniqueTabID(value){
    var id=(value===undefined||value===null)?'':String(value);
    return id && !tabByID(id) ? id : freshTabID();
  }
  function readSavedActive(){
    var raw='';
    try{ raw=String(sessionStorage.getItem(STORE_ACTIVE)||''); }catch(e){}
    if(!raw)return {id:'',url:''};
    try{
      var saved=JSON.parse(raw);
      if(saved && typeof saved==='object')return {id:saved.id?String(saved.id):'',url:saved.url?String(saved.url):''};
    }catch(e){}
    // До появления стабильных id ключ содержал голый URL.
    return {id:'',url:raw};
  }
  function persist(){ try{ sessionStorage.setItem(STORE, JSON.stringify(tabs.map(function(t){return {id:t.id,url:t.url,title:t.title};}))); }catch(e){} }
  function persistActive(t){ try{ sessionStorage.setItem(STORE_ACTIVE, JSON.stringify({id:t.id,url:t.url})); }catch(e){} }
  function clearPersistedActive(){ try{ sessionStorage.removeItem(STORE_ACTIVE); }catch(e){} }
  function syncEmpty(){ if(empty) empty.style.display = (!home && !tabs.length) ? '' : 'none'; }
  function setActive(t){
    active=t||null;
    tabs.forEach(function(x){ x.btn.classList.toggle('active',x===t); x.frame.classList.toggle('active',x===t); });
    if(home) home.style.display = t ? 'none' : '';
    if(active && active.btn.scrollIntoView) active.btn.scrollIntoView({inline:'nearest',block:'nearest'}); // фаза 4: активная вкладка в видимую область
    // id отличает несколько экземпляров одного URL. setActive(null) при
    // ?home=1 намеренно не стирает выбор: возврат открывает прежнюю вкладку.
    if(active)persistActive(active);
    syncEmpty();
  }
  function closeTab(t){
    var i=tabs.indexOf(t); if(i<0)return;
    if(t.dirty && !window.confirm('В этой вкладке есть несохранённые изменения. Закрыть вкладку?'))return; // фаза 3
    t.btn.remove(); t.frame.remove(); tabs.splice(i,1);
    if(active===t){
      var next=tabs[Math.min(i,tabs.length-1)]||null;
      setActive(next);
      if(!next)clearPersistedActive();
    }
    persist();
  }
  // Фаза 4: управление множеством вкладок — контекст-меню по правому клику.
  function closeOthers(keep){ tabs.slice().forEach(function(t){ if(t!==keep) closeTab(t); }); }
  function tabMenu(t,x,y){
    var old=document.getElementById('ob-tabmenu'); if(old)old.remove();
    var m=document.createElement('div'); m.id='ob-tabmenu'; m.className='ob-tabmenu'; m.style.left=x+'px'; m.style.top=y+'px';
    [['Закрыть',function(){closeTab(t);}],['Закрыть другие',function(){closeOthers(t);}],['Закрыть все',function(){tabs.slice().forEach(closeTab);}]].forEach(function(it){
      var b=document.createElement('div'); b.className='ob-tabmenu-item'; b.textContent=it[0];
      b.addEventListener('click',function(){ m.remove(); it[1](); });
      m.appendChild(b);
    });
    document.body.appendChild(m);
    setTimeout(function(){ document.addEventListener('click',function rm(){ m.remove(); document.removeEventListener('click',rm); }); },0);
  }
  function openTab(url,title,opts){
    opts=opts||{};
    if(!opts.allowDup){ for(var i=0;i<tabs.length;i++){ if(tabs[i].url===url){ if(!opts.restoring)setActive(tabs[i]); return tabs[i]; } } }
    var btn=document.createElement('div'); btn.className='ob-tab'; btn.setAttribute('role','tab'); btn.title=title||'Форма'; // фаза 4: тултип полного заголовка
    var lab=document.createElement('span'); lab.className='ob-tab-label'; lab.textContent=title||'Форма'; btn.appendChild(lab);
    var dup=document.createElement('span'); dup.className='ob-tab-dup'; dup.textContent='⧉'; dup.title='Открыть ещё один экземпляр'; btn.appendChild(dup);
    var cl=document.createElement('span'); cl.className='ob-tab-close'; cl.textContent='✕'; cl.title='Закрыть'; btn.appendChild(cl);
    var frame=document.createElement('iframe'); frame.src=url;
    var t={id:uniqueTabID(opts.id),url:url,title:title,btn:btn,frame:frame,label:lab};
    btn.addEventListener('click',function(e){ if(e.target===cl||e.target===dup)return; setActive(t); });
    btn.addEventListener('mousedown',function(e){ if(e.button===1){ e.preventDefault(); closeTab(t); } });
    cl.addEventListener('click',function(e){ e.stopPropagation(); closeTab(t); });
    dup.addEventListener('click',function(e){ e.stopPropagation(); openTab(t.url, t.title, {allowDup:true}); }); // #130
    btn.addEventListener('contextmenu',function(e){ e.preventDefault(); setActive(t); tabMenu(t,e.clientX,e.clientY); }); // фаза 4
    strip.appendChild(btn); body.appendChild(frame); tabs.push(t);
    if(!opts.restoring){ setActive(t); persist(); }
    return t;
  }
  window.obOpenTab=openTab;

  function tabByWindow(win){ for(var i=0;i<tabs.length;i++){ if(tabs[i].frame.contentWindow===win)return tabs[i]; } return null; }
  window.addEventListener('message',function(ev){
    // Принимаем сообщения только от своего origin: иначе любой сторонний фрейм
    // мог бы навязать src произвольной вкладке. d.url дополнительно валидируем
    // той же openable()-проверкой, что и клик по ссылке (только /ui/, без
    // admin/login/...), чтобы postMessage не открывал внешние схемы.
    if(ev.origin!==location.origin)return;
    var d=ev.data; if(!d||typeof d!=='object')return;
    if(d.source==='obOpenTab' && d.url){ var ou=String(d.url); if(!openable(ou))return; openTab(ou, d.title?String(d.title):'Форма', {allowDup:!!d.allowDup}); }
    else if(d.source==='obCloseTab'){ var ct=tabByWindow(ev.source); if(ct)closeTab(ct); }
    else if(d.source==='obSetTitle' && active && d.title){ active.title=String(d.title); active.label.textContent=active.title; active.btn.title=active.title; persist(); }
    else if(d.source==='obDirty'){ var dt=tabByWindow(ev.source); if(dt){ dt.dirty=!!d.dirty; dt.btn.classList.toggle('dirty',dt.dirty); } } // фаза 3
  });

  function openable(href){
    if(!/^\/ui\//.test(href))return false;
    if(/^\/ui\/(admin|about|logout|login|logo|debug|app|_(?!ref-open|ref-create))/.test(href))return false;
    return true;
  }
  function shellHomeURL(href){
    try{
      var u=new URL(href, location.origin);
      if(u.origin!==location.origin)return '';
      if(u.pathname!=='/ui'&&u.pathname!=='/ui/')return '';
      var t=new URL('/ui/app', location.origin);
      t.searchParams.set('home','1');
      var sub=u.searchParams.get('subsystem');
      if(sub)t.searchParams.set('subsystem',sub);
      return t.pathname+t.search;
    }catch(e){return '';}
  }
  document.addEventListener('click',function(e){
    if(e.defaultPrevented||e.button!==0||e.metaKey||e.ctrlKey||e.shiftKey||e.altKey)return;
    var a=e.target.closest?e.target.closest('#ob-nav a[href], .subsys-bar a[href], #ob-tabhome a[href]'):null;
    if(!a)return;
    var href=a.getAttribute('href')||'';
    var shellHome=shellHomeURL(href);
    if(shellHome){ e.preventDefault(); location.href=shellHome; return; }
    if(!openable(href))return;
    e.preventDefault();
    openTab(href,(a.getAttribute('title')||a.textContent||'').replace(/\s+/g,' ').trim()||'Форма');
  });

  // Снимок читаем ДО openTab: восстановление само создаёт вкладки и не должно
  // успеть заменить сохранённый выбор последней добавленной вкладкой.
  var savedActive=readSavedActive();
  try{
    var saved=JSON.parse(sessionStorage.getItem(STORE)||'[]');
    // restoring не активирует/не сохраняет промежуточные вкладки; allowDup
    // сохраняет отдельные экземпляры одного URL (#130).
    saved.forEach(function(s){ if(s&&s.url) openTab(String(s.url), s.title?String(s.title):'Форма', {allowDup:true,id:s.id,restoring:true}); });
    var restore=null;
    if(savedActive.id)restore=tabByID(savedActive.id);
    // Backward compatibility: старые версии хранили в obTabsActive только URL.
    if(!restore && !savedActive.id && savedActive.url){ for(var i=0;i<tabs.length;i++){ if(tabs[i].url===savedActive.url){ restore=tabs[i]; break; } } }
    if(tabs.length && !SHOW_HOME) setActive(restore||tabs[0]); else setActive(null);
    persist(); // добавить id в legacy obTabs и убрать невалидные записи
  }catch(e){}
  syncEmpty();

  // Фаза 3: предупредить о потере несохранённых правок при закрытии/перезагрузке окна.
  window.addEventListener('beforeunload',function(e){
    for(var i=0;i<tabs.length;i++){ if(tabs[i].dirty){ e.preventDefault(); e.returnValue=''; return ''; } }
  });
})();
</script>
</div></body></html>
{{end}}`
