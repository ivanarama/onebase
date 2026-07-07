package ui

const tplQueryConsole = `
{{define "page-query-console"}}
{{template "head" .}}{{template "nav" .}}
<main style="max-width:100%">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">
  <h2 style="margin:0">{{t $.Lang "Консоль запросов"}}</h2>
  <div style="display:flex;gap:8px;align-items:center">
    <button data-ob-qc-action="exec" class="btn" style="background:#3b82f6;color:#fff;border:none;border-radius:6px;padding:6px 16px;cursor:pointer;font-size:13px">{{t $.Lang "▶ Выполнить"}}</button>
    <button data-ob-qc-action="toggle-builder" class="btn" style="background:#e2e8f0;color:#475569;border:none;border-radius:6px;padding:6px 16px;cursor:pointer;font-size:13px" id="qc-builder-btn">{{t $.Lang "Конструктор"}}</button>
    <button data-ob-qc-action="clear" style="background:none;border:1px solid #e2e8f0;border-radius:6px;padding:6px 12px;cursor:pointer;font-size:13px;color:#64748b">{{t $.Lang "Очистить"}}</button>
    <span id="qc-status" style="font-size:12px;color:#64748b"></span>
  </div>
</div>

<!-- Builder panel (collapsible) -->
<div id="qc-builder" style="display:none;margin-bottom:12px">
<div style="display:grid;grid-template-columns:380px 1fr;gap:16px;align-items:start">

<!-- LEFT: builder panels -->
<div>
<div class="card" style="margin-bottom:10px">
<h3 style="margin-top:0">{{t $.Lang "Источник данных"}}</h3>
<select id="qb-src" data-ob-qb-source style="width:100%;margin-bottom:8px">
  <option value="">{{t $.Lang "— выбрать —"}}</option>
</select>
<div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">
  <span style="font-size:12px;color:#64748b;flex-shrink:0;width:70px">{{t $.Lang "Псевдоним:"}}</span>
  <input id="qb-main-alias" type="text" placeholder="{{t $.Lang "напр. Прод"}}" data-ob-qb-main-alias
    style="width:110px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 6px">
</div>
<div id="qb-vt-param" style="display:none;margin-top:4px">
  <label style="font-size:12px;color:#64748b">{{t $.Lang "Параметры виртуальной таблицы"}}</label>
  <input id="qb-vt-param-val" type="text" data-ob-qb-vt-param style="width:100%;margin-top:4px" placeholder="{{t $.Lang "например: &НаДату"}}">
</div>
</div>

<div class="card" style="margin-bottom:10px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Соединения"}}</h3>
  <button data-ob-qb-action="add-join" style="background:#dbeafe;color:#1d4ed8;border:none;border-radius:4px;padding:2px 8px;font-size:12px;cursor:pointer">{{t $.Lang "+ Добавить"}}</button>
</div>
<div id="qb-joins"><p style="font-size:12px;color:#94a3b8;margin:0" id="qb-joins-hint">{{t $.Lang "Нет соединений"}}</p></div>
</div>

<div class="card" style="margin-bottom:10px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Поля (ВЫБРАТЬ)"}}</h3>
  <div style="display:flex;gap:4px">
    <button data-ob-qb-action="all-fields" data-ob-qb-all-fields="true" style="background:#e2e8f0;color:#475569;border:none;border-radius:4px;padding:2px 8px;font-size:12px;cursor:pointer">{{t $.Lang "Все"}}</button>
    <button data-ob-qb-action="all-fields" data-ob-qb-all-fields="false" style="background:#e2e8f0;color:#475569;border:none;border-radius:4px;padding:2px 8px;font-size:12px;cursor:pointer">{{t $.Lang "Сброс"}}</button>
  </div>
</div>
<div id="qb-fields-list" style="max-height:200px;overflow-y:auto"></div>
</div>

<div class="card" style="margin-bottom:10px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Условия (ГДЕ)"}}</h3>
  <button data-ob-qb-action="add-cond" style="background:#dbeafe;color:#1d4ed8;border:none;border-radius:4px;padding:2px 8px;font-size:12px;cursor:pointer">{{t $.Lang "+ Условие"}}</button>
</div>
<div id="qb-conds"></div>
</div>

<div class="card">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Сортировка"}}</h3>
  <button data-ob-qb-action="add-order" style="background:#dbeafe;color:#1d4ed8;border:none;border-radius:4px;padding:2px 8px;font-size:12px;cursor:pointer">{{t $.Lang "+ Поле"}}</button>
</div>
<div id="qb-orders"></div>
</div>
</div><!-- /LEFT -->

<!-- RIGHT: params + apply -->
<div>
<div class="card">
<h3 style="margin-top:0">{{t $.Lang "Параметры запроса"}}</h3>
<p style="font-size:12px;color:#64748b;margin-bottom:8px">{{t $.Lang "Значения &Параметров для выполнения"}}</p>
<div id="qb-params" style="font-size:13px">—</div>
<div style="margin-top:12px">
  <button data-ob-qb-action="apply-editor" style="background:#3b82f6;color:#fff;border:none;border-radius:6px;padding:6px 16px;cursor:pointer;font-size:13px">{{t $.Lang "Применить к редактору"}}</button>
</div>
</div>
</div>

<div id="qb-debug" style="display:none;background:#fef3c7;color:#92400e;font-size:12px;padding:6px 10px;border-radius:4px;margin-bottom:8px;max-height:120px;overflow-y:auto;white-space:pre-wrap"></div>
</div><!-- /grid -->
</div><!-- /qc-builder -->

<!-- Monaco editor -->
<div class="card" style="margin-bottom:12px">
<div id="qc-editor" style="height:260px;border:1px solid #e2e8f0;border-radius:6px"></div>
<textarea id="qc-textarea" style="display:none;width:100%;height:260px;font-family:Consolas,monospace;font-size:14px;padding:10px;border:1px solid #e2e8f0;border-radius:6px;box-sizing:border-box;resize:vertical">ВЫБРАТЬ *
ИЗ </textarea>
</div>

<!-- Parameters (always visible) -->
<div class="card" style="margin-bottom:12px" id="qc-params-card">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Параметры"}}</h3>
  <button data-ob-qc-action="detect-params" style="background:#dbeafe;color:#1d4ed8;border:none;border-radius:4px;padding:4px 12px;cursor:pointer;font-size:12px">{{t $.Lang "Заполнить из запроса"}}</button>
</div>
<div id="qc-params" style="font-size:13px"><span style="color:#94a3b8">{{t $.Lang "Нажмите «Заполнить из запроса» или введите вручную"}}</span></div>
</div>

<!-- Results -->
<div class="card" id="qc-results-card" style="display:none">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Результат"}}</h3>
  <span id="qc-result-info" style="font-size:12px;color:#64748b"></span>
</div>
<div style="overflow-x:auto;max-height:400px;overflow-y:auto">
<table id="qc-result-table" class="tbl" style="font-size:13px;white-space:nowrap">
  <thead id="qc-thead"></thead>
  <tbody id="qc-tbody"></tbody>
</table>
</div>
</div>

<div id="qc-error" class="card" style="display:none;border-color:#fca5a5;background:#fef2f2">
  <pre style="margin:0;font-size:13px;color:#991b1b;white-space:pre-wrap"></pre>
</div>

</main>

<script>
// Самохостинг Monaco: web-воркер из встроенного /vendor/monaco/ (тот же origin).
window.MonacoEnvironment = { getWorkerUrl: function () {
  return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(
    "self.MonacoEnvironment={baseUrl:'" + location.origin + "/vendor/monaco/'};" +
    "importScripts('" + location.origin + "/vendor/monaco/vs/base/worker/workerMain.js');");
}};
</script>
<script src="/vendor/monaco/vs/loader.js"
  onerror="document.getElementById('qc-editor').style.display='none';document.getElementById('qc-textarea').style.display='block'"></script>
<script>
var _schema = {{.Schema}};
var _srcMap = {};
_schema.forEach(function(s){ _srcMap[s.id] = s; });

// Единые геттер/сеттер — прозрачно работают с Monaco и с textarea-fallback.
function qcGetValue() { return window.qcEditor ? window.qcEditor.getValue() : document.getElementById('qc-textarea').value; }
function qcSetValue(v) { if (window.qcEditor) window.qcEditor.setValue(v); else document.getElementById('qc-textarea').value = v; }

if (typeof require !== 'undefined') {
require.config({ paths: { 'vs': '/vendor/monaco/vs' }});
require(['vs/editor/editor.main'], function() {
  monaco.languages.register({ id: 'onebase-query' });
  monaco.languages.setMonarchTokensProvider('onebase-query', {
    keywords: ['ВЫБРАТЬ','ИЗ','ГДЕ','СГРУППИРОВАТЬ','УПОРЯДОЧИТЬ','ПО','ИМЕЯ','КАК','И','ИЛИ','НЕ','В','ВЫБОР','КОГДА','ТОГДА','ИНАЧЕ','КОНЕЦ','УБЫВ','ВОЗР','РАЗЛИЧНЫЕ','ЕСТЬ','ПУСТО','ОБЪЕДИНИТЬ','ВСЕ','ЛЕВОЕ','ВНУТРЕННЕЕ','ПРАВОЕ','ПОЛНОЕ','СОЕДИНЕНИЕ','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ','SELECT','FROM','WHERE','GROUP','ORDER','BY','ON','AND','OR','NOT','IN','AS','JOIN','INNER','LEFT','RIGHT','FULL','HAVING'],
    tokenizer: {
      root: [
        [/&[А-Яа-яёЁA-Za-z_][А-Яа-яёЁA-Za-z_0-9]*/, 'variable.predefined'],
        [/"[^"]*"/, 'string'],
        [/'[^']*'/, 'string'],
        [/\d+(\.\d+)?/, 'number'],
        [/[A-Za-zА-Яа-яёЁ_]\w*/, { cases: { '@keywords': 'keyword', '@default': 'identifier' } }],
      ]
    }
  });
  window.qcEditor = monaco.editor.create(document.getElementById('qc-editor'), {
    value: 'ВЫБРАТЬ *\nИЗ ',
    language: 'onebase-query',
    theme: 'vs',
    minimap: { enabled: false },
    automaticLayout: true,
    fontSize: 14,
    lineNumbers: 'on',
    scrollBeyondLastLine: false
  });
});
}

function qcToggleBuilder() {
  var el = document.getElementById('qc-builder');
  var btn = document.getElementById('qc-builder-btn');
  if (el.style.display === 'none') {
    el.style.display = '';
    btn.style.background = '#3b82f6';
    btn.style.color = '#fff';
    qcParseQueryToBuilder();
  } else {
    qbApplyToEditor();
    el.style.display = 'none';
    btn.style.background = '#e2e8f0';
    btn.style.color = '#475569';
  }
}


function qcParseQueryToBuilder() {
  var dbgEl = document.getElementById('qb-debug');
  var dbg = function(msg) { if(dbgEl){dbgEl.style.display=''; dbgEl.textContent += msg + '\\n';} };
  if(dbgEl) dbgEl.textContent = '';
  try {
  dbg('1. start');
  var code = qcGetValue();
  if (!code.trim()) { dbg('empty code'); return; }
  var norm = code.replace(/\s+/g, ' ').trim();
  dbg('2. norm=' + norm.substring(0, 80));

  // 1. Parse FROM source + alias
  var fromM = norm.match(/(^|\s)ИЗ\s+([\wА-Яа-яёЁ.]+(?:\([^)]*\))?)(?:\s+КАК\s+([\wА-Яа-яёЁ]+))?/i);
  if (!fromM) { dbg('3. FROM not found'); return; }
  var fromExpr = fromM[2].trim().replace(/\(.*$/, '').toLowerCase().trim();
  var fromAlias = fromM[3] || '';
  dbg('3. fromExpr=' + fromExpr + ' alias=' + fromAlias);
  var srcId = null;
  _schema.forEach(function(s) {
    var lbl = s.label.replace(/\(.*$/, '').toLowerCase().trim();
    if (lbl === fromExpr) srcId = s.id;
  });
  if (!srcId) { dbg('3b. srcId not found'); return; }
  dbg('3c. srcId=' + srcId);
  var curSrc = document.getElementById('qb-src').value;
  if (curSrc !== srcId) {
    document.getElementById('qb-src').value = srcId;
    qbSetSource(srcId);
  }
  if (fromAlias) document.getElementById('qb-main-alias').value = fromAlias;

  // VT params
  var vtSrc = _srcMap[srcId];
  if (vtSrc && vtSrc.vtParam) {
    var vtM = fromM[2].match(/\((.+)\)$/);
    document.getElementById('qb-vt-param-val').value = vtM ? vtM[1] : vtSrc.vtParam;
  }

  // 2. Parse JOINs
  var joinRe = /(^|\s)(ЛЕВОЕ|ВНУТРЕННЕЕ|ПРАВОЕ|ПОЛНОЕ)\s+СОЕДИНЕНИЕ\s+([\wА-Яа-яёЁ.]+)(?:\s+КАК\s+([\wА-Яа-яёЁ]+))?\s+ПО\s+(.+?)(?=\s+(?:ГДЕ|СГРУППИРОВАТЬ|УПОРЯДОЧИТЬ|ЛЕВОЕ|ВНУТРЕННЕЕ|ПРАВОЕ|ПОЛНОЕ|$))/gi;
  _joins = [];
  document.getElementById('qb-joins').innerHTML = '';
  var jm;
  dbg('4. scanning joins...');
  while ((jm = joinRe.exec(norm)) !== null) {
    var jType = jm[2], jLabel = jm[3], jAlias = jm[4] || '', jOn = jm[5] ? jm[5].trim() : '';
    dbg('4a. join: ' + jType + ' ' + jLabel + ' AS ' + jAlias);
    var jLabelClean = jLabel.replace(/\(.*$/, '').toLowerCase().trim();
    var jSrcId = '';
    _schema.forEach(function(s) {
      var lbl = s.label.replace(/\(.*$/, '').toLowerCase().trim();
      if (lbl === jLabelClean) jSrcId = s.id;
    });
    if (!jSrcId) continue;
    qbAddJoin();
    var lastJoin = _joins[_joins.length - 1];
    if (lastJoin) {
      for (var ti = 0; ti < lastJoin.typeSel.options.length; ti++) {
        if (lastJoin.typeSel.options[ti].value === jType) { lastJoin.typeSel.selectedIndex = ti; break; }
      }
      lastJoin.srcSel.value = jSrcId;
      if (jAlias) lastJoin.aliasInp.value = jAlias;
      if (jOn) lastJoin.onInp.value = jOn;
    }
  }
  if (!_joins.length) {
    document.getElementById('qb-joins').innerHTML =
      '<p style="font-size:12px;color:#94a3b8;margin:0" id="qb-joins-hint">Нет соединений</p>';
  }

  // 3. Parse SELECT fields
  var selM = norm.match(/(^|\s)ВЫБРАТЬ\s+(.+?)(?=\s+ИЗ(\s|$))/i);
  if (selM) {
    var selParts = splitTopLevel(selM[2], ',');
    _selFields = {};
    selParts.forEach(function(part) {
      part = part.trim(); if (!part) return;
      var asM = part.match(/(.+?)\s+КАК\s+([\wА-Яа-яёЁ]+)$/i);
      var expr = asM ? asM[1].trim() : part;
      var alias = asM ? asM[2].trim() : '';
      var aggM = expr.match(/^(СУММА|КОЛИЧЕСТВО|МИНИМУМ|МАКСИМУМ|СРЕДНЕЕ)\((.+)\)$/i);
      var agg = aggM ? aggM[1] : '';
      var field = aggM ? aggM[2].trim() : expr;
      if (fromAlias && field.toLowerCase().indexOf(fromAlias.toLowerCase() + '.') === 0)
        field = field.substring(fromAlias.length + 1);
      _selFields[field] = {alias: alias, agg: agg};
    });
    qbRebuildAllFields();
  } else {
    qbRebuildAllFields();
  }

  // 4. Parse WHERE
  var whereM = norm.match(/(^|\s)ГДЕ\s+(.+?)(?=\s+(?:СГРУППИРОВАТЬ|УПОРЯДОЧИТЬ|$))/i);
  if (whereM) {
    var condParts = splitTopLevel(whereM[2], /\s+И\s+/i);
    condParts.forEach(function(cp) {
      cp = cp.trim();
      if (cp.charAt(0) === '(' && cp.charAt(cp.length-1) === ')')
        cp = cp.substring(1, cp.length-1).trim();
      var opM = cp.match(/(.+?)\s*(<>|>=|<=|!=|=|>|<|ЕСТЬ\s+ПУСТО|НЕ\s+ЕСТЬ\s+ПУСТО|ПОДОБНО|В)\s*(.*)/i);
      if (!opM) return;
      var cField = opM[1].trim(), cOp = opM[2].trim(), cVal = (opM[3] || '').trim();
      if (fromAlias && cField.toLowerCase().indexOf(fromAlias.toLowerCase() + '.') === 0)
        cField = cField.substring(fromAlias.length + 1);
      qbAddCond();
      var condDivs = document.getElementById('qb-conds').querySelectorAll('div');
      var lc = condDivs[condDivs.length - 1];
      if (lc) {
        var sels = lc.querySelectorAll('select');
        var inp = lc.querySelector('input[type=text]');
        setSelectValue(sels[0], cField);
        if (sels[1]) setSelectValue(sels[1], cOp);
        if (inp) {
          if (cOp === 'ЕСТЬ ПУСТО' || cOp === 'НЕ ЕСТЬ ПУСТО') inp.style.display = 'none';
          else inp.value = cVal;
        }
      }
    });
  }

  // 5. Parse ORDER BY
  var orderM = norm.match(/(^|\s)УПОРЯДОЧИТЬ\s+ПО\s+(.+)$/i);
  if (orderM) {
    splitTopLevel(orderM[2], ',').forEach(function(op) {
      op = op.trim();
      var dirM = op.match(/(.+?)\s+(УБЫВ|ВОЗР)$/i);
      var oField = dirM ? dirM[1].trim() : op;
      var oDir = dirM ? dirM[2].trim() : 'ВОЗР';
      if (fromAlias && oField.toLowerCase().indexOf(fromAlias.toLowerCase() + '.') === 0)
        oField = oField.substring(fromAlias.length + 1);
      qbAddOrder();
      var orderDivs = document.getElementById('qb-orders').querySelectorAll('div');
      var lo = orderDivs[orderDivs.length - 1];
      if (lo) {
        var osels = lo.querySelectorAll('select');
        setSelectValue(osels[0], oField);
        if (osels[1]) setSelectValue(osels[1], oDir);
      }
    });
  }

  qbGenerate();
  dbg('DONE');
  } catch(e) { dbg('ERROR: ' + e.message); }
}

function setSelectValue(sel, val) {
  if (!sel) return;
  for (var i = 0; i < sel.options.length; i++) {
    if (sel.options[i].value === val) { sel.selectedIndex = i; return; }
  }
  var o = document.createElement('option'); o.value = val; o.textContent = val;
  sel.appendChild(o); sel.value = val;
}

function splitTopLevel(str, sep) {
  var parts = [], depth = 0, cur = '';
  if (typeof sep === 'string') {
    for (var i = 0; i < str.length; i++) {
      var ch = str.charAt(i);
      if (ch === '(') depth++;
      else if (ch === ')') depth--;
      if (depth === 0 && str.substring(i, i + sep.length) === sep) {
        parts.push(cur); cur = ''; i += sep.length - 1;
      } else cur += ch;
    }
    if (cur) parts.push(cur);
  } else {
    var rem = str;
    while (rem.length > 0) {
      var m = rem.match(sep);
      if (!m) { parts.push(cur + rem); cur = ''; break; }
      var before = rem.substring(0, m.index);
      var pd = (before.match(/\(/g) || []).length - (before.match(/\)/g) || []).length;
      if (pd === 0 && depth === 0) {
        parts.push(cur + before); cur = ''; rem = rem.substring(m.index + m[0].length);
      } else {
        cur += rem.substring(0, m.index + m[0].length);
        depth += pd; rem = rem.substring(m.index + m[0].length);
      }
    }
    if (cur) parts.push(cur);
  }
  return parts;
}

function qcExec() {
  var code = qcGetValue();
  // Auto-detect params from query if panel is empty
  var hasParams = document.querySelectorAll('.qc-param-row').length > 0;
  if (!hasParams) qcDetectParams();
  // Collect params
  var params = {};
  var emptyParams = [];
  document.querySelectorAll('.qc-param-row').forEach(function(row) {
    var k = (row.dataset.name || '').trim();
    var v = row.querySelector('.qc-pv').value;
    var t = row.dataset.type || 'string';
    if (k) {
      if (v === '') { emptyParams.push(k); return; }
      if (t === 'number') { var n = Number(v); params[k] = isNaN(n) ? v : n; }
      else params[k] = v;
    }
  });
  if (emptyParams.length > 0) {
    document.getElementById('qc-error').style.display = '';
    document.getElementById('qc-error').querySelector('pre').textContent = 'Заполните параметры: ' + emptyParams.join(', ');
    return;
  }
  document.getElementById('qc-status').textContent = 'Выполнение...';
  fetch('/ui/dev/query-exec', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({query: code, params: params})
  }).then(function(r){ return r.json(); }).then(function(data) {
    document.getElementById('qc-status').textContent = '';
    var errDiv = document.getElementById('qc-error');
    var resCard = document.getElementById('qc-results-card');
    if (data.error) {
      errDiv.style.display = '';
      errDiv.querySelector('pre').textContent = data.error;
      resCard.style.display = 'none';
      return;
    }
    errDiv.style.display = 'none';
    resCard.style.display = '';
    document.getElementById('qc-result-info').textContent =
      (data.count || 0) + ' строк' + (data.time ? ', ' + data.time : '');

    var cols = data.columns || [];
    var thead = document.getElementById('qc-thead');
    thead.innerHTML = '<tr>' + cols.map(function(c){ return '<th>' + escHtml(c) + '</th>'; }).join('') + '</tr>';

    var tbody = document.getElementById('qc-tbody');
    tbody.innerHTML = '';
    (data.rows || []).forEach(function(row) {
      var tr = document.createElement('tr');
      row.forEach(function(cell) {
        var td = document.createElement('td');
        td.textContent = cell == null ? '' : String(cell);
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
  }).catch(function(e) {
    document.getElementById('qc-status').textContent = 'Ошибка сети';
  });
}

function qcClear() {
  qcSetValue('ВЫБРАТЬ *\nИЗ ');
  document.getElementById('qc-results-card').style.display = 'none';
  document.getElementById('qc-error').style.display = 'none';
  document.getElementById('qc-params').innerHTML = '<span style="color:#94a3b8">Нажмите «Заполнить из запроса» или введите вручную</span>';
}

function qcDetectParams() {
  var code = qcGetValue();
  var re = /&([А-Яа-яёЁA-Za-z_][А-Яа-яёЁA-Za-z_0-9]*)/g;
  var found = {};
  var m;
  while ((m = re.exec(code)) !== null) { found[m[1]] = true; }
  var names = Object.keys(found);
  if (!names.length) {
    document.getElementById('qc-params').innerHTML = '<span style="color:#94a3b8">Параметры не найдены</span>';
    return;
  }
  // Ask backend to detect types
  fetch('/ui/dev/query-analyze', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({query: code})
  }).then(function(r){ return r.json(); }).then(function(data) {
    var types = data.paramTypes || {};
    var existing = {};
    document.querySelectorAll('.qc-param-row').forEach(function(row) {
      var k = row.dataset.name || '';
      existing[k] = row.querySelector('.qc-pv').value;
    });
    var html = '';
    names.forEach(function(name) {
      var t = types[name] || 'string';
      var prev = existing[name] || '';
      var hint;
      if (t === 'number') hint = 'Число';
      else if (t === 'date') hint = 'Дата';
      else if (t === 'uuid') hint = 'UUID';
      else if (t.indexOf('reference:') === 0) hint = t.replace('reference:', '');
      else hint = 'Строка';
      html += '<div class="qc-param-row" data-type="'+t+'" data-name="'+escHtml(name)+'" style="display:flex;gap:8px;align-items:center;margin-bottom:6px">'
        + '<span style="width:140px;font-size:13px;font-weight:600;color:#334155">&amp;'+escHtml(name)+'</span>'
        + '<span style="font-size:11px;color:#94a3b8;min-width:90px">'+escHtml(hint)+'</span>';
      if (t.indexOf('reference:') === 0) {
        var entityType = t.replace('reference:', '');
        html += '<div style="flex:1;display:flex;gap:4px;position:relative">'
          + '<div style="flex:1;position:relative">'
          + '<input class="qc-pn" autocomplete="off" placeholder="введите для поиска" style="width:100%;box-sizing:border-box;font-size:13px;border:1px solid #e2e8f0;border-radius:4px;padding:4px 8px">'
          + '<input class="qc-pv" type="hidden" value="'+escHtml(prev)+'">'
          + '<div class="qc-suggest-list" style="display:none;position:absolute;top:100%;left:0;right:0;border:1px solid #e2e8f0;border-radius:4px;background:#fff;z-index:100;max-height:180px;overflow-y:auto;box-shadow:0 4px 8px rgba(0,0,0,.12)"></div>'
          + '</div>'
          + '<button type="button" data-ob-qc-open-picker data-ob-qc-picker-entity="'+escHtml(entityType)+'" title="Выбрать из справочника" style="padding:4px 8px;font-size:13px;border:1px solid #e2e8f0;border-radius:4px;background:#f8fafc;cursor:pointer;white-space:nowrap">...</button>'
          + '</div>';
      } else {
        html += '<input class="qc-pv" value="'+escHtml(prev)+'" placeholder="значение" style="flex:1;font-size:13px;border:1px solid #e2e8f0;border-radius:4px;padding:4px 8px">';
      }
      html += '</div>';
    });
    document.getElementById('qc-params').innerHTML = html;
  }).catch(function() {
    // Fallback: just show string params without type info
    var html = '';
    names.forEach(function(name) {
      var prev = '';
      document.querySelectorAll('.qc-param-row').forEach(function(row) {
        if ((row.dataset.name || '') === name) prev = row.querySelector('.qc-pv').value;
      });
      html += '<div class="qc-param-row" data-type="string" data-name="'+escHtml(name)+'" style="display:flex;gap:8px;align-items:center;margin-bottom:6px">'
        + '<span style="width:140px;font-size:13px;font-weight:600;color:#334155">&amp;'+escHtml(name)+'</span>'
        + '<span style="font-size:11px;color:#94a3b8;width:60px">Строка</span>'
        + '<input class="qc-pv" value="'+escHtml(prev)+'" placeholder="значение" style="flex:1;font-size:13px;border:1px solid #e2e8f0;border-radius:4px;padding:4px 8px">'
        + '</div>';
    });
    document.getElementById('qc-params').innerHTML = html;
  });
}

function escHtml(s) {
  var d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// ─── Query Builder JS (shared with standalone builder) ────────────────────
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

var _curFields = [];
var _selFields = {};
var _joins = [];

function qbSetSource(id){
  var src = _srcMap[id];
  _selFields = {};
  document.getElementById('qb-conds').innerHTML = '';
  document.getElementById('qb-orders').innerHTML = '';
  _joins = [];
  document.getElementById('qb-joins').innerHTML = '<p style="font-size:12px;color:#94a3b8;margin:0" id="qb-joins-hint">Нет соединений</p>';
  document.getElementById('qb-main-alias').value = '';
  var vtDiv = document.getElementById('qb-vt-param');
  if(src && src.vtParam){ vtDiv.style.display=''; document.getElementById('qb-vt-param-val').value=src.vtParam; }
  else { vtDiv.style.display='none'; }
  qbRebuildAllFields();
}

function qbRebuildAllFields(){
  var srcId = document.getElementById('qb-src').value;
  var mainSrc = _srcMap[srcId];
  var mainAlias = document.getElementById('qb-main-alias').value.trim();
  var all = [];
  if(mainSrc){
    mainSrc.fields.forEach(function(f){
      var name = mainAlias ? mainAlias+'.'+f.name : f.name;
      all.push({name: name, label: name, type: f.type});
    });
    if(mainAlias) all.push({name: mainAlias+'.Ссылка', label: mainAlias+'.Ссылка (id)', type: 'ref'});
  }
  _joins.forEach(function(j){
    var src = _srcMap[j.srcSel.value]; var alias = j.aliasInp.value.trim();
    if(!src || !alias) return;
    src.fields.forEach(function(f){ all.push({name: alias+'.'+f.name, label: alias+'.'+f.name, type: f.type}); });
    all.push({name: alias+'.Ссылка', label: alias+'.Ссылка (id)', type: 'ref'});
  });
  var nameSet = {}; all.forEach(function(f){ nameSet[f.name]=true; });
  var newSel = {};
  Object.keys(_selFields).forEach(function(k){ if(nameSet[k]) newSel[k]=_selFields[k]; });
  if(!Object.keys(newSel).length && mainSrc){
    all.forEach(function(f){ if(!f.name.endsWith('.Ссылка') && f.name !== 'Ссылка') newSel[f.name]={alias:'',agg:''}; });
  }
  _selFields = newSel; _curFields = all;
  renderFields(); qbGenerate();
}

function qbAddJoin(){
  var mainAliasInp = document.getElementById('qb-main-alias');
  if(!mainAliasInp.value.trim()){
    var mainSrc = _srcMap[document.getElementById('qb-src').value];
    if(mainSrc){ var parts=mainSrc.label.split('.'); mainAliasInp.value=parts.length>=2?parts[1].replace(/\(.*$/,''):parts[0]; }
  }
  var hint = document.getElementById('qb-joins-hint'); if(hint) hint.remove();
  var jid = Date.now();
  var div = document.createElement('div');
  div.dataset.jid = jid;
  div.style.cssText = 'border:1px solid #e2e8f0;border-radius:6px;padding:8px;margin-bottom:8px;background:#f8fafc';
  var row1 = document.createElement('div');
  row1.style.cssText = 'display:flex;gap:6px;align-items:center;margin-bottom:6px;flex-wrap:wrap';
  var typeSel = document.createElement('select');
  typeSel.style.cssText = 'width:126px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';
  [['ЛЕВОЕ','⬅ ЛЕВОЕ'],['ВНУТРЕННЕЕ','✕ ВНУТРЕННЕЕ'],['ПРАВОЕ','➡ ПРАВОЕ'],['ПОЛНОЕ','⟺ ПОЛНОЕ']].forEach(function(x){
    var o = document.createElement('option'); o.value=x[0]; o.textContent=x[1]; typeSel.appendChild(o);
  });
  var srcSel = document.createElement('select');
  srcSel.style.cssText = 'flex:1;min-width:140px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';
  var o0 = document.createElement('option'); o0.value=''; o0.textContent='— источник —'; srcSel.appendChild(o0);
  var jgroups = {};
  _schema.forEach(function(s){ if(!jgroups[s.group]) jgroups[s.group]=[]; jgroups[s.group].push(s); });
  Object.keys(jgroups).forEach(function(g){
    var og = document.createElement('optgroup'); og.label=g;
    jgroups[g].forEach(function(s){ var o=document.createElement('option'); o.value=s.id; o.textContent=s.label; og.appendChild(o); });
    srcSel.appendChild(og);
  });
  var aliasInp = document.createElement('input');
  aliasInp.type='text'; aliasInp.placeholder='Псевдоним';
  aliasInp.style.cssText = 'width:86px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';
  var del = document.createElement('button');
  del.type='button'; del.textContent='×';
  del.style.cssText = 'background:none;border:none;color:#ef4444;cursor:pointer;font-size:18px;line-height:1;flex-shrink:0;padding:0 2px';
  del.addEventListener('click', function(){
    div.remove(); _joins=_joins.filter(function(j){ return j.id!==jid; });
    if(!_joins.length) document.getElementById('qb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="qb-joins-hint">Нет соединений</p>';
    qbRebuildAllFields();
  });
  row1.appendChild(typeSel); row1.appendChild(srcSel); row1.appendChild(aliasInp); row1.appendChild(del);
  var row2 = document.createElement('div');
  row2.style.cssText = 'display:flex;gap:6px;align-items:center';
  var onLabel = document.createElement('span'); onLabel.textContent='ПО:'; onLabel.style.cssText='font-size:12px;font-weight:600;color:#475569;flex-shrink:0;width:26px';
  var onInp = document.createElement('input'); onInp.type='text'; onInp.placeholder='Псевд1.Поле = Псевд2.Ссылка';
  onInp.style.cssText = 'flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 6px';
  row2.appendChild(onLabel); row2.appendChild(onInp);
  div.appendChild(row1); div.appendChild(row2);
  document.getElementById('qb-joins').appendChild(div);
  var jdata = {id:jid, el:div, typeSel:typeSel, srcSel:srcSel, aliasInp:aliasInp, onInp:onInp};
  _joins.push(jdata);
  srcSel.addEventListener('change', function(){
    var src=_srcMap[srcSel.value]; if(src&&!aliasInp.value.trim()){ var parts=src.label.split('.'); aliasInp.value=parts.length>=2?parts[1].replace(/\(.*$/,''):parts[0]; }
    qbRebuildAllFields();
  });
  typeSel.addEventListener('change', function(){ qbGenerate(); });
  aliasInp.addEventListener('input', function(){ qbRebuildAllFields(); });
  onInp.addEventListener('input', function(){ qbGenerate(); });
  qbRebuildAllFields();
}

function renderFields(){
  var div = document.getElementById('qb-fields-list');
  div.innerHTML = '';
  if(!_curFields.length){ div.innerHTML='<p style="font-size:12px;color:#94a3b8;margin:4px 0">Сначала выберите источник</p>'; return; }
  var lastPrefix = null;
  _curFields.forEach(function(f){
    var dotIdx = f.name.indexOf('.');
    var prefix = dotIdx >= 0 ? f.name.substring(0, dotIdx) : '';
    if(prefix && prefix !== lastPrefix){
      lastPrefix = prefix;
      var sep = document.createElement('div');
      sep.style.cssText = 'font-size:11px;font-weight:600;color:#64748b;margin:6px 0 2px;padding-left:2px;border-top:1px solid #f1f5f9;padding-top:4px';
      sep.textContent = prefix;
      div.appendChild(sep);
    }
    var row = document.createElement('div');
    row.style.cssText = 'display:flex;align-items:center;gap:6px;margin-bottom:3px;font-size:13px;padding:1px 0';
    var chk = document.createElement('input'); chk.type='checkbox'; chk.checked=!!_selFields[f.name]; chk.dataset.field=f.name;
    chk.addEventListener('change', function(){ if(chk.checked) _selFields[f.name]={alias:'',agg:''}; else delete _selFields[f.name]; qbGenerate(); });
    var lbl = document.createElement('label');
    lbl.textContent = dotIdx>=0 ? f.name.substring(dotIdx+1) : f.name;
    lbl.title = f.name; lbl.style.cssText='flex:1;cursor:pointer;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
    lbl.addEventListener('click', function(){ chk.click(); });
    var aggSel = document.createElement('select');
    aggSel.style.cssText = 'font-size:11px;padding:1px 3px;border:1px solid #e2e8f0;border-radius:4px;width:90px';
    ['','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ'].forEach(function(a){
      var o=document.createElement('option'); o.value=a; o.textContent=a||'— нет —'; aggSel.appendChild(o);
    });
    if(_selFields[f.name]) aggSel.value=_selFields[f.name].agg||'';
    aggSel.addEventListener('change', function(){ if(_selFields[f.name]) _selFields[f.name].agg=aggSel.value; qbGenerate(); });
    var aliasInp = document.createElement('input'); aliasInp.type='text'; aliasInp.placeholder='КАК';
    aliasInp.style.cssText='font-size:11px;width:70px;padding:1px 4px;border:1px solid #e2e8f0;border-radius:4px';
    if(_selFields[f.name]) aliasInp.value=_selFields[f.name].alias||'';
    aliasInp.addEventListener('input', function(){ if(_selFields[f.name]) _selFields[f.name].alias=aliasInp.value.trim(); qbGenerate(); });
    row.appendChild(chk); row.appendChild(lbl); row.appendChild(aggSel); row.appendChild(aliasInp);
    div.appendChild(row);
  });
}

function qbAllFields(v){
  _curFields.forEach(function(f){
    if(f.name.endsWith('.Ссылка')||f.name==='Ссылка') return;
    if(v) _selFields[f.name]={alias:'',agg:''}; else delete _selFields[f.name];
  });
  renderFields(); qbGenerate();
}

function qbAddCond(){
  var div = document.createElement('div');
  div.style.cssText = 'display:flex;gap:4px;margin-bottom:6px;align-items:center;flex-wrap:wrap';
  var fsel = document.createElement('select');
  fsel.style.cssText = 'flex:1;min-width:100px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  _curFields.forEach(function(f){ var o=document.createElement('option'); o.value=f.name; o.textContent=f.name; fsel.appendChild(o); });
  var opSel = document.createElement('select');
  opSel.style.cssText = 'width:100px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  ['=','<>','>','<','>=','<=','ЕСТЬ ПУСТО','НЕ ЕСТЬ ПУСТО','ПОДОБНО','В'].forEach(function(op){
    var o=document.createElement('option'); o.value=op; o.textContent=op; opSel.appendChild(o);
  });
  var valInp = document.createElement('input'); valInp.type='text'; valInp.placeholder='&Параметр или значение';
  valInp.style.cssText = 'flex:1;min-width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 4px';
  opSel.addEventListener('change', function(){ var noVal=opSel.value==='ЕСТЬ ПУСТО'||opSel.value==='НЕ ЕСТЬ ПУСТО'; valInp.style.display=noVal?'none':''; qbGenerate(); });
  fsel.addEventListener('change', function(){ qbGenerate(); });
  valInp.addEventListener('input', function(){ qbGenerate(); });
  var del = document.createElement('button'); del.type='button'; del.textContent='×';
  del.style.cssText = 'background:none;border:none;color:#ef4444;cursor:pointer;font-size:16px;line-height:1';
  del.addEventListener('click', function(){ div.remove(); qbGenerate(); });
  div.appendChild(fsel); div.appendChild(opSel); div.appendChild(valInp); div.appendChild(del);
  document.getElementById('qb-conds').appendChild(div);
  qbGenerate();
}

function qbAddOrder(){
  var div = document.createElement('div');
  div.style.cssText = 'display:flex;gap:4px;margin-bottom:6px;align-items:center';
  var fsel = document.createElement('select');
  fsel.style.cssText = 'flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  _curFields.forEach(function(f){ var o=document.createElement('option'); o.value=f.name; o.textContent=f.name; fsel.appendChild(o); });
  var dirSel = document.createElement('select');
  dirSel.style.cssText = 'width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px';
  [['ВОЗР','↑ ВОЗР'],['УБЫВ','↓ УБЫВ']].forEach(function(x){ var o=document.createElement('option'); o.value=x[0]; o.textContent=x[1]; dirSel.appendChild(o); });
  var del = document.createElement('button'); del.type='button'; del.textContent='×';
  del.style.cssText = 'background:none;border:none;color:#ef4444;cursor:pointer;font-size:16px;line-height:1';
  del.addEventListener('click', function(){ div.remove(); qbGenerate(); });
  fsel.addEventListener('change', function(){ qbGenerate(); });
  dirSel.addEventListener('change', function(){ qbGenerate(); });
  div.appendChild(fsel); div.appendChild(dirSel); div.appendChild(del);
  document.getElementById('qb-orders').appendChild(div);
  qbGenerate();
}

function qbGenerate(){
  var srcId = document.getElementById('qb-src').value;
  var src = _srcMap[srcId];
  if(!src) return;

  var mainAlias = document.getElementById('qb-main-alias').value.trim();
  var activeJoins = _joins.filter(function(j){ return !!j.srcSel.value && !!j.aliasInp.value.trim(); });
  var hasJoins = activeJoins.length > 0;

  var selParts = [];
  var hasAgg = false;
  var groupFields = [];
  _curFields.forEach(function(f){
    var info = _selFields[f.name]; if(!info) return;
    var expr = f.name;
    if(info.agg){ expr = info.agg+'('+f.name+')'; hasAgg = true; } else { groupFields.push(f.name); }
    if(info.alias) expr += ' КАК '+info.alias;
    selParts.push('  '+expr);
  });
  if(!selParts.length) selParts=['  *'];

  var fromClause = src.label;
  if(src.vtParam){
    var vtVal = document.getElementById('qb-vt-param-val').value.trim() || src.vtParam;
    fromClause = fromClause.replace(/\(.*?\)/,'('+vtVal+')');
  }
  if(mainAlias || hasJoins) fromClause += ' КАК '+(mainAlias||'Т');

  activeJoins.forEach(function(j){
    var jSrc = _srcMap[j.srcSel.value]; var jAlias = j.aliasInp.value.trim(); var jLabel = jSrc.label;
    var onCond = j.onInp.value.trim();
    fromClause += '\n  '+j.typeSel.value+' СОЕДИНЕНИЕ '+jLabel+' КАК '+jAlias;
    fromClause += '\n  ПО '+(onCond||'/* условие */');
  });

  var whereParts = [];
  var params = {};
  document.getElementById('qb-conds').querySelectorAll('div').forEach(function(row){
    var sels = row.querySelectorAll('select');
    var inp = row.querySelector('input[type=text]');
    if(!sels[0]) return;
    var field = sels[0].value; var op = sels[1]?sels[1].value:'=';
    var val = (inp && inp.style.display!=='none') ? inp.value.trim() : '';
    if(op==='ЕСТЬ ПУСТО'||op==='НЕ ЕСТЬ ПУСТО'){ whereParts.push(field+' '+op); }
    else if(val){ var m=val.match(/&[А-Яа-яёЁA-Za-z_]\w*/g); if(m) m.forEach(function(p){ params[p]=true; }); whereParts.push(op==='В'?field+' В ('+val+')':field+' '+op+' '+val); }
  });
  activeJoins.forEach(function(j){ var m=j.onInp.value.match(/&[А-Яа-яёЁA-Za-z_]\w*/g); if(m) m.forEach(function(p){ params[p]=true; }); });

  var orderParts = [];
  document.getElementById('qb-orders').querySelectorAll('div').forEach(function(row){
    var sels = row.querySelectorAll('select'); if(!sels[0]) return;
    var f=sels[0].value; var d=sels[1]?sels[1].value:'ВОЗР';
    orderParts.push(d==='УБЫВ'?f+' УБЫВ':f);
  });

  var q = 'ВЫБРАТЬ\n'+selParts.join(',\n')+'\nИЗ '+fromClause;
  if(whereParts.length) q += '\nГДЕ '+whereParts.join('\n  И ');
  if(hasAgg && groupFields.length) q += '\nСГРУППИРОВАТЬ ПО '+groupFields.join(', ');
  if(orderParts.length) q += '\nУПОРЯДОЧИТЬ ПО '+orderParts.join(', ');

  // Update params UI
  var pList = Object.keys(params);
  var paramDiv = document.getElementById('qb-params');
  paramDiv.innerHTML = '';
  if(!pList.length){ paramDiv.innerHTML='—'; return; }
  pList.forEach(function(p){
    var row = document.createElement('div');
    row.className = 'qc-param-row';
    row.style.cssText = 'display:flex;gap:8px;align-items:center;margin-bottom:6px';
    row.innerHTML = '<input class="qc-pk" value="'+p+'" readonly style="width:120px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:4px 6px;background:#f1f5f9">'
      + '<input class="qc-pv" placeholder="значение" style="flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:4px 6px">';
    paramDiv.appendChild(row);
  });

  // Store generated query for apply
  window._qbGeneratedQuery = q;
}

function qbApplyToEditor(){
  if(window._qbGeneratedQuery){
    qcSetValue(window._qbGeneratedQuery);
  }
}

function qbRunAction(action, el) {
  if (action === 'add-join') qbAddJoin();
  else if (action === 'all-fields') qbAllFields(el.getAttribute('data-ob-qb-all-fields') === 'true');
  else if (action === 'add-cond') qbAddCond();
  else if (action === 'add-order') qbAddOrder();
  else if (action === 'apply-editor') qbApplyToEditor();
}

function qcRunAction(action) {
  if (action === 'exec') qcExec();
  else if (action === 'toggle-builder') qcToggleBuilder();
  else if (action === 'clear') qcClear();
  else if (action === 'detect-params') qcDetectParams();
}

function obInitQueryConsoleDelegates() {
  document.addEventListener('click', function(e) {
    var picker = e.target.closest && e.target.closest('[data-ob-qc-open-picker]');
    if (picker) {
      e.preventDefault();
      qcOpenPicker(picker, picker.getAttribute('data-ob-qc-picker-entity') || '');
      return;
    }
    var close = e.target.closest && e.target.closest('[data-ob-qc-picker-close]');
    if (close) {
      e.preventDefault();
      var modal = document.getElementById('qc-picker-modal');
      if (modal) modal.style.display = 'none';
      return;
    }
    var qcBtn = e.target.closest && e.target.closest('[data-ob-qc-action]');
    if (qcBtn) {
      e.preventDefault();
      qcRunAction(qcBtn.getAttribute('data-ob-qc-action') || '');
      return;
    }
    var qbBtn = e.target.closest && e.target.closest('[data-ob-qb-action]');
    if (qbBtn) {
      e.preventDefault();
      qbRunAction(qbBtn.getAttribute('data-ob-qb-action') || '', qbBtn);
    }
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
obInitQueryConsoleDelegates();

// ─── Reference picker helpers ─────────────────────────────────────────────────
function qcItemLabel(item) {
  var nm = item.name != null ? String(item.name) : '';
  var code = item.code != null ? String(item.code) : '';
  if (code) return '[' + code + '] ' + nm;
  return nm || String(item.id || '');
}

function qcFillRef(row, id, label) {
  var pv = row.querySelector('.qc-pv');
  var pn = row.querySelector('.qc-pn');
  if (pv) pv.value = id;
  if (pn) pn.value = label;
}

// ─── Inline autocomplete ──────────────────────────────────────────────────────
document.addEventListener('input', function(e) {
  if (!e.target.classList.contains('qc-pn')) return;
  var row = e.target.closest('.qc-param-row');
  if (!row) return;
  var t = row.dataset.type || '';
  if (t.indexOf('reference:') !== 0) return;
  var entityType = t.replace('reference:', '');
  var q = e.target.value;
  var list = row.querySelector('.qc-suggest-list');
  if (!list) return;
  fetch('/ui/dev/entity-search?type=' + encodeURIComponent(entityType) + '&q=' + encodeURIComponent(q))
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var items = data.items || [];
      if (!items.length) { list.style.display = 'none'; return; }
      var html = '';
      items.forEach(function(item) {
        var id = String(item.id || '');
        var label = escHtml(qcItemLabel(item));
        html += '<div class="qc-suggest-item" data-id="' + escHtml(id) + '" data-label="' + label + '"'
          + ' style="padding:6px 10px;cursor:pointer;font-size:13px;border-bottom:1px solid #f1f5f9;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">'
          + label + '</div>';
      });
      list.innerHTML = html;
      list.style.display = 'block';
    }).catch(function() { list.style.display = 'none'; });
});

document.addEventListener('click', function(e) {
  if (e.target.classList.contains('qc-suggest-item')) {
    var row = e.target.closest('.qc-param-row');
    if (!row) return;
    qcFillRef(row, e.target.dataset.id || '', e.target.dataset.label || e.target.textContent || '');
    var list = row.querySelector('.qc-suggest-list');
    if (list) list.style.display = 'none';
    return;
  }
  if (!e.target.closest('.qc-param-row') && !e.target.closest('#qc-picker-modal')) {
    document.querySelectorAll('.qc-suggest-list').forEach(function(l) { l.style.display = 'none'; });
  }
});

// ─── Modal picker ("...") ─────────────────────────────────────────────────────
var _qcPickerRow = null;

function qcOpenPicker(btn, entityType) {
  _qcPickerRow = btn.closest('.qc-param-row');
  var modal = document.getElementById('qc-picker-modal');
  if (!modal) {
    modal = document.createElement('div');
    modal.id = 'qc-picker-modal';
    modal.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,.4);z-index:1000;display:flex;align-items:center;justify-content:center';
    modal.innerHTML = '<div style="background:#fff;border-radius:8px;box-shadow:0 8px 32px rgba(0,0,0,.2);width:480px;max-width:95vw;max-height:80vh;display:flex;flex-direction:column">'
      + '<div style="padding:12px 16px;border-bottom:1px solid #e2e8f0;display:flex;gap:8px;align-items:center">'
      + '<input id="qc-picker-q" placeholder="Поиск..." autocomplete="off" style="flex:1;font-size:14px;border:1px solid #e2e8f0;border-radius:4px;padding:6px 10px">'
      + '<button type="button" data-ob-qc-picker-close style="background:none;border:none;font-size:18px;cursor:pointer;color:#94a3b8;padding:0 4px">&times;</button>'
      + '</div>'
      + '<div id="qc-picker-list" style="overflow-y:auto;flex:1;min-height:80px"></div>'
      + '</div>';
    document.body.appendChild(modal);
    document.getElementById('qc-picker-q').addEventListener('input', function() {
      qcPickerSearch(this.getAttribute('data-ob-qc-picker-entity') || '', this.value);
    });
    modal.addEventListener('click', function(e) {
      if (e.target === modal) modal.style.display = 'none';
    });
  } else {
    modal.style.display = 'flex';
    document.getElementById('qc-picker-q').value = '';
  }
  document.getElementById('qc-picker-q').setAttribute('data-ob-qc-picker-entity', entityType);
  document.getElementById('qc-picker-list').innerHTML = '<div style="padding:16px;text-align:center;color:#94a3b8;font-size:13px">Загрузка...</div>';
  qcPickerSearch(entityType, '');
}

function qcPickerSearch(entityType, q) {
  var list = document.getElementById('qc-picker-list');
  if (!list) return;
  fetch('/ui/dev/entity-search?type=' + encodeURIComponent(entityType) + '&q=' + encodeURIComponent(q))
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var items = data.items || [];
      if (!items.length) {
        list.innerHTML = '<div style="padding:16px;text-align:center;color:#94a3b8;font-size:13px">Ничего не найдено</div>';
        return;
      }
      var html = '';
      items.forEach(function(item) {
        var id = String(item.id || '');
        var label = qcItemLabel(item);
        html += '<div class="qc-picker-item" data-id="' + escHtml(id) + '" data-label="' + escHtml(label) + '"'
          + ' style="padding:10px 16px;cursor:pointer;font-size:13px;border-bottom:1px solid #f8fafc;display:flex;gap:12px;align-items:center">';
        if (item.code != null) {
          html += '<span style="color:#94a3b8;font-size:11px;min-width:60px">' + escHtml(String(item.code)) + '</span>';
          html += '<span>' + escHtml(String(item.name || '')) + '</span>';
        } else {
          html += '<span>' + escHtml(String(item.name || id)) + '</span>';
        }
        html += '</div>';
      });
      list.innerHTML = html;
    }).catch(function() {
      list.innerHTML = '<div style="padding:16px;text-align:center;color:#ef4444;font-size:13px">Ошибка загрузки</div>';
    });
}

document.addEventListener('click', function(e) {
  var item = e.target.closest('.qc-picker-item');
  if (!item) return;
  if (_qcPickerRow) {
    qcFillRef(_qcPickerRow, item.dataset.id || '', item.dataset.label || '');
  }
  var modal = document.getElementById('qc-picker-modal');
  if (modal) modal.style.display = 'none';
});
</script>
</div></body></html>
{{end}}
`

