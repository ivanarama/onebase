package ui

const tplQueryBuilder = `
{{define "page-query-builder"}}
{{template "head" .}}{{template "nav" .}}
<main style="max-width:100%">
<h2>{{t $.Lang "Конструктор запросов"}}</h2>
<div style="display:grid;grid-template-columns:400px 1fr;gap:20px;align-items:start">

<!-- LEFT: builder panels -->
<div>

<!-- Source -->
<div class="card" style="margin-bottom:12px">
<h3 style="margin-top:0">{{t $.Lang "Источник данных"}}</h3>
<select id="qb-src" data-ob-qb-source style="width:100%;margin-bottom:8px">
  <option value="">{{t $.Lang "— выбрать —"}}</option>
</select>
<div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">
  <span style="font-size:12px;color:#64748b;flex-shrink:0;width:70px">{{t $.Lang "Псевдоним:"}}</span>
  <input id="qb-main-alias" type="text" placeholder="{{t $.Lang "напр. Прод"}}" data-ob-qb-main-alias
    style="width:110px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 6px">
  <span style="font-size:11px;color:#94a3b8">({{t $.Lang "обязателен при JOIN"}})</span>
</div>
<div id="qb-vt-param" style="display:none;margin-top:4px">
  <label style="font-size:12px;color:#64748b">{{t $.Lang "Параметры виртуальной таблицы"}}</label>
  <input id="qb-vt-param-val" type="text" data-ob-qb-vt-param style="width:100%;margin-top:4px" placeholder="{{t $.Lang "например: &НаДату"}}">
</div>
</div>

<!-- Joins -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Соединения (JOIN)"}}</h3>
  <button class="btn btn-sm" data-ob-qb-action="add-join"
    style="background:#dbeafe;color:#1d4ed8;padding:2px 8px;font-size:12px">{{t $.Lang "+ Соединение"}}</button>
</div>
<div id="qb-joins">
  <p style="font-size:12px;color:#94a3b8;margin:0" id="qb-joins-hint">{{t $.Lang "Нет соединений"}}</p>
</div>
</div>

<!-- Fields -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Поля (ВЫБРАТЬ)"}}</h3>
  <div style="display:flex;gap:4px">
    <button class="btn btn-sm" data-ob-qb-action="all-fields" data-ob-qb-all-fields="true" style="background:#e2e8f0;color:#475569;padding:2px 8px;font-size:12px">{{t $.Lang "Все"}}</button>
    <button class="btn btn-sm" data-ob-qb-action="all-fields" data-ob-qb-all-fields="false" style="background:#e2e8f0;color:#475569;padding:2px 8px;font-size:12px">{{t $.Lang "Сбросить"}}</button>
  </div>
</div>
<div id="qb-fields-list" style="max-height:260px;overflow-y:auto"></div>
</div>

<!-- Where -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Условия (ГДЕ)"}}</h3>
  <button class="btn btn-sm" data-ob-qb-action="add-cond"
    style="background:#dbeafe;color:#1d4ed8;padding:2px 8px;font-size:12px">{{t $.Lang "+ Условие"}}</button>
</div>
<div id="qb-conds"></div>
</div>

<!-- Order -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Сортировка"}}</h3>
  <button class="btn btn-sm" data-ob-qb-action="add-order"
    style="background:#dbeafe;color:#1d4ed8;padding:2px 8px;font-size:12px">{{t $.Lang "+ Поле"}}</button>
</div>
<div id="qb-orders"></div>
</div>

<!-- Params -->
<div class="card">
<h3 style="margin-top:0">{{t $.Lang "Параметры"}}</h3>
<p style="font-size:12px;color:#64748b;margin-bottom:8px">{{t $.Lang "Автообнаружение из условий по &ИмяПараметра"}}</p>
<div id="qb-params" style="font-size:13px">—</div>
</div>
</div><!-- /LEFT -->

<!-- RIGHT: generated text -->
<div>
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Текст запроса"}}</h3>
  <button data-ob-qb-action="copy-query"
    style="background:#dcfce7;color:#166534;border:none;border-radius:5px;padding:3px 12px;cursor:pointer;font-size:12px">{{t $.Lang "Копировать"}}</button>
</div>
<textarea id="qb-query-out" rows="16" readonly
  style="width:100%;font-family:monospace;font-size:13px;border:1px solid #e2e8f0;border-radius:6px;padding:10px;background:#f8fafc;resize:vertical"></textarea>
</div>

<div class="card">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "DSL-фрагмент (вставить в модуль)"}}</h3>
  <button data-ob-qb-action="copy-dsl"
    style="background:#dcfce7;color:#166534;border:none;border-radius:5px;padding:3px 12px;cursor:pointer;font-size:12px">{{t $.Lang "Копировать"}}</button>
</div>
<textarea id="qb-dsl-out" rows="14" readonly
  style="width:100%;font-family:monospace;font-size:13px;border:1px solid #e2e8f0;border-radius:6px;padding:10px;background:#f8fafc;resize:vertical"></textarea>
</div>
</div><!-- /RIGHT -->

</div><!-- /grid -->
</main>

<script>
var _schema = {{.Schema}};
var _srcMap = {};
_schema.forEach(function(s){ _srcMap[s.id] = s; });

// Populate source select
(function(){
  var sel = document.getElementById('qb-src');
  var groups = {};
  _schema.forEach(function(s){
    if(!groups[s.group]) groups[s.group]=[];
    groups[s.group].push(s);
  });
  Object.keys(groups).forEach(function(g){
    var og = document.createElement('optgroup'); og.label = g;
    groups[g].forEach(function(s){
      var o = document.createElement('option'); o.value=s.id; o.textContent=s.label;
      og.appendChild(o);
    });
    sel.appendChild(og);
  });
})();

// ─── State ────────────────────────────────────────────────────────────────────
var _curFields = [];
var _selFields = {};
var _joins = []; // [{id, typeSel, srcSel, aliasInp, onInp, el}]

// ─── Source change ────────────────────────────────────────────────────────────
function qbSetSource(id){
  var src = _srcMap[id];
  _selFields = {};
  document.getElementById('qb-conds').innerHTML = '';
  document.getElementById('qb-orders').innerHTML = '';
  // Clear joins when main source changes
  _joins = [];
  document.getElementById('qb-joins').innerHTML =
    '<p style="font-size:12px;color:#94a3b8;margin:0" id="qb-joins-hint">Нет соединений</p>';
  document.getElementById('qb-main-alias').value = '';

  var vtDiv = document.getElementById('qb-vt-param');
  if(src && src.vtParam){
    vtDiv.style.display = '';
    document.getElementById('qb-vt-param-val').value = src.vtParam;
  } else {
    vtDiv.style.display = 'none';
  }
  qbRebuildAllFields();
}

// ─── Rebuild combined field list from main source + joined sources ─────────────
function qbRebuildAllFields(){
  var srcId = document.getElementById('qb-src').value;
  var mainSrc = _srcMap[srcId];
  var mainAlias = document.getElementById('qb-main-alias').value.trim();
  var hasJoins = _joins.some(function(j){ return !!j.srcSel.value; });
  var usePrefix = !!(mainAlias);

  var all = [];
  if(mainSrc){
    mainSrc.fields.forEach(function(f){
      var name = usePrefix ? mainAlias+'.'+f.name : f.name;
      all.push({name: name, label: name, type: f.type});
    });
    // Always include Ссылка as virtual field for joins
    if(usePrefix){
      all.push({name: mainAlias+'.Ссылка', label: mainAlias+'.Ссылка (id)', type: 'ref'});
    }
  }
  _joins.forEach(function(j){
    var src = _srcMap[j.srcSel.value];
    var alias = j.aliasInp.value.trim();
    if(!src || !alias) return;
    src.fields.forEach(function(f){
      all.push({name: alias+'.'+f.name, label: alias+'.'+f.name, type: f.type});
    });
    all.push({name: alias+'.Ссылка', label: alias+'.Ссылка (id)', type: 'ref'});
  });

  // Preserve existing selections where name still exists
  var nameSet = {};
  all.forEach(function(f){ nameSet[f.name]=true; });
  var newSel = {};
  Object.keys(_selFields).forEach(function(k){
    if(nameSet[k]) newSel[k] = _selFields[k];
  });
  // If nothing preserved — select all non-Ссылка fields from main source
  if(!Object.keys(newSel).length && mainSrc){
    all.forEach(function(f){
      if(!f.name.endsWith('.Ссылка') && f.name !== 'Ссылка')
        newSel[f.name] = {alias:'', agg:''};
    });
  }
  _selFields = newSel;
  _curFields = all;
  renderFields();
  qbGenerate();
}

// ─── Add JOIN ─────────────────────────────────────────────────────────────────
function qbAddJoin(){
  // Auto-set main alias if not set
  var mainAliasInp = document.getElementById('qb-main-alias');
  if(!mainAliasInp.value.trim()){
    var mainSrc = _srcMap[document.getElementById('qb-src').value];
    if(mainSrc){
      var lbl = mainSrc.label;
      var parts = lbl.split('.');
      // Справочник.Клиент → Клиент, РегистрНакопления.Остатки.Балансы → Остатки
      var ent = parts.length >= 2 ? parts[1].replace(/\(.*$/, '') : parts[0];
      mainAliasInp.value = ent;
    }
  }

  // Remove hint
  var hint = document.getElementById('qb-joins-hint');
  if(hint) hint.remove();

  var jid = Date.now();
  var div = document.createElement('div');
  div.dataset.jid = jid;
  div.style.cssText = 'border:1px solid #e2e8f0;border-radius:6px;padding:8px;margin-bottom:8px;background:#f8fafc';

  // Row 1: type + source + alias + delete
  var row1 = document.createElement('div');
  row1.style.cssText = 'display:flex;gap:6px;align-items:center;margin-bottom:6px;flex-wrap:wrap';

  var typeSel = document.createElement('select');
  typeSel.style.cssText = 'width:126px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';
  [['ЛЕВОЕ','⬅ ЛЕВОЕ'],['ВНУТРЕННЕЕ','✕ ВНУТРЕННЕЕ'],['ПРАВОЕ','➡ ПРАВОЕ'],['ПОЛНОЕ','⟺ ПОЛНОЕ']].forEach(function(x){
    var o = document.createElement('option'); o.value=x[0]; o.textContent=x[1];
    typeSel.appendChild(o);
  });

  var srcSel = document.createElement('select');
  srcSel.style.cssText = 'flex:1;min-width:140px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';
  var o0 = document.createElement('option'); o0.value=''; o0.textContent='— источник —';
  srcSel.appendChild(o0);
  var jgroups = {};
  _schema.forEach(function(s){ if(!jgroups[s.group]) jgroups[s.group]=[]; jgroups[s.group].push(s); });
  Object.keys(jgroups).forEach(function(g){
    var og = document.createElement('optgroup'); og.label=g;
    jgroups[g].forEach(function(s){
      var o = document.createElement('option'); o.value=s.id; o.textContent=s.label;
      og.appendChild(o);
    });
    srcSel.appendChild(og);
  });

  var aliasInp = document.createElement('input');
  aliasInp.type='text'; aliasInp.placeholder='Псевдоним';
  aliasInp.style.cssText = 'width:86px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';

  var del = document.createElement('button');
  del.type='button'; del.textContent='×';
  del.style.cssText = 'background:none;border:none;color:#ef4444;cursor:pointer;font-size:18px;line-height:1;flex-shrink:0;padding:0 2px';
  del.addEventListener('click', function(){
    div.remove();
    _joins = _joins.filter(function(j){ return j.id !== jid; });
    if(!_joins.length){
      document.getElementById('qb-joins').innerHTML =
        '<p style="font-size:12px;color:#94a3b8;margin:0" id="qb-joins-hint">Нет соединений</p>';
    }
    qbRebuildAllFields();
  });

  row1.appendChild(typeSel); row1.appendChild(srcSel);
  row1.appendChild(aliasInp); row1.appendChild(del);

  // Row 2: ON condition
  var row2 = document.createElement('div');
  row2.style.cssText = 'display:flex;gap:6px;align-items:center';
  var onLabel = document.createElement('span');
  onLabel.textContent = 'ПО:';
  onLabel.style.cssText = 'font-size:12px;font-weight:600;color:#475569;flex-shrink:0;width:26px';
  var onInp = document.createElement('input');
  onInp.type='text';
  onInp.placeholder='Псевд1.Поле = Псевд2.Ссылка';
  onInp.style.cssText = 'flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 6px';
  row2.appendChild(onLabel); row2.appendChild(onInp);

  div.appendChild(row1); div.appendChild(row2);
  document.getElementById('qb-joins').appendChild(div);

  var jdata = {id: jid, el: div, typeSel: typeSel, srcSel: srcSel, aliasInp: aliasInp, onInp: onInp};
  _joins.push(jdata);

  // Auto-fill alias from source name
  srcSel.addEventListener('change', function(){
    var src = _srcMap[srcSel.value];
    if(src && !aliasInp.value.trim()){
      var parts = src.label.split('.');
      aliasInp.value = parts.length >= 2 ? parts[1].replace(/\(.*$/,'') : parts[0];
    }
    qbRebuildAllFields();
  });
  typeSel.addEventListener('change', function(){ qbGenerate(); });
  aliasInp.addEventListener('input', function(){ qbRebuildAllFields(); });
  onInp.addEventListener('input', function(){ qbGenerate(); });

  // Trigger alias rebuild since main alias was possibly set
  qbRebuildAllFields();
}

// ─── Render fields list ───────────────────────────────────────────────────────
function renderFields(){
  var div = document.getElementById('qb-fields-list');
  div.innerHTML = '';
  if(!_curFields.length){
    div.innerHTML = '<p style="font-size:12px;color:#94a3b8;margin:4px 0">Сначала выберите источник</p>';
    return;
  }

  // Group by source prefix for visual separation
  var lastPrefix = null;
  _curFields.forEach(function(f){
    var dotIdx = f.name.indexOf('.');
    var prefix = dotIdx >= 0 ? f.name.substring(0, dotIdx) : '';
    if(prefix && prefix !== lastPrefix){
      lastPrefix = prefix;
      var sep = document.createElement('div');
      sep.style.cssText = 'font-size:11px;font-weight:600;color:#64748b;margin:6px 0 2px;padding-left:2px;border-top:1px solid #f1f5f9;padding-top:4px';
      sep.textContent = prefix + ' (' + (prefix === document.getElementById('qb-main-alias').value.trim() ? 'основная таблица' : 'присоединённая') + ')';
      div.appendChild(sep);
    }

    var row = document.createElement('div');
    row.style.cssText = 'display:flex;align-items:center;gap:6px;margin-bottom:3px;font-size:13px;padding:1px 0';

    var chk = document.createElement('input');
    chk.type='checkbox'; chk.checked=!!_selFields[f.name]; chk.dataset.field=f.name;
    chk.addEventListener('change', function(){
      if(chk.checked) _selFields[f.name]={alias:'',agg:''};
      else delete _selFields[f.name];
      qbGenerate();
    });

    var lbl = document.createElement('label');
    // Show only field part (after dot) as label, full name as title
    var displayName = dotIdx >= 0 ? f.name.substring(dotIdx+1) : f.name;
    lbl.textContent = displayName;
    lbl.title = f.name;
    lbl.style.cssText = 'flex:1;cursor:pointer;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
    lbl.addEventListener('click', function(){ chk.click(); });

    var aggSel = document.createElement('select');
    aggSel.style.cssText = 'font-size:11px;padding:1px 3px;border:1px solid #e2e8f0;border-radius:4px;width:90px';
    ['','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ'].forEach(function(a){
      var o=document.createElement('option'); o.value=a; o.textContent=a||'— нет —';
      aggSel.appendChild(o);
    });
    if(_selFields[f.name]) aggSel.value = _selFields[f.name].agg || '';
    aggSel.addEventListener('change', function(){
      if(_selFields[f.name]) _selFields[f.name].agg = aggSel.value;
      qbGenerate();
    });

    var aliasInp = document.createElement('input');
    aliasInp.type='text'; aliasInp.placeholder='КАК';
    aliasInp.style.cssText = 'font-size:11px;width:70px;padding:1px 4px;border:1px solid #e2e8f0;border-radius:4px';
    if(_selFields[f.name]) aliasInp.value = _selFields[f.name].alias || '';
    aliasInp.addEventListener('input', function(){
      if(_selFields[f.name]) _selFields[f.name].alias = aliasInp.value.trim();
      qbGenerate();
    });

    row.appendChild(chk); row.appendChild(lbl); row.appendChild(aggSel); row.appendChild(aliasInp);
    div.appendChild(row);
  });
}

function qbAllFields(v){
  _curFields.forEach(function(f){
    if(f.name.endsWith('.Ссылка') || f.name === 'Ссылка') return; // skip virtual Ссылка by default
    if(v) _selFields[f.name]={alias:'',agg:''};
    else delete _selFields[f.name];
  });
  renderFields(); qbGenerate();
}

// ─── WHERE conditions ─────────────────────────────────────────────────────────
function qbAddCond(){
  var div = document.createElement('div');
  div.style.cssText = 'display:flex;gap:4px;margin-bottom:6px;align-items:center;flex-wrap:wrap';

  var fsel = document.createElement('select');
  fsel.style.cssText = 'flex:1;min-width:100px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  _curFields.forEach(function(f){
    var o=document.createElement('option'); o.value=f.name; o.textContent=f.name; fsel.appendChild(o);
  });

  var opSel = document.createElement('select');
  opSel.style.cssText = 'width:100px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  ['=','<>','>','<','>=','<=','ЕСТЬ ПУСТО','НЕ ЕСТЬ ПУСТО','ПОДОБНО','В'].forEach(function(op){
    var o=document.createElement('option'); o.value=op; o.textContent=op; opSel.appendChild(o);
  });

  var valInp = document.createElement('input');
  valInp.type='text'; valInp.placeholder='&Параметр или значение';
  valInp.style.cssText = 'flex:1;min-width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';

  opSel.addEventListener('change', function(){
    var noVal = opSel.value==='ЕСТЬ ПУСТО'||opSel.value==='НЕ ЕСТЬ ПУСТО';
    valInp.style.display = noVal ? 'none' : '';
    qbGenerate();
  });
  fsel.addEventListener('change', function(){ qbGenerate(); });
  valInp.addEventListener('input', function(){ qbGenerate(); });

  var del = document.createElement('button');
  del.type='button'; del.textContent='×';
  del.style.cssText = 'background:none;border:none;color:#ef4444;cursor:pointer;font-size:16px;line-height:1';
  del.addEventListener('click', function(){ div.remove(); qbGenerate(); });

  div.appendChild(fsel); div.appendChild(opSel); div.appendChild(valInp); div.appendChild(del);
  document.getElementById('qb-conds').appendChild(div);
  qbGenerate();
}

// ─── ORDER BY ─────────────────────────────────────────────────────────────────
function qbAddOrder(){
  var div = document.createElement('div');
  div.style.cssText = 'display:flex;gap:4px;margin-bottom:6px;align-items:center';

  var fsel = document.createElement('select');
  fsel.style.cssText = 'flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  _curFields.forEach(function(f){
    var o=document.createElement('option'); o.value=f.name; o.textContent=f.name; fsel.appendChild(o);
  });

  var dirSel = document.createElement('select');
  dirSel.style.cssText = 'width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  [['ВОЗР','↑ ВОЗР'],['УБЫВ','↓ УБЫВ']].forEach(function(x){
    var o=document.createElement('option'); o.value=x[0]; o.textContent=x[1]; dirSel.appendChild(o);
  });

  var del = document.createElement('button');
  del.type='button'; del.textContent='×';
  del.style.cssText = 'background:none;border:none;color:#ef4444;cursor:pointer;font-size:16px;line-height:1';
  del.addEventListener('click', function(){ div.remove(); qbGenerate(); });

  fsel.addEventListener('change', function(){ qbGenerate(); });
  dirSel.addEventListener('change', function(){ qbGenerate(); });
  div.appendChild(fsel); div.appendChild(dirSel); div.appendChild(del);
  document.getElementById('qb-orders').appendChild(div);
  qbGenerate();
}

// ─── Generate query text ──────────────────────────────────────────────────────
function qbGenerate(){
  var srcId = document.getElementById('qb-src').value;
  var src = _srcMap[srcId];
  if(!src){
    document.getElementById('qb-query-out').value='';
    document.getElementById('qb-dsl-out').value='';
    return;
  }

  var mainAlias = document.getElementById('qb-main-alias').value.trim();
  var activeJoins = _joins.filter(function(j){ return !!j.srcSel.value && !!j.aliasInp.value.trim(); });
  var hasJoins = activeJoins.length > 0;

  // SELECT fields
  var selParts = [];
  var hasAgg = false;
  var groupFields = [];
  _curFields.forEach(function(f){
    var info = _selFields[f.name];
    if(!info) return;
    var expr = f.name;
    if(info.agg){ expr = info.agg+'('+f.name+')'; hasAgg = true; }
    else { groupFields.push(f.name); }
    if(info.alias) expr += ' КАК '+info.alias;
    selParts.push('  '+expr);
  });
  if(!selParts.length) selParts=['  *'];

  // FROM clause — main source
  var fromClause = src.label;
  if(src.vtParam){
    var vtVal = document.getElementById('qb-vt-param-val').value.trim() || src.vtParam;
    fromClause = fromClause.replace(/\(.*?\)/,'('+vtVal+')');
  }
  if(mainAlias || hasJoins){
    fromClause += ' КАК ' + (mainAlias || 'Т');
  }

  // JOIN clauses
  activeJoins.forEach(function(j){
    var jSrc = _srcMap[j.srcSel.value];
    var jAlias = j.aliasInp.value.trim();
    var jLabel = jSrc.label;
    if(jSrc.vtParam){
      // VT joins: keep param as-is
    }
    var onCond = j.onInp.value.trim();
    fromClause += '\n  '+j.typeSel.value+' СОЕДИНЕНИЕ '+jLabel+' КАК '+jAlias;
    fromClause += '\n  ПО ' + (onCond || '/* условие соединения */');
  });

  // WHERE
  var whereParts = [];
  var params = {};
  document.getElementById('qb-conds').querySelectorAll('div').forEach(function(row){
    var sels = row.querySelectorAll('select');
    var inp = row.querySelector('input[type=text]');
    if(!sels[0]) return;
    var field = sels[0].value;
    var op = sels[1] ? sels[1].value : '=';
    var val = (inp && inp.style.display!=='none') ? inp.value.trim() : '';
    if(op==='ЕСТЬ ПУСТО'||op==='НЕ ЕСТЬ ПУСТО'){
      whereParts.push(field+' '+op);
    } else if(val){
      var m = val.match(/&[А-Яа-яёЁA-Za-z_]\w*/g);
      if(m) m.forEach(function(p){ params[p]=true; });
      whereParts.push(op==='В' ? field+' В ('+val+')' : field+' '+op+' '+val);
    }
  });
  // Also detect params in ON conditions
  activeJoins.forEach(function(j){
    var m = j.onInp.value.match(/&[А-Яа-яёЁA-Za-z_]\w*/g);
    if(m) m.forEach(function(p){ params[p]=true; });
  });

  // ORDER BY
  var orderParts = [];
  document.getElementById('qb-orders').querySelectorAll('div').forEach(function(row){
    var sels = row.querySelectorAll('select');
    if(!sels[0]) return;
    var f = sels[0].value;
    var d = sels[1] ? sels[1].value : 'ВОЗР';
    orderParts.push(d==='УБЫВ' ? f+' УБЫВ' : f);
  });

  // Build query
  var q = 'ВЫБРАТЬ\n'+selParts.join(',\n')+'\nИЗ '+fromClause;
  if(whereParts.length)    q += '\nГДЕ '+whereParts.join('\n  И ');
  if(hasAgg && groupFields.length) q += '\nСГРУППИРОВАТЬ ПО '+groupFields.join(', ');
  if(orderParts.length)    q += '\nУПОРЯДОЧИТЬ ПО '+orderParts.join(', ');

  document.getElementById('qb-query-out').value = q;

  // Detected params
  var pList = Object.keys(params);
  var paramDiv = document.getElementById('qb-params');
  paramDiv.innerHTML = pList.length
    ? pList.map(function(p){ return '<code style="background:#f1f5f9;padding:2px 6px;border-radius:4px;margin-right:4px">'+p+'</code>'; }).join('')
    : '—';

  // DSL fragment (multiline string with | continuation)
  var qLines = q.split('\n');
  var strLit = '"'+qLines[0];
  for(var i=1;i<qLines.length;i++) strLit += '\n|'+qLines[i];
  strLit += '"';

  var dsl = 'Запрос = Новый Запрос;\n';
  dsl += 'Запрос.Текст =\n  '+strLit+';\n';
  pList.forEach(function(p){
    dsl += 'Запрос.УстановитьПараметр("'+p.slice(1)+'", '+p+');\n';
  });
  dsl += 'Результат = Запрос.Выполнить();\n\n';
  dsl += 'Для Каждого Строка Из Результат Цикл\n';
  var ff = _curFields.find(function(f){
    return !!_selFields[f.name] && !f.name.endsWith('.Ссылка') && f.name !== 'Ссылка';
  });
  if(ff){
    var fn = (_selFields[ff.name] && _selFields[ff.name].alias) || ff.name.replace(/.*\./, '');
    dsl += '  Сообщить(Строка.'+fn+');\n';
  }
  dsl += 'КонецЦикла;';
  document.getElementById('qb-dsl-out').value = dsl;
}

function qbCopyQuery(){ var t=document.getElementById('qb-query-out'); t.select(); document.execCommand('copy'); }
function qbCopyDSL()  { var t=document.getElementById('qb-dsl-out');   t.select(); document.execCommand('copy'); }
function qbRunAction(action, el) {
  if (action === 'add-join') qbAddJoin();
  else if (action === 'all-fields') qbAllFields(el.getAttribute('data-ob-qb-all-fields') === 'true');
  else if (action === 'add-cond') qbAddCond();
  else if (action === 'add-order') qbAddOrder();
  else if (action === 'copy-query') qbCopyQuery();
  else if (action === 'copy-dsl') qbCopyDSL();
}
function qbInitDelegates() {
  document.addEventListener('click', function(e) {
    var btn = e.target.closest && e.target.closest('[data-ob-qb-action]');
    if (!btn) return;
    e.preventDefault();
    qbRunAction(btn.getAttribute('data-ob-qb-action') || '', btn);
  });
  document.addEventListener('change', function(e) {
    if (e.target.matches && e.target.matches('[data-ob-qb-source]')) qbSetSource(e.target.value);
  });
  document.addEventListener('input', function(e) {
    if (!e.target.matches) return;
    if (e.target.matches('[data-ob-qb-main-alias]')) qbRebuildAllFields();
    if (e.target.matches('[data-ob-qb-vt-param]')) qbGenerate();
  });
}
qbInitDelegates();
</script>
</div></body></html>
{{end}}
`