const tplCodeConsole = `
{{define "page-code-console"}}
{{template "head" .}}{{template "nav" .}}
<main style="max-width:100%">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">
  <h2 style="margin:0">{{t $.Lang "Консоль кода"}}</h2>
  <div style="display:flex;gap:8px;align-items:center">
    <button data-ob-cc-action="exec" class="btn" style="background:#3b82f6;color:#fff;border:none;border-radius:6px;padding:6px 16px;cursor:pointer;font-size:13px">{{t $.Lang "▶ Выполнить"}}</button>
    <button data-ob-cc-action="clear" style="background:none;border:1px solid #e2e8f0;border-radius:6px;padding:6px 12px;cursor:pointer;font-size:13px;color:#64748b">{{t $.Lang "Очистить"}}</button>
  </div>
</div>

<div class="card" style="margin-bottom:12px">
<div id="cc-editor" style="height:320px;border:1px solid #e2e8f0;border-radius:6px"></div>
<textarea id="cc-textarea" style="display:none;width:100%;height:320px;font-family:'Cascadia Code',Consolas,monospace;font-size:13px;padding:10px;border:1px solid #e2e8f0;border-radius:6px;box-sizing:border-box;resize:vertical;tab-size:2">// Введите DSL-код
Сообщить("Привет из консоли!");
</textarea>
</div>

<div class="card">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Результат"}}</h3>
  <button data-ob-cc-action="clear-output" style="background:none;border:1px solid #e2e8f0;border-radius:4px;padding:2px 8px;cursor:pointer;font-size:12px;color:#64748b">{{t $.Lang "Очистить"}}</button>
</div>
<div id="cc-output" style="background:#1e293b;color:#e2e8f0;font-family:'Cascadia Code',Consolas,monospace;font-size:13px;padding:12px 16px;border-radius:6px;min-height:120px;max-height:400px;overflow-y:auto;white-space:pre-wrap;word-break:break-word"></div>
</div>

</main>

<script>
// Самохостинг Monaco: web-воркер из встроенного /vendor/monaco/ (тот же origin).
window.MonacoEnvironment = { getWorkerUrl: function () {
  return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(
    "self.MonacoEnvironment={baseUrl:'" + location.origin + "/vendor/monaco/'};" +
    "importScripts('" + location.origin + "/vendor/monaco/vs/base/worker/workerMain.js');");
}};
</script>
<script src="/vendor/monaco/vs/loader.js"
  onerror="document.getElementById('cc-editor').style.display='none';document.getElementById('cc-textarea').style.display='block'"></script>
<script>
if(typeof require !== 'undefined') {
  require.config({ paths: { 'vs': '/vendor/monaco/vs' }});
  require(['vs/editor/editor.main'], function() {
    monaco.languages.register({ id: 'onebase-dsl' });
    monaco.languages.setMonarchTokensProvider('onebase-dsl', {
      keywords: ['Процедура','КонецПроцедуры','Функция','КонецФункции','Если','Тогда','ИначеЕсли','Иначе','КонецЕсли','Для','Каждого','Из','Цикл','КонецЦикла','Пока','По','Прервать','Продолжить','Перем','Новый','Попытка','Исключение','КонецПопытки','ВызватьИсключение','Возврат','Экспорт','И','Или','Не','Null','Неопределено','Истина','Ложь'],
      builtin: ['Сообщить','Строка','Число','Окр','Мин','Макс','Формат','Найти','СтрДлина','Лев','Прав','Сред','ВРег','НРег','ТРег','СокрЛП','СокрЛ','СокрП','СтрНайти','СтрЗаменить','СтрРазделить','СтрСоединить','СтрШаблон','СтрНачинаетсяС','СтрЗаканчиваетсяНа','СтрЧислоВхождений','СтрЧислоСтрок','СтрПолучитьСтроку','СтрСравнить','КодСимвола','ПустаяСтрока','НСтр','Символ','ТипЗнч','Тип','ЗначениеЗаполнено','Год','Месяц','День','Час','Минута','Секунда','ДеньНедели','ДеньГода','НеделяГода','НачалоДня','КонецДня','НачалоМесяца','КонецМесяца','НачалоКвартала','КонецКвартала','НачалоГода','КонецГода','НачалоНедели','КонецНедели','НачалоЧаса','КонецЧаса','НачалоМинуты','КонецМинуты','ТекущаяДата','Дата','ДобавитьМесяц','ДобавитьДень','ДобавитьГод','РазностьДат','ЧислоПрописью','Pow','Sqrt','Exp','Log','Log10','Base64Строка','Base64Значение','ОписаниеОшибки','ИнформацияОбОшибке','ЗаполнитьЗначенияСвойств','НайтиПоРеквизиту','НайтиПоКоду','НайтиПоНаименованию','НайтиПоНомеру','ПолучитьОбъект','ЭтоНовый','Прочитать','Записать','Провести','Заполнить','РегистрыНакопления','Остатки','Движения','ВыбратьПоРегистратору','Вычислить','КопироватьФайл','ПереместитьФайл','НайтиФайлы','СоздатьКаталог','УдалитьФайлы','Запрос','Массив','ТаблицаЗначений','Соответствие','ЗаписьJSON','ЧтениеJSON'],
      tokenizer: {
        root: [
          [/\/\/.*$/, 'comment'],
          [/"[^"]*"/, 'string'],
          [/'[^']*'/, 'string'],
          [/\d+(\.\d+)?/, 'number'],
          [/&[А-Яа-яёЁA-Za-z_][А-Яа-яёЁA-Za-z_0-9]*/, 'variable.predefined'],
          [/[A-Za-zА-Яа-яёЁ_]\w*/, { cases: { '@keywords': 'keyword', '@builtin': 'type', '@default': 'identifier' } }],
        ]
      }
    });
    document.getElementById('cc-editor').style.display = 'block';
    document.getElementById('cc-textarea').style.display = 'none';
    window.ccEditor = monaco.editor.create(document.getElementById('cc-editor'), {
      value: '// Введите DSL-код\nСообщить("Привет из консоли!");\n',
      language: 'onebase-dsl',
      theme: 'vs',
      minimap: { enabled: false },
      automaticLayout: true,
      fontSize: 14,
      lineNumbers: 'on',
      scrollBeyondLastLine: false
    });
  });
}

function ccExec() {
  var code = window.ccEditor ? window.ccEditor.getValue() : document.getElementById('cc-textarea').value;
  var out = document.getElementById('cc-output');
  out.innerHTML += '<div style="color:#94a3b8;border-bottom:1px solid #334155;padding:2px 0;font-size:11px">--- ' + new Date().toLocaleTimeString() + ' ---</div>';
  fetch('/ui/dev/code-exec', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({code: code})
  }).then(function(r){ return r.json(); }).then(function(data) {
    if(data.output && data.output.length) {
      data.output.forEach(function(m) {
        out.innerHTML += '<div>' + escHtml(m) + '</div>';
      });
    }
    if(data.error) {
      out.innerHTML += '<div style="color:#fca5a5">' + escHtml(data.error) + '</div>';
    }
    if((!data.output || !data.output.length) && !data.error) {
      out.innerHTML += '<div style="color:#94a3b8">(без вывода)</div>';
    }
    out.scrollTop = out.scrollHeight;
  }).catch(function(e) {
    out.innerHTML += '<div style="color:#fca5a5">Ошибка сети: ' + e.message + '</div>';
  });
}

function ccClear() {
  if(window.ccEditor) window.ccEditor.setValue('// Введите DSL-код\n');
  document.getElementById('cc-output').innerHTML = '';
}

function ccRunAction(action) {
  if (action === 'exec') ccExec();
  else if (action === 'clear') ccClear();
  else if (action === 'clear-output') document.getElementById('cc-output').innerHTML = '';
}

document.addEventListener('click', function(e) {
  var btn = e.target.closest && e.target.closest('[data-ob-cc-action]');
  if (!btn) return;
  e.preventDefault();
  ccRunAction(btn.getAttribute('data-ob-cc-action') || '');
});

function escHtml(s) {
  var d = document.createElement('div'); d.textContent = s; return d.innerHTML;
}
</script>
</div></body></html>
{{end}}
`
