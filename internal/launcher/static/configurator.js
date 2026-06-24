// ── New object form ────────────────────────────────────────────
var _cfgNewTitles = {catalog:'Новый справочник', document:'Новый документ', register:'Новый регистр', inforeg:'Новый регистр сведений', accountreg:'Новый регистр бухгалтерии', enum:'Новое перечисление', subsystem:'Новая подсистема', widget:'Новый виджет', module:'Новый общий модуль', processor:'Новая обработка', page:'Новая страница'};
function cfgNewObj(kind) {
  if (kind === 'printform') { cfgNewPrintFormShow(); return; }
  // Вставляем форму сразу после кликнутой группы (+ кнопка)
  var addBtn = event && event.target;
  var group = addBtn && addBtn.closest('details');
  var f = document.getElementById('cfg-new-form');
  document.getElementById('cfg-new-title').textContent = _cfgNewTitles[kind] || 'Новый объект';
  document.getElementById('cfg-new-kind-inp').value = kind;
  document.getElementById('cfg-new-name').value = '';
  f.style.display = 'block';
  document.getElementById('cfg-new-form-pf').style.display = 'none';
  if (group && group.parentNode) {
    group.parentNode.insertBefore(f, group.nextSibling);
  }
  f.scrollIntoView({block:'nearest',behavior:'smooth'});
  document.getElementById('cfg-new-name').focus();
}
function cfgHideNew() {
  document.getElementById('cfg-new-form').style.display = 'none';
  document.getElementById('cfg-new-form-pf').style.display = 'none';
}
// obToggleLayout переключает пейны редактора раскладки: «Авто» (галочки) и
// «По рядам» (drag-конструктор).
function obToggleLayout(sel) {
  var form = sel.closest('form'); if (!form) return;
  var auto = form.querySelector('.ob-auto'), rows = form.querySelector('.ob-rows-pane');
  if (auto) auto.style.display = (sel.value === 'rows') ? 'none' : 'block';
  if (rows) rows.style.display = (sel.value === 'rows') ? 'block' : 'none';
}
// obHomeInit инициализирует drag-конструктор рядов для одной формы редактора.
function obHomeInit(form) {
  var key = form.getAttribute('data-home-key'); if (!key) return;
  var rowsWrap = form.querySelector('.ob-rows');
  var pool = form.querySelector('.ob-pool');
  var hidden = form.querySelector('input[name="home_rows"]');
  if (!rowsWrap || !pool || !hidden) return;
  var data = (window.__homeData && window.__homeData[key]) || {rows: []};
  var widgets = window.__homeWidgets || [];
  var titleByName = {}; widgets.forEach(function (w) { titleByName[w.name] = w.title || w.name; });

  function chip(name) {
    var c = document.createElement('span');
    c.className = 'ob-chip'; c.setAttribute('draggable', 'true'); c.dataset.name = name;
    c.textContent = titleByName[name] || name;
    c.addEventListener('dragstart', function () { window.__obDrag = c; setTimeout(function(){ c.classList.add('dragging'); }, 0); });
    c.addEventListener('dragend', function () { c.classList.remove('dragging'); window.__obDrag = null; });
    return c;
  }
  function afterElement(zone, x) {
    var els = Array.prototype.slice.call(zone.querySelectorAll('.ob-chip:not(.dragging)'));
    for (var i = 0; i < els.length; i++) {
      var r = els[i].getBoundingClientRect();
      if (x < r.left + r.width / 2) return els[i];
    }
    return null;
  }
  function bindZone(zone) {
    zone.addEventListener('dragover', function (e) {
      e.preventDefault();
      var dr = window.__obDrag; if (!dr) return;
      var after = afterElement(zone, e.clientX);
      if (after == null) zone.appendChild(dr); else zone.insertBefore(dr, after);
    });
  }
  function makeRow(names) {
    var row = document.createElement('div'); row.className = 'ob-row';
    var zone = document.createElement('div'); zone.className = 'ob-zone';
    (names || []).forEach(function (n) { zone.appendChild(chip(n)); });
    bindZone(zone);
    var del = document.createElement('button');
    del.type = 'button'; del.className = 'ob-row-del'; del.textContent = '✕'; del.title = 'Удалить ряд';
    del.addEventListener('click', function () {
      Array.prototype.slice.call(zone.children).forEach(function (c) { pool.appendChild(c); });
      row.remove();
    });
    row.appendChild(zone); row.appendChild(del);
    return row;
  }

  bindZone(pool);
  rowsWrap.innerHTML = '';
  var used = {};
  (data.rows || []).forEach(function (names) {
    rowsWrap.appendChild(makeRow(names));
    (names || []).forEach(function (n) { used[n] = 1; });
  });
  pool.innerHTML = '';
  widgets.forEach(function (w) { if (!used[w.name]) pool.appendChild(chip(w.name)); });

  var addBtn = form.querySelector('.ob-add-row');
  if (addBtn) addBtn.addEventListener('click', function () { rowsWrap.appendChild(makeRow([])); });

  form.addEventListener('submit', function () {
    var sel = form.querySelector('select[name="home_layout"]');
    if (!sel || sel.value !== 'rows') return; // в «Авто» сохраняются галочки
    var out = [];
    Array.prototype.slice.call(rowsWrap.querySelectorAll('.ob-zone')).forEach(function (zone) {
      var names = Array.prototype.slice.call(zone.querySelectorAll('.ob-chip')).map(function (c) { return c.dataset.name; });
      if (names.length) out.push(names);
    });
    hidden.value = JSON.stringify(out);
  });
}
document.addEventListener('DOMContentLoaded', function () {
  Array.prototype.slice.call(document.querySelectorAll('form[data-home-key]')).forEach(obHomeInit);
});
function cfgNewPrintFormShow() {
  document.getElementById('cfg-new-form').style.display = 'none';
  document.getElementById('cfg-new-form-pf').style.display = 'block';
  document.getElementById('cfg-new-pf-name').value = '';
  document.getElementById('cfg-new-pf-name').focus();
}
// _cfgSubmitForm создаёт скрытую POST-форму и отправляет её (план 64, 6.4).
function _cfgSubmitForm(action, fields) {
  var f = document.createElement('form');
  f.method = 'POST'; f.action = action; f.style.display = 'none';
  for (var k in fields) {
    if (!Object.prototype.hasOwnProperty.call(fields, k)) continue;
    var inp = document.createElement('input');
    inp.type = 'hidden'; inp.name = k; inp.value = fields[k];
    f.appendChild(inp);
  }
  document.body.appendChild(f); f.submit();
}
// cfgNewLayout — «+ Печатная форма (макет)» у сущности: спрашивает имя и создаёт
// декларативный макет со скелетом по первой ТЧ.
function cfgNewLayout(action, entity) {
  var name = prompt(T("Имя печатной формы (макета):"));
  if (!name) return;
  _cfgSubmitForm(action, {entity: entity, name: name});
}
// cfgCreateOSLayout — «Создать макет» у DSL-формы без макета.
function cfgCreateOSLayout(action, osform) {
  _cfgSubmitForm(action, {osform: osform});
}
// cfgImportPdfLayout — «Создать макет из PDF»: модальный диалог (имя + файл +
// № страницы), затем multipart-POST на import-pdf. После импорта сервер
// открывает редактор макета (план 64, этап 6).
function cfgImportPdfLayout(action) {
  var overlay = document.createElement('div');
  overlay.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:9999;display:flex;align-items:center;justify-content:center';
  var box = document.createElement('div');
  box.style.cssText = 'background:#fff;border-radius:8px;padding:20px;min-width:360px;max-width:90vw;box-shadow:0 8px 32px rgba(0,0,0,.3);font-size:13px';
  box.innerHTML =
    '<div style="font-weight:600;font-size:15px;margin-bottom:12px">'+T("Создать макет из PDF")+'</div>' +
    '<div style="color:#64748b;font-size:12px;margin-bottom:12px">'+T("Подходит вектор с текстовым слоем (выгрузка из 1С/Excel). Сканы импортировать нельзя. Черновик нужно доработать в редакторе.")+'</div>' +
    '<label style="display:block;margin-bottom:8px">'+T("Имя макета")+'<br><input type="text" id="_pdfName" style="width:100%;padding:6px;border:1px solid #cbd5e1;border-radius:4px;box-sizing:border-box"></label>' +
    '<label style="display:block;margin-bottom:8px">'+T("PDF-файл")+'<br><input type="file" id="_pdfFile" accept="application/pdf,.pdf" style="width:100%"></label>' +
    '<label style="display:block;margin-bottom:14px">'+T("Номер страницы")+'<br><input type="number" id="_pdfPage" value="1" min="1" style="width:80px;padding:6px;border:1px solid #cbd5e1;border-radius:4px"></label>' +
    '<div id="_pdfErr" style="color:#dc2626;font-size:12px;margin-bottom:8px;display:none"></div>' +
    '<div style="text-align:right">' +
    '<button type="button" id="_pdfCancel" style="padding:6px 14px;margin-right:8px;background:#e2e8f0;border:none;border-radius:4px;cursor:pointer">'+T("Отмена")+'</button>' +
    '<button type="button" id="_pdfOk" style="padding:6px 14px;background:#0369a1;color:#fff;border:none;border-radius:4px;cursor:pointer">'+T("Импортировать")+'</button>' +
    '</div>';
  overlay.appendChild(box);
  document.body.appendChild(overlay);
  function close() { document.body.removeChild(overlay); }
  document.getElementById('_pdfCancel').onclick = close;
  overlay.onclick = function(e){ if (e.target === overlay) close(); };
  var okBtn = document.getElementById('_pdfOk');
  okBtn.onclick = function() {
    var name = (document.getElementById('_pdfName').value || '').trim();
    var fileInp = document.getElementById('_pdfFile');
    var page = document.getElementById('_pdfPage').value || '1';
    var err = document.getElementById('_pdfErr');
    err.style.display = 'none';
    if (!name) { err.textContent = T("Имя макета обязательно"); err.style.display = ''; return; }
    if (!fileInp.files || !fileInp.files[0]) { err.textContent = T("Выберите PDF-файл"); err.style.display = ''; return; }
    var fd = new FormData();
    fd.append('file', fileInp.files[0]);
    fd.append('name', name);
    fd.append('page', page);
    okBtn.disabled = true; okBtn.textContent = T("Импорт...");
    fetch(action, {method:'POST', body:fd})
      .then(function(resp){ return resp.text().then(function(html){ return {ok:resp.ok, html:html}; }); })
      .then(function(res){
        // Сервер всегда возвращает полную страницу конфигуратора (успех — с
        // открытым редактором, ошибка — с баннером). Заменяем документ целиком.
        document.open(); document.write(res.html); document.close();
      })
      .catch(function(e){
        okBtn.disabled = false; okBtn.textContent = T("Импортировать");
        err.textContent = String(e); err.style.display = '';
      });
  };
}

// ── Folder picker ──────────────────────────────────────────────
function pickDir(inputId, title) {
  var btn = event.target;
  var cur = document.getElementById(inputId).value || '';
  btn.disabled = true;
  btn.textContent = '...';
  fetch('/browse-dir?title=' + encodeURIComponent(title) + '&initial_path=' + encodeURIComponent(cur))
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d.path) document.getElementById(inputId).value = d.path;
    })
    .finally(function(){ btn.disabled = false; btn.textContent = '\u{1F4C1}'; });
}

// ── Logo upload helpers ──────────────────────────────────────────
function previewLogo(input) {
  if (input.files && input.files[0]) {
    var reader = new FileReader();
    reader.onload = function(e) {
      var img = document.getElementById('logo-preview');
      img.src = e.target.result;
      img.style.display = '';
    };
    reader.readAsDataURL(input.files[0]);
  }
}
function removeLogo() {
  var img = document.getElementById('logo-preview');
  if (img) { img.src = ''; img.style.display = 'none'; }
  document.getElementById('logo-remove').value = '1';
  var fileInput = document.querySelector('input[name="app_logo_file"]');
  if (fileInput) fileInput.value = '';
}

// ── Reference / enum picker toggle ───────────────────────────────
// Один и тот же select-target используется и для reference, и для enum:
// при смене типа JS меняет options между списком сущностей и списком
// перечислений. Сохраняем выбранное значение если оно есть в новой
// группе options.
var _cfgEntityNames = window.__cfg.entityNames;
var _cfgEnumNames = window.__cfg.enumNames;
function cfgToggleRef(sel, refId) {
  var r = document.getElementById(refId);
  if (!r) return;
  if (sel.value === 'reference' || sel.value === 'enum') {
    r.style.display = '';
    var src = sel.value === 'enum' ? _cfgEnumNames : _cfgEntityNames;
    var cur = r.value;
    var keep = false;
    var html = '<option value="">'+T("— выбрать —")+'</option>';
    for (var i = 0; i < src.length; i++) {
      var n = src[i];
      var sel2 = (n === cur) ? ' selected' : '';
      if (n === cur) keep = true;
      html += '<option value="' + n + '"' + sel2 + '>' + n + '</option>';
    }
    r.innerHTML = html;
    if (!keep) r.value = '';
  } else {
    r.style.display = 'none';
  }
}
// cfgToggleNum показывает поля «Длина, Точность» только для типа «число».
function cfgToggleNum(sel, numId) {
  var n = document.getElementById(numId);
  if (!n) return;
  n.style.display = (sel.value === 'number') ? '' : 'none';
}
var _cfgNewFieldIdx = 0;
function cfgAddField(tblId, prefix, entityName) {
  _cfgNewFieldIdx++;
  var tbl = document.getElementById(tblId);
  if (!tbl) return;
  var refId = 'cfr-'+entityName+'-nf'+_cfgNewFieldIdx;
  var numId = 'cfn-'+entityName+'-nf'+_cfgNewFieldIdx;
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input name="'+prefix+'.'+_cfgNewFieldIdx+'.name" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px" placeholder="ИмяПоля"></td>'
    +'<td><select name="'+prefix+'.'+_cfgNewFieldIdx+'.type" onchange="cfgToggleRef(this,\''+refId+'\');cfgToggleNum(this,\''+numId+'\')">'
    +'<option value="string">'+T("строка")+'</option><option value="number">'+T("число")+'</option><option value="date">'+T("дата")+'</option><option value="bool">'+T("булево")+'</option><option value="reference">'+T("ссылка →")+'</option><option value="enum">'+T("перечисление →")+'</option>'
    +'</select>'
    +' <span id="'+numId+'" style="display:none" title="'+T("Длина, Точность")+'">'
    +'<input type="number" min="1" name="'+prefix+'.'+_cfgNewFieldIdx+'.length" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">'
    +' , <input type="number" min="0" name="'+prefix+'.'+_cfgNewFieldIdx+'.scale" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">'
    +'</span></td>'
    +'<td><select name="'+prefix+'.'+_cfgNewFieldIdx+'.ref" id="'+refId+'" style="display:none">'
    +'<option value="">'+T("— выбрать —")+'</option>'
    +'</select></td>';
  tbl.appendChild(tr);
  tr.querySelector('input').focus();
}
var _cfgNewTpIdx = 0;
function cfgAddTP(btn, entityName) {
  _cfgNewTpIdx++;
  var tpName = prompt('Имя табличной части:');
  if (!tpName) return;
  var wrapper = document.createElement('details');
  wrapper.open = true;
  var tblId = 'ft-'+entityName+'-ntp'+_cfgNewTpIdx;
  // Поля новой ТЧ шлём под ИМЕННЫМ префиксом new_tp.<Имя>.field — тем же, что и
  // «+ Добавить поле» у существующих ТЧ, чтобы их читал единый серверный цикл.
  var fldPrefix = 'new_tp.'+tpName+'.field';
  wrapper.innerHTML = '<summary class="section-hd" style="cursor:pointer">📋 '+tpName+' (0)</summary>'
    +'<input type="hidden" name="new_tp_name" value="'+tpName+'">'
    +'<div class="tp-block"><table class="fields-tbl" id="'+tblId+'">'
    +'<tr><th>'+T("Поле")+'</th><th>'+T("Тип")+'</th><th style="min-width:150px">'+T("Объект")+'</th></tr>'
    +'</table>'
    +'<button type="button" onclick="cfgAddField(\''+tblId+'\',\''+fldPrefix+'\',\''+entityName+'\')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ '+T("Добавить поле")+'</button>'
    +'</div>';
  btn.parentNode.insertBefore(wrapper, btn);
  cfgAddField(tblId, fldPrefix, entityName);
}
// ── Predefined items row ──────────────────────────────────────────
var _cfgPreRowIdx = 10000;
function cfgAddPreRow(tblId, fieldCount) {
  _cfgPreRowIdx++;
  var tbl = document.getElementById(tblId);
  if (!tbl) { tbl = document.querySelector('[id="'+tblId+'"]'); }
  if (!tbl) return;
  var idx = _cfgPreRowIdx;
  // read column headers to get field names
  var headers = tbl.querySelectorAll('th');
  var tr = document.createElement('tr');
  var html = '<td><input type="text" name="pre.'+idx+'.name" placeholder="ИмяЭлемента" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>';
  for (var i = 1; i < headers.length; i++) {
    var fn = headers[i].textContent.trim();
    html += '<td><input type="text" name="pre.'+idx+'.field.'+fn+'" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>';
  }
  tr.innerHTML = html;
  tbl.appendChild(tr);
  tr.querySelector('input').focus();
}
// ── AccountReg resource row ───────────────────────────────────────
var _cfgARResIdx = 0;
function cfgAddARField(tblId) {
  _cfgARResIdx++;
  var tbl = document.getElementById(tblId);
  if (!tbl) return;
  tbl.style.display = '';
  var idx = tbl.querySelectorAll('tr').length - 1;
  var numId = 'arn-new-'+idx;
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input type="text" name="res.'+idx+'.name" placeholder="ИмяРесурса" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>'
    +'<td><select name="res.'+idx+'.type" onchange="cfgToggleNum(this,\''+numId+'\')"><option value="number">'+T("число")+'</option><option value="string">'+T("строка")+'</option><option value="bool">'+T("булево")+'</option></select>'
    +' <span id="'+numId+'" title="'+T("Длина, Точность")+'">'
    +'<input type="number" min="1" name="res.'+idx+'.length" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">'
    +' , <input type="number" min="0" name="res.'+idx+'.scale" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">'
    +'</span></td>';
  tbl.appendChild(tr);
  tr.querySelector('input').focus();
}
// ── Click-to-edit module (Monaco with textarea fallback) ─────────
var monacoEditors = {};
window._monacoReady = false;
window.onerror = function(msg, url, ln, col, err) {
  var d = document.getElementById('dbg-diag');
  if (d) d.innerHTML = '<div style="color:#ef4444;font-size:10px;background:#1e1e2e;padding:4px">JS ERROR: ' + msg + ' (line ' + ln + ')</div>' + d.innerHTML;
  return false;
};

function startEdit(name) {
  var pre = document.getElementById('pre-'+name);
  var ta  = document.getElementById('ta-'+name);
  if (!pre || !ta) return;
  if (window._monacoReady && typeof monaco !== 'undefined') {
    // Monaco path: create editor inside the code-wrap container
    var wrap = pre.parentNode;
    if (!wrap || wrap.querySelector('.monaco-target')) return; // already active
    var lang = 'onebase-dsl';
    if (name.indexOf('rep-') === 0) lang = 'onebase-query';
    if (name.indexOf('pf-') === 0) lang = 'yaml';
    ta.value = pre.textContent;
    pre.style.display = 'none';
    var div = document.createElement('div');
    div.className = 'monaco-target';
    div._editorId = name;
    wrap.appendChild(div);
    var langId = (lang === 'yaml') ? 'yaml' : 'onebase-dsl';
    var editor = monaco.editor.create(div, {
      value: ta.value,
      language: langId,
      theme: 'onebase-dark',
      minimap: { enabled: false },
      fontSize: 12,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      wordWrap: 'on',
      tabSize: 2,
      glyphMargin: true,
      automaticLayout: true,
      contextmenu: false // используем своё контекстное меню (см. document-level handler)
    });
    editor._fileId = name;
    monacoEditors[name] = editor;
    window._lastFocusedEditorId = window._lastFocusedEditorId || name;
    editor.onDidFocusEditorText(function(){ window._lastFocusedEditorId = name; });
    // Запоминаем последнее непустое выделение: правый клик в Monaco
    // сбрасывает selection раньше, чем открывается конструктор запроса,
    // поэтому в openQBModalMonaco текущее выделение уже пустое.
    editor._lastSelText = '';
    editor.onDidChangeCursorSelection(function(ev) {
      var t = editor.getModel().getValueInRange(ev.selection);
      if (t && t.trim()) editor._lastSelText = t;
    });
    // Override F10/F11 inside Monaco so debugger shortcuts work even when editor has focus.
    // Monaco intercepts these via its internal keybinding manager before the DOM event
    // reaches our document-level capture handler.
    editor.addCommand(monaco.KeyCode.F10, function() { dbgStep('over'); });
    editor.addCommand(monaco.KeyCode.F11, function() { dbgStep('into'); });
    editor.addCommand(monaco.KeyMod.Shift | monaco.KeyCode.F11, function() { dbgStep('out'); });
    // Gutter click for breakpoint toggle — setTimeout decouples from Monaco internals
    editor.onMouseDown(function(e) {
      try {
        var tgt = e.target;
        var isGutter = tgt && (tgt.type === monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN
          || tgt.type === monaco.editor.MouseTargetType.GUTTER_LINE_NUMBERS
          || tgt.type === monaco.editor.MouseTargetType.GUTTER_LINE_DECORATIONS);
        if (!isGutter) return;
        var line = tgt.position ? tgt.position.lineNumber : (tgt.range ? tgt.range.startLineNumber : 0);
        if (line) {
          var n = name, l = line;
          setTimeout(function(){ dbgToggleBreakpoint(n, l); }, 0);
        }
      } catch(err) { /* ignore Monaco internal errors */ }
    });
  } else {
    // Fallback: original textarea pattern
    if (ta.style.display !== 'none' && ta.style.display !== '') return;
    ta.value = pre.textContent;
    pre.style.display = 'none';
    ta.style.display = 'block';
    ta.focus();
  }
}

function endEdit(name) {
  var pre = document.getElementById('pre-'+name);
  var ta  = document.getElementById('ta-'+name);
  // Sync Monaco content back to textarea if present
  if (monacoEditors[name]) {
    ta.value = monacoEditors[name].getValue();
    monacoEditors[name].dispose();
    delete monacoEditors[name];
    var wrap = pre.parentNode;
    var mDiv = wrap.querySelector('.monaco-target');
    if (mDiv) mDiv.remove();
  }
  if (pre && ta) {
    pre.innerHTML = hl(ta.value);
    pre.style.display = '';
    ta.style.display = 'none';
  }
}
// ── Syntax check ──────────────────────────────────────────────────
// jumpToError ставит курсор Monaco-редактора в координаты ошибки синтаксического
// контроля и прокручивает к ней (issue #103). key — тот же, что и у runCheck.
function jumpToError(key, line, col) {
  var ed = (typeof monacoEditors !== 'undefined') ? monacoEditors[key] : null;
  if (ed) {
    ed.revealLineInCenter(line);
    ed.setPosition({lineNumber: line, column: col || 1});
    ed.focus();
  }
  return false;
}

// runCheck reads code from a textarea (id "ta-<key>") and posts it to the
// configurator check endpoint, then renders the result in the sibling
// .check-result element with id "check-<key>". The kind argument selects the
// validator: dsl | widget | home_page | entity.
function runCheck(kind, key, name) {
  var ta = document.getElementById('ta-' + key);
  var result = document.getElementById('check-' + key);
  if (!ta || !result) return;
  // Sync Monaco editor content back to textarea if present
  if (typeof monacoEditors !== 'undefined' && monacoEditors[key]) {
    ta.value = monacoEditors[key].getValue();
  }
  // URLSearchParams sets Content-Type to application/x-www-form-urlencoded,
  // which Go's r.ParseForm() can decode. FormData would force multipart and
  // require r.ParseMultipartForm() on the server.
  var body = new URLSearchParams();
  body.set('kind', kind);
  body.set('source', ta.value);
  if (name) body.set('name', name);
  result.className = 'check-result check-pending';
  result.textContent = '⏳ Проверка... (' + ta.value.length + ' симв.)';
  fetch('/bases/' + _dbgBase + '/configurator/check', {
    method: 'POST',
    headers: {'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8'},
    body: body.toString()
  })
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d.error) {
        result.className = 'check-result check-err';
        result.textContent = '⚠ ' + d.error;
        return;
      }
      if (d.ok) {
        result.className = 'check-result check-ok';
        result.textContent = '✓ Синтаксис ОК';
      } else {
        result.className = 'check-result check-err';
        var lines = (d.issues || []).map(function(i){
          var txt = escapeHtml('• ' + i.message);
          if (i.line) {
            var pos = ' (стр. ' + i.line + (i.column ? ', поз. ' + i.column : '') + ')';
            return '<a href="#" onclick="return jumpToError(\'' + key + '\',' + i.line + ',' + (i.column || 1) + ')" style="color:inherit;text-decoration:underline;cursor:pointer">' + txt + escapeHtml(pos) + '</a>';
          }
          return txt;
        });
        result.innerHTML = '<b>Найдено ошибок: ' + d.total + '</b><br>' + lines.join('<br>');
      }
    })
    .catch(function(e){
      result.className = 'check-result check-err';
      result.textContent = '⚠ ' + e.message;
    });
}

// ── Виджеты: предпросмотр данных и структурная карта (ось X / серии) ──
function _wdgEsc(s){
  return String(s == null ? '' : s).replace(/[&<>"]/g, function(c){
    return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c];
  });
}
function _wdgTa(name){ return document.getElementById('ta-wdg-' + name); }

function previewWidget(name){
  var ta = _wdgTa(name);
  var out = document.getElementById('wdg-preview-' + name);
  if (!ta || !out) return;
  if (typeof monacoEditors !== 'undefined' && monacoEditors['wdg-' + name]) {
    ta.value = monacoEditors['wdg-' + name].getValue();
  }
  out.style.display = 'block';
  out.innerHTML = '<div class="hint">⏳ Прогон виджета по данным базы…</div>';
  var body = new URLSearchParams();
  body.set('yaml', ta.value);
  fetch('/bases/' + _dbgBase + '/configurator/widget-preview', {
    method: 'POST',
    headers: {'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8'},
    body: body.toString()
  })
    .then(function(r){ return r.json(); })
    .then(function(d){ renderWidgetPreview(name, d); })
    .catch(function(e){ out.innerHTML = '<div class="wdg-err">⚠ ' + _wdgEsc(e.message) + '</div>'; });
}

function renderWidgetPreview(name, d){
  var out = document.getElementById('wdg-preview-' + name);
  if (!out) return;
  if (d.error){ out.innerHTML = '<div class="wdg-err">⚠ ' + _wdgEsc(d.error) + '</div>'; return; }
  var html = '';
  if (d.mapping) html += '<div class="wdg-mapping">🧭 ' + _wdgEsc(d.mapping) + '</div>';
  var chartId = 'wdg-echart-' + name;
  if (d.echarts_option) html += '<div id="' + chartId + '" class="wdg-echart"></div>';
  if (d.columns && d.columns.length){
    html += '<table class="wdg-table"><thead><tr>';
    d.columns.forEach(function(c){ html += '<th>' + _wdgEsc(c) + '</th>'; });
    html += '</tr></thead><tbody>';
    (d.rows || []).forEach(function(row){
      html += '<tr>';
      row.forEach(function(cell){ html += '<td>' + _wdgEsc(cell) + '</td>'; });
      html += '</tr>';
    });
    html += '</tbody></table>';
    if (!(d.rows || []).length) html += '<div class="hint">Запрос вернул 0 строк.</div>';
  }
  out.innerHTML = html || '<div class="hint">Нет данных для предпросмотра.</div>';
  // Рисуем тем же ECharts и той же опцией, что и рабочий стол — предпросмотр
  // совпадает с тем, что увидит пользователь.
  if (d.echarts_option){
    var el = document.getElementById(chartId);
    if (el && window.echarts){
      echarts.init(el).setOption(d.echarts_option);
    } else if (el){
      el.innerHTML = '<div class="hint">График: библиотека ECharts недоступна.</div>';
    }
  }
  _wdgPopulateMap(name, d);
}

// _wdgPopulateMap заполняет селекты «Ось X» и чекбоксы серий колонками запроса,
// предвыбирая текущие значения из YAML. Только для графиков.
function _wdgPopulateMap(name, d){
  var ctl = document.getElementById('wdg-ctl-' + name);
  if (!ctl) return;
  if (d.type !== 'chart' || !d.columns || !d.columns.length){ ctl.style.display = 'none'; return; }
  ctl.style.display = 'block';
  var yaml = (_wdgTa(name) || {}).value || '';
  var curKind = _wdgYamlGet(yaml, 'chart_kind') || (d.chart ? d.chart.kind : 'bar');
  var curX = _wdgYamlGet(yaml, 'x_field');
  var curY = _wdgYamlGetList(yaml, 'y_fields');

  var kindSel = document.getElementById('wdg-kind-' + name);
  if (kindSel) kindSel.value = curKind;

  var xSel = document.getElementById('wdg-x-' + name);
  if (xSel){
    xSel.innerHTML = '';
    d.columns.forEach(function(c){
      var o = document.createElement('option');
      o.value = c; o.textContent = c;
      if (c === curX) o.selected = true;
      xSel.appendChild(o);
    });
  }
  var yBox = document.getElementById('wdg-y-' + name);
  if (yBox){
    yBox.innerHTML = '';
    d.columns.forEach(function(c){
      var checked = curY.indexOf(c) >= 0 ? ' checked' : '';
      var lbl = document.createElement('label');
      lbl.className = 'yopt';
      lbl.innerHTML = '<input type="checkbox" value="' + _wdgEsc(c) + '"' + checked + ' onchange="applyWidgetMapping(\'' + name + '\')"> ' + _wdgEsc(c);
      yBox.appendChild(lbl);
    });
  }
}

// applyWidgetMapping записывает выбор из контролов обратно в YAML (точечно
// правит строки chart_kind/x_field/y_fields, остальное не трогает).
function applyWidgetMapping(name){
  var ta = _wdgTa(name);
  if (!ta) return;
  var kind = (document.getElementById('wdg-kind-' + name) || {}).value || 'bar';
  var x = (document.getElementById('wdg-x-' + name) || {}).value || '';
  var ys = [];
  document.querySelectorAll('#wdg-y-' + name + ' input:checked').forEach(function(cb){ ys.push(cb.value); });
  var yaml = ta.value;
  yaml = _wdgYamlSet(yaml, 'chart_kind', kind);
  yaml = _wdgYamlSet(yaml, 'x_field', x);
  yaml = _wdgYamlSet(yaml, 'y_fields', '[' + ys.join(', ') + ']');
  ta.value = yaml;
  if (typeof monacoEditors !== 'undefined' && monacoEditors['wdg-' + name]) {
    monacoEditors['wdg-' + name].setValue(yaml);
  }
}

function _wdgYamlGet(yaml, key){
  var m = yaml.match(new RegExp('^' + key + ':[ \\t]*(.+)$', 'm'));
  return m ? m[1].trim() : '';
}
function _wdgYamlGetList(yaml, key){
  var raw = _wdgYamlGet(yaml, key);
  if (!raw) return [];
  raw = raw.replace(/^\[|\]$/g, '');
  return raw.split(',').map(function(s){ return s.trim(); }).filter(Boolean);
}
function _wdgYamlSet(yaml, key, val){
  var re = new RegExp('^' + key + ':.*$', 'm');
  if (re.test(yaml)) return yaml.replace(re, key + ': ' + val);
  if (yaml.length && yaml[yaml.length - 1] !== '\n') yaml += '\n';
  return yaml + key + ': ' + val + '\n';
}

function runCheckAll() {
  var btn = document.getElementById('btn-check-all');
  var panel = document.getElementById('check-all-panel');
  var body = document.getElementById('check-all-body');
  var explainBtn = document.getElementById('check-all-explain-btn');
  var explainOut = document.getElementById('check-all-explain-out');
  if (explainBtn) explainBtn.style.display = 'none';
  if (explainOut) { explainOut.style.display = 'none'; explainOut.textContent = ''; }
  btn.disabled = true;
  btn.textContent = 'Проверка...';
  body.innerHTML = '<div style="padding:10px;color:#888">⏳ Идёт проверка конфигурации...</div>';
  panel.style.display = 'flex';
  fetch('/bases/' + _dbgBase + '/configurator/check-all', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      btn.disabled = false;
      btn.textContent = 'Проверить конфигурацию';
      if (d.error) {
        body.innerHTML = '<div class="check-row check-err">⚠ ' + d.error + '</div>';
        return;
      }
      var renderIssueRow = function(i, isWarn) {
        var html = '<div class="check-row">';
        if (i.kind || i.object) {
          html += '<div style="font-size:11px;color:#888;text-transform:uppercase;letter-spacing:.04em">' +
                  (i.kind || '') + (i.object ? ' · ' + i.object : '') + '</div>';
        }
        var color = isWarn ? '#92400e' : '#c00';
        var prefix = isWarn ? '[предупреждение] ' : '';
        html += '<div style="color:' + color + '">' + prefix + escapeHtml(i.message) + '</div>';
        if (i.file) {
          html += '<div style="font-size:10px;color:#aaa;font-family:Consolas,monospace">' + i.file +
                  (i.line ? ':' + i.line : '') + '</div>';
        }
        html += '</div>';
        return html;
      };
      if (d.ok) {
        var warnCount = (d.warnings || []).length;
        var okMsg = warnCount > 0
          ? '<b>✓ Конфигурация корректна</b><br>Ошибок не найдено (' + warnCount + ' предупреждений).'
          : '<b>✓ Конфигурация корректна</b><br>Ошибок не найдено.';
        var warnHtml = '';
        (d.warnings || []).forEach(function(i){ warnHtml += renderIssueRow(i, true); });
        body.innerHTML = '<div class="check-row check-ok">' + okMsg + '</div>' + warnHtml;
        return;
      }
      var html = '<div class="check-row check-err" style="font-weight:600">Найдено ошибок: ' + d.total + '</div>';
      (d.issues || []).forEach(function(i){ html += renderIssueRow(i, false); });
      (d.warnings || []).forEach(function(i){ html += renderIssueRow(i, true); });
      body.innerHTML = html;
      if (explainBtn) explainBtn.style.display = '';
    })
    .catch(function(e){
      btn.disabled = false;
      btn.textContent = 'Проверить конфигурацию';
      body.innerHTML = '<div class="check-row check-err">⚠ ' + e.message + '</div>';
    });
}

function closeCheckAll() {
  document.getElementById('check-all-panel').style.display = 'none';
}
function explainCheckErrors(btn){
  var body=document.getElementById('check-all-body');
  var out=document.getElementById('check-all-explain-out');
  var text=body?body.innerText.trim():'';
  if(!text){return;}
  if(btn){btn.disabled=true;}
  out.style.display='';out.textContent='Объясняю...';out.style.color='#888';
  fetch('/bases/'+_dbgBase+'/configurator/ai-explain',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:text})})
    .then(function(r){return r.json();})
    .then(function(d){
      if(d&&d.ok){out.textContent=d.text;out.style.color='#1e293b';}
      else{out.textContent=(d&&d.error)||'Ошибка';out.style.color='#c00';}
    })
    .catch(function(){out.textContent='Ошибка сети';out.style.color='#c00';})
    .finally(function(){if(btn){btn.disabled=false;}});
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, function(c){
    return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];
  });
}

// ── Form field reorder ──────────────────────────────────────────
function moveUp(btn){var row=btn.closest('.form-field-row'),prev=row.previousElementSibling;if(prev&&prev.classList.contains('form-field-row'))row.parentNode.insertBefore(row,prev);}
function moveDown(btn){var row=btn.closest('.form-field-row'),next=row.nextElementSibling;if(next&&next.classList.contains('form-field-row'))row.parentNode.insertBefore(next,row);}
// ── Form tabs (outside module-editor-wrap) ────────────────────
function formTab(el,showId,hideId){
  var tabs=el.parentNode;
  tabs.querySelectorAll('.module-tab').forEach(function(t){t.classList.remove('active')});
  el.classList.add('active');
  document.getElementById(showId).classList.add('active');
  document.getElementById(hideId).classList.remove('active');
}
// ── Panel selection ────────────────────────────────────────────
function toggleSidebar() {
  var sb = document.getElementById('cfg-sidebar');
  var btn = document.getElementById('sidebar-toggle');
  sb.classList.toggle('collapsed');
  btn.classList.toggle('collapsed');
  btn.textContent = sb.classList.contains('collapsed') ? '▶' : '◀';
}
function selItem(el) {
  document.querySelectorAll('.cfg-item').forEach(function(e){e.classList.remove('sel')});
  document.querySelectorAll('.cfg-panel').forEach(function(e){e.classList.remove('active')});
  el.classList.add('sel');
  var panel = document.getElementById(el.dataset.id);
  if (panel) panel.classList.add('active');
}
// Context menu for tree items
document.addEventListener('contextmenu', function(ev) {
  var item = ev.target.closest('.cfg-item');
  if (!item) return;
  var did = item.dataset.id || '';
  if (did.indexOf('e-') !== 0 && did.indexOf('r-') !== 0 && did.indexOf('ir-') !== 0 && did.indexOf('ar-') !== 0 &&
      did.indexOf('en-') !== 0 && did.indexOf('rep-') !== 0 && did.indexOf('mod-') !== 0 &&
      did.indexOf('proc-') !== 0 && did.indexOf('pf-') !== 0 && did.indexOf('sub-') !== 0) return;
  ev.preventDefault();
  selItem(item);
  var existing = document.getElementById('cfg-ctx-menu');
  if (existing) existing.remove();
  var menu = document.createElement('div');
  menu.id = 'cfg-ctx-menu';
  menu.style.cssText = 'position:fixed;left:'+ev.clientX+'px;top:'+ev.clientY+'px;background:#fff;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.2);padding:4px 0;z-index:9999;min-width:150px';
  var dname = did.replace(/^[a-z]+-/, '');
  var delBtn = document.createElement('div');
  delBtn.textContent = 'Удалить «' + dname + '»';
  delBtn.style.cssText = 'padding:8px 14px;cursor:pointer;font-size:13px;color:#dc2626';
  delBtn.onmouseover = function(){this.style.background='#fef2f2'};
  delBtn.onmouseout = function(){this.style.background=''};
  delBtn.onclick = function(){
    menu.remove();
    if (!confirm('Удалить «' + dname + '»? Это действие необратимо!')) return;
    var form = document.createElement('form');
    form.method = 'POST';
    form.action = '/bases/' + _dbgBase + '/configurator/entity-delete';
    var inp = document.createElement('input'); inp.type='hidden'; inp.name='entity'; inp.value=dname;
    form.appendChild(inp);
    document.body.appendChild(form);
    form.submit();
  };
  menu.appendChild(delBtn);
  document.body.appendChild(menu);
  setTimeout(function(){ document.addEventListener('click', function rm(){ menu.remove(); document.removeEventListener('click',rm); }); }, 10);
});
function cfgSelectPanel(id) {
  var el = document.querySelector('[data-id="' + id + '"]');
  if (el) selItem(el);
}
// ── AJAX-сохранение форм + единая кнопка «Сохранить» в шапке ────────────────
function cfgToast(msg, isError) {
  var t = document.getElementById('cfg-toast');
  if (!t) { t = document.createElement('div'); t.id = 'cfg-toast'; document.body.appendChild(t); }
  t.textContent = msg;
  t.style.cssText = 'position:fixed;bottom:22px;left:50%;transform:translateX(-50%);z-index:10001;padding:10px 18px;border-radius:6px;font-size:13px;box-shadow:0 4px 16px rgba(0,0,0,.25);color:#fff;max-width:70vw;background:' + (isError ? '#dc2626' : '#16a34a');
  t.style.display = 'block';
  clearTimeout(t._h);
  t._h = setTimeout(function(){ t.style.display = 'none'; }, isError ? 6000 : 2500);
}

// Является ли форма редактированием объекта (её сохраняем через AJAX).
// Формы создания/удаления меняют дерево и идут обычным сабмитом (перезагрузка).
function cfgIsAjaxForm(form) {
  if (!form || form.tagName !== 'FORM') return false;
  if (!form.closest('.cfg-panel')) return false;
  var a = form.getAttribute('action') || '';
  if (/\/(widget-delete|entity-delete|new|new-printform)$/.test(a)) return false;
  return a.indexOf('/configurator/') >= 0;
}

function cfgAjaxSubmit(form) {
  var saveBtn = document.getElementById('cfg-save-topbar');
  if (saveBtn) { saveBtn.disabled = true; }
  var submitBtns = form.querySelectorAll('button[type="submit"],input[type="submit"]');
  submitBtns.forEach(function(b){ b.disabled = true; });
  var done = function(){
    if (saveBtn) saveBtn.disabled = false;
    submitBtns.forEach(function(b){ b.disabled = false; });
  };
  // Формы с файлами шлём multipart (их хендлеры зовут ParseMultipartForm).
  // Остальные — urlencoded: Go r.ParseForm() декодирует их без отдельного
  // ParseMultipartForm, а FormData форсировал бы multipart, при котором
  // r.ParseForm()+r.FormValue() возвращали бы пустые поля (тост «Укажите
  // имя подсистемы» и т.п.).
  var opts = { method: 'POST', headers: { 'X-Onebase-Ajax': '1' } };
  if (form.querySelector('input[type="file"]')) {
    opts.body = new FormData(form);
  } else {
    opts.headers['Content-Type'] = 'application/x-www-form-urlencoded; charset=UTF-8';
    opts.body = new URLSearchParams(new FormData(form)).toString();
  }
  fetch(form.getAttribute('action'), opts)
    .then(function(r){ return r.json().catch(function(){ return { ok:false, error:'Некорректный ответ сервера' }; }); })
    .then(function(d){
      done();
      if (d && d.ok) {
        cfgToast(d.message || 'Сохранено', false);
        cfgMarkDirtyStar(d.running);
      } else {
        cfgToast((d && d.error) || 'Ошибка сохранения', true);
      }
    })
    .catch(function(err){ done(); cfgToast('Ошибка: ' + err.message, true); });
}

// Сохранить объект, открытый в правой панели (единая кнопка в шапке, Ctrl+S).
function cfgSaveActive() {
  var panel = document.querySelector('.cfg-panel.active');
  if (!panel) { cfgToast('Нет открытого объекта для сохранения', true); return; }
  var pick = function(scope){
    var f = null;
    scope.querySelectorAll('form').forEach(function(x){ if (!f && cfgIsAjaxForm(x)) f = x; });
    return f;
  };
  // Сперва ищем форму в активной вкладке объекта (.obj-pane.active), затем — во
  // всей панели: Ctrl+S на вкладке «Формы» должен сохранять формы, а не реквизиты.
  var activePane = panel.querySelector('.obj-pane.active');
  var target = (activePane && pick(activePane)) || pick(panel);
  if (!target) { cfgToast('В этом разделе нечего сохранять', true); return; }
  if (typeof target.requestSubmit === 'function') target.requestSubmit();
  else target.submit();
}

// После сохранения файла конфигурации, если база запущена, помечаем дерево
// звёздочкой «требуется перезапуск» — как при серверном рендере ConfigDirty.
function cfgMarkDirtyStar(running) {
  if (!running) return;
  var title = T("Конфигурация на диске изменилась с момента запуска базы. Перезапустите базу, чтобы изменения применились.");
  var targets = [];
  var grp = document.querySelector('#cfg-sidebar .cfg-group');
  if (grp) targets.push(grp);
  var app = document.querySelector('.cfg-item[data-id="panel-app"]');
  if (app) targets.push(app);
  targets.forEach(function(el){
    if (el.querySelector('.cfg-dirty')) return;
    var s = document.createElement('span');
    s.className = 'cfg-dirty';
    s.title = title;
    s.textContent = '*';
    el.appendChild(s);
  });
}

// Глобальный перехват сабмита форм редактирования объектов.
document.addEventListener('submit', function(e) {
  var form = e.target;
  if (!cfgIsAjaxForm(form)) return;
  e.preventDefault();
  cfgAjaxSubmit(form);
}, false);

// ── Перемещение объектов метаданных мышью (drag-and-drop, как в 1С) ─────────
var _treeDrag = null;
var _groupDrag = null;
function cfgGid(det) { return (det && (det.dataset.group || det.dataset.gid)) || ''; }
function initTreeDnd() {
  // объекты внутри групп — перетаскиваемы (порядок объектов)
  document.querySelectorAll('#cfg-sidebar details[data-group]').forEach(function(d) {
    d.querySelectorAll(':scope > .cfg-item').forEach(function(it) { it.setAttribute('draggable', 'true'); });
  });
  // сами группы — перетаскиваемы за заголовок (порядок групп)
  document.querySelectorAll('#cfg-sidebar details.cfg-tree').forEach(function(d) {
    var sum = d.querySelector(':scope > summary');
    if (sum) sum.setAttribute('draggable', 'true');
  });
}
// Применить сохранённый порядок групп при загрузке (клиентская перестановка).
function applyGroupOrder() {
  if (!window._treeGroupOrder || !_treeGroupOrder.length) return;
  var sb = document.getElementById('cfg-sidebar');
  if (!sb) return;
  var groups = Array.prototype.slice.call(sb.querySelectorAll(':scope > details.cfg-tree'));
  if (groups.length < 2) return;
  var byId = {};
  groups.forEach(function(d) { var id = cfgGid(d); if (id) byId[id] = d; });
  var ordered = [];
  _treeGroupOrder.forEach(function(id) { if (byId[id]) { ordered.push(byId[id]); delete byId[id]; } });
  groups.forEach(function(d) { var id = cfgGid(d); if (byId[id]) ordered.push(d); });
  var stop = groups[groups.length - 1].nextSibling;
  ordered.forEach(function(d) { sb.insertBefore(d, stop); });
}
document.addEventListener('dragstart', function(e) {
  // перетаскивание группы — старт на заголовке
  var sum = e.target.closest ? e.target.closest('summary.cfg-group-hd') : null;
  if (sum && sum.parentElement && sum.parentElement.matches('details.cfg-tree')) {
    _groupDrag = sum.parentElement; _treeDrag = null;
    _groupDrag.style.opacity = '0.5';
    if (e.dataTransfer) { e.dataTransfer.effectAllowed = 'move'; try { e.dataTransfer.setData('text/plain', cfgGid(_groupDrag)); } catch (_) {} }
    return;
  }
  // перетаскивание объекта внутри группы
  var it = e.target.closest ? e.target.closest('.cfg-item') : null;
  if (!it || !it.parentElement || !it.parentElement.matches('details[data-group]')) return;
  _treeDrag = it; _groupDrag = null;
  it.style.opacity = '0.4';
  if (e.dataTransfer) { e.dataTransfer.effectAllowed = 'move'; try { e.dataTransfer.setData('text/plain', it.dataset.id || ''); } catch (_) {} }
});
// Сохраняем порядок в dragend, а не в drop: dragend срабатывает всегда после
// перетаскивания (даже если отпустили мимо валидной цели/в пустой области), —
// это устраняет случай, когда «перетащил в конец, но не сохранилось».
document.addEventListener('dragend', function() {
  if (_treeDrag) {
    var parent = _treeDrag.parentElement;
    if (parent && parent.matches('details[data-group]')) {
      var names = [];
      parent.querySelectorAll(':scope > .cfg-item').forEach(function(it) {
        names.push((it.dataset.id || '').replace(/^[a-z]+-/, ''));
      });
      cfgSaveOrder(parent.dataset.group, names);
    }
    _treeDrag.style.opacity = ''; _treeDrag = null;
  }
  if (_groupDrag) {
    var sb = _groupDrag.parentElement;
    if (sb) {
      var ids = [];
      sb.querySelectorAll(':scope > details.cfg-tree').forEach(function(d) { var id = cfgGid(d); if (id) ids.push(id); });
      cfgSaveOrder('groups', ids);
    }
    _groupDrag.style.opacity = ''; _groupDrag = null;
  }
});
document.addEventListener('dragover', function(e) {
  if (_groupDrag) {
    var os = e.target.closest ? e.target.closest('summary.cfg-group-hd') : null;
    var det = os ? os.parentElement : null;
    if (!det || det === _groupDrag || !det.matches('details.cfg-tree') || det.parentElement !== _groupDrag.parentElement) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    var rg = det.getBoundingClientRect();
    var beforeG = (e.clientY - rg.top) < rg.height / 2;
    det.parentElement.insertBefore(_groupDrag, beforeG ? det : det.nextSibling);
    return;
  }
  if (!_treeDrag) return;
  var det = _treeDrag.parentElement;
  var it = e.target.closest ? e.target.closest('.cfg-item') : null;
  if (it && it !== _treeDrag && it.parentElement === det) {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    var r = it.getBoundingClientRect();
    var before = (e.clientY - r.top) < r.height / 2;
    det.insertBefore(_treeDrag, before ? it : it.nextSibling);
    return;
  }
  // Над пустой областью группы (ниже последнего элемента) — разрешаем дроп и
  // двигаем элемент в конец, иначе перемещение «на последнее место» не работает:
  // без preventDefault здесь событие drop не сработало бы.
  if (e.target.closest && e.target.closest('summary')) return; // над заголовком — игнор
  var inDet = e.target.closest ? e.target.closest('details[data-group]') : null;
  if (inDet === det) {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    if (det.lastElementChild !== _treeDrag) det.appendChild(_treeDrag);
  }
});
// drop только подавляет действие браузера по умолчанию; само сохранение — в
// dragend (срабатывает надёжнее). Так избегаем и двойного POST.
document.addEventListener('drop', function(e) {
  if (_treeDrag || _groupDrag) e.preventDefault();
});
function cfgSaveOrder(group, names) {
  var fd = new FormData();
  fd.append('group', group);
  names.forEach(function(n) { fd.append('name', n); });
  fetch('/bases/' + _dbgBase + '/configurator/reorder', { method: 'POST', headers: { 'X-Onebase-Ajax': '1' }, body: fd })
    .then(function(r){ return r.json().catch(function(){ return { ok:false }; }); })
    .then(function(d){
      if (!d || !d.ok) { cfgToast((d && d.error) || 'Не удалось сохранить порядок', true); return; }
      cfgToast('Порядок сохранён', false);
      // Подсистемы сортируются по полю order; сервер записал order=(i+1)*10.
      // Синхронизируем number-инпуты «Порядок» в панелях, иначе последующее
      // «Сохранить» подсистемы вернуло бы устаревший порядок из формы.
      if (group === 'subsystems') {
        names.forEach(function(n, i){
          var panel = document.getElementById('sub-' + n);
          if (!panel) return;
          var inp = panel.querySelector('input[name="order"]');
          if (inp) inp.value = (i + 1) * 10;
        });
      }
    })
    .catch(function(err){ cfgToast('Ошибка: ' + err.message, true); });
}
function initTree() { applyGroupOrder(); initTreeDnd(); }
initTree();
document.addEventListener('DOMContentLoaded', initTree);

// ── Перетаскиваемые разделители боковых панелей (issue #131) ────────────────
// Ширина панели хранится в CSS-переменной (--cfg-left-w / --cfg-dbg-w) и
// localStorage. .collapsed перебивает переменную по специфичности, поэтому
// сворачивание дерева продолжает работать. side:'right' — ручка справа от панели
// (дерево слева), 'left' — слева (панель отладки справа).
function cfgMakeResizer(target, opts){
  var h=document.createElement('div');
  h.className='cfg-resizer';
  if(opts.id)h.id=opts.id;
  h.title='Потяните, чтобы изменить ширину';
  if(opts.side==='left')target.parentNode.insertBefore(h,target);
  else target.parentNode.insertBefore(h,target.nextSibling);
  h.addEventListener('pointerdown',function(e){
    e.preventDefault();
    try{h.setPointerCapture(e.pointerId);}catch(_){}
    h.classList.add('dragging');
    document.body.classList.add('cfg-resizing');
    var startX=e.clientX, startW=target.getBoundingClientRect().width;
    function move(ev){
      var dx=ev.clientX-startX;
      var w=opts.side==='left'?startW-dx:startW+dx;
      w=Math.max(opts.min,Math.min(opts.max,w));
      document.documentElement.style.setProperty(opts.cssVar,w+'px');
    }
    function up(){
      h.classList.remove('dragging');
      document.body.classList.remove('cfg-resizing');
      h.removeEventListener('pointermove',move);
      h.removeEventListener('pointerup',up);
      var cur=getComputedStyle(document.documentElement).getPropertyValue(opts.cssVar).trim();
      try{localStorage.setItem(opts.storeKey,cur);}catch(_){}
    }
    h.addEventListener('pointermove',move);
    h.addEventListener('pointerup',up);
  });
  // двойной клик по ручке — сброс к ширине по умолчанию
  h.addEventListener('dblclick',function(){
    document.documentElement.style.removeProperty(opts.cssVar);
    try{localStorage.removeItem(opts.storeKey);}catch(_){}
  });
  return h;
}
function cfgRestoreVar(cssVar,storeKey){
  try{var v=localStorage.getItem(storeKey);if(v)document.documentElement.style.setProperty(cssVar,v);}catch(_){}
}
function initResizers(){
  cfgRestoreVar('--cfg-left-w','cfgLeftW');
  cfgRestoreVar('--cfg-dbg-w','cfgDbgW');
  var left=document.getElementById('cfg-sidebar');
  if(left&&!left._rsz){left._rsz=cfgMakeResizer(left,{side:'right',cssVar:'--cfg-left-w',storeKey:'cfgLeftW',min:150,max:600});}
  var dbg=document.getElementById('dbg-panel');
  if(dbg&&!dbg._rsz){
    var rz=cfgMakeResizer(dbg,{side:'left',cssVar:'--cfg-dbg-w',storeKey:'cfgDbgW',min:220,max:680,id:'dbg-resizer'});
    rz.style.display=(dbg.style.display==='none')?'none':'';   // показывается вместе с панелью отладки
    dbg._rsz=rz;
  }
}
document.addEventListener('DOMContentLoaded',initResizers);

// ── Поиск/фильтр по дереву метаданных (как в 1С) ───────────────────────────
function filterTree(q) {
  q = (q || '').trim().toLowerCase();
  var sidebar = document.getElementById('cfg-sidebar');
  if (!sidebar) return;
  sidebar.querySelectorAll('.cfg-item').forEach(function(it) {
    var txt = (it.textContent || '').toLowerCase();
    it.style.display = (!q || txt.indexOf(q) >= 0) ? '' : 'none';
  });
  sidebar.querySelectorAll('details.cfg-tree').forEach(function(d) {
    var items = d.querySelectorAll('.cfg-item');
    var visible = 0;
    items.forEach(function(it){ if (it.style.display !== 'none') visible++; });
    d.style.display = (!q || visible > 0) ? '' : 'none';
    if (q && visible > 0) d.open = true;
  });
}
document.addEventListener('keydown', function(e) {
  if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S' || e.key === 'ы' || e.key === 'Ы')) {
    e.preventDefault(); cfgSaveActive(); return;
  }
  if ((e.ctrlKey || e.metaKey) && (e.key === 'f' || e.key === 'F' || e.key === 'а' || e.key === 'А')) {
    var s = document.getElementById('cfg-tree-search');
    if (s) { e.preventDefault(); s.focus(); s.select(); }
  } else if (e.key === 'Escape') {
    var s2 = document.getElementById('cfg-tree-search');
    if (s2 && document.activeElement === s2 && s2.value) { s2.value = ''; filterTree(''); }
  }
});
// Поиск узла дерева по точному data-id строковым сравнением — устойчиво к
// кавычкам/спецсимволам в имени объекта (CSS-селектор на них спотыкается).
function cfgFindItem(id){
  if(!id) return null;
  var all=document.querySelectorAll('[data-id]');
  for(var i=0;i<all.length;i++){ if(all[i].getAttribute('data-id')===id) return all[i]; }
  return null;
}
// Автовыбор узла дерева после загрузки/перезагрузки: точный selectedTreeId
// (например, после создания объекта — issue #127) либо prefix-поиск по имени
// сохранённого объекта. Раскрывает свёрнутую группу, а при явном создании/выборе
// (directId) ещё и скроллит к узлу с краткой подсветкой. Выполняется по готовности
// DOM: главный <script> подключается ДО разметки дерева, поэтому без этого узлов
// в DOM ещё нет.
function cfgAutoSelect(){
  var directId = window.__cfg.selectedTreeId;
  var saved = window.__cfg.fieldsSaved || window.__cfg.moduleSaved;
  var el=null;
  if(directId)el=cfgFindItem(directId);
  if(!el&&saved&&saved!=='')['e-','r-','ir-','ar-','en-','cn-','rep-','mod-','proc-','pf-','dpf-','mkt-','sub-','wdg-','page-','panel-app'].forEach(function(p){if(!el)el=cfgFindItem(p+saved);});
  if(el){
    var grp=el.closest('details.cfg-tree'); if(grp)grp.open=true;   // раскрыть свёрнутую группу
    selItem(el);
    if(directId){                                                   // явное создание/выбор — проскроллить и подсветить
      try{el.scrollIntoView({block:'center'});}catch(e){el.scrollIntoView();}
      el.style.outline='2px solid #f59e0b';
      setTimeout(function(){el.style.outline='';},1600);
    }
  } else {var f=document.querySelector('.cfg-item');if(f)selItem(f);}
}
if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',cfgAutoSelect);
else cfgAutoSelect();

// ── Module tabs ────────────────────────────────────────────────
function modTab(el, panelId) {
  var wrap = el.closest('.module-editor-wrap');
  wrap.querySelectorAll('.module-tab').forEach(function(t){t.classList.remove('active')});
  wrap.querySelectorAll('.module-pane').forEach(function(p){p.classList.remove('active')});
  el.classList.add('active');
  document.getElementById(panelId).classList.add('active');
}

// Вкладки редактора объекта (issue #35). Скоуп — ближайший .obj-editor,
// чтобы не конфликтовать с вложенными modTab/formTab.
function cfgObjTab(el, paneId){
  var box = el.closest('.obj-editor');
  box.querySelectorAll('.obj-tab').forEach(function(t){t.classList.remove('active')});
  box.querySelectorAll('.obj-pane').forEach(function(p){p.classList.remove('active')});
  el.classList.add('active');
  document.getElementById(paneId).classList.add('active');
}

// ── Layout Editor (макет v2, план 64) ─────────────────────────────
// _led[n].data.areas — массив {name, rows} (sequence). Старый map-формат
// (areas: {Имя:{rows}}) читается и конвертируется в массив с сохранением
// порядка ключей. Незнакомые ключи (page/binding/будущие) НЕ теряются: правим
// распарсенный объект, не пересобираем его с нуля.
var _led={};
// _ldMeta — метаданные для панели «Данные» (план 64, этап 5b, 6.5):
// {entities:{Имя:{fields,tableParts}}, constants:[], formDoc:{макет→документ}}.
var _ldMeta = window.__ldMeta;
if(!_ldMeta||typeof _ldMeta!=='object')_ldMeta={entities:{},constants:[],formDoc:{}};
// _ldEntityForForm возвращает метаданные сущности, к которой привязан макет n,
// или null. Имя документа берётся из formDoc[n] (регистронезависимо).
function _ldEntityForForm(n){
  if(!_ldMeta||!_ldMeta.formDoc)return null;
  var doc=_ldMeta.formDoc[(n||'').toLowerCase()];
  if(!doc)return null;
  var ents=_ldMeta.entities||{};
  if(ents[doc])return ents[doc];
  var dl=doc.toLowerCase();
  for(var k in ents){if(Object.prototype.hasOwnProperty.call(ents,k)&&k.toLowerCase()===dl)return ents[k];}
  return null;
}
// _ldNormAreas приводит areas к массиву {name, rows}. Принимает:
//   - массив (v2) — нормализует name/rows у каждого элемента;
//   - объект (legacy map) — порядок ключей сохраняется (JS Object iteration order).
function _ldNormAreas(d){
  if(!d||typeof d!=='object')return;
  var a=d.areas;
  if(Array.isArray(a)){
    for(var i=0;i<a.length;i++){
      if(!a[i]||typeof a[i]!=='object')a[i]={};
      if(a[i].name==null)a[i].name='Область'+(i+1);
      if(!Array.isArray(a[i].rows))a[i].rows=[];
    }
    return;
  }
  if(a&&typeof a==='object'){
    var out=[];
    for(var k in a){
      if(!Object.prototype.hasOwnProperty.call(a,k))continue;
      var ar=(a[k]&&typeof a[k]==='object')?a[k]:{};
      // переносим все ключи области (rows + будущие), задаём name и rows.
      var area={};for(var p in ar){if(Object.prototype.hasOwnProperty.call(ar,p))area[p]=ar[p];}
      area.name=k;
      if(!Array.isArray(area.rows))area.rows=[];
      out.push(area);
    }
    d.areas=out;
    return;
  }
  d.areas=[];
}
// _ldAreas возвращает массив областей (после нормализации).
function _ldAreas(n){var s=_led[n];if(!s||!s.data)return [];return Array.isArray(s.data.areas)?s.data.areas:[];}
// _ldArea возвращает область по имени (регистронезависимо) или null.
function _ldAreaByName(n,name){
  var as=_ldAreas(n);
  for(var i=0;i<as.length;i++){if(as[i].name===name||(as[i].name||'').toLowerCase()===(name||'').toLowerCase())return as[i];}
  return null;
}
function _ldAreaIndex(n,name){
  var as=_ldAreas(n);
  for(var i=0;i<as.length;i++){if(as[i].name===name||(as[i].name||'').toLowerCase()===(name||'').toLowerCase())return i;}
  return -1;
}
function initLayoutEditor(n){
  var ta=document.getElementById('ta-mkt-'+n);
  var ved=document.getElementById('veditor-'+n);
  if(!ta){if(ved)ved.innerHTML='<p style="color:red">ta not found: '+n+'</p>';return;}
  if(!window.jsyaml){if(ved)ved.innerHTML='<p style="color:red">js-yaml not loaded! [v5]</p>';return;}
  var d=null;
  try{d=jsyaml.load(ta.value);}catch(e){}
  if(!d||typeof d!=='object')d={areas:[]};
  _ldNormAreas(d);
  _led[n]={data:d,sel:null,init:true};
  if(_ldAreas(n).length>0){renderLayoutEditor(n);}else{_ldSyncPagePanel(n);}
}
// _ldBorderWidth переводит толщину (thin/medium/thick/all) в CSS-ширину.
function _ldBorderWidth(v){
  switch(v){case 'thick':return '2px';case 'medium':return '1.5px';case 'thin':case 'all':case '':return '1px';default:return '1px';}
}
// _ldSideBorderCss строит CSS одной стороны рамки.
function _ldSideBorderCss(side,val,color){
  if(val==='none'||val==='')return 'border-'+side+':none;';
  return 'border-'+side+':'+_ldBorderWidth(val)+' solid '+color+';';
}
function _ldCellStyle(c,extra){
  var st='padding:4px 8px;min-width:40px;';
  // border: per-side borders приоритетнее legacy-пресета.
  var bc=c.borderColor||'#999';
  var bs=c.borders;
  if(bs&&typeof bs==='object'&&(bs.left||bs.top||bs.right||bs.bottom)){
    st+=_ldSideBorderCss('left',bs.left||'none',bc);
    st+=_ldSideBorderCss('top',bs.top||'none',bc);
    st+=_ldSideBorderCss('right',bs.right||'none',bc);
    st+=_ldSideBorderCss('bottom',bs.bottom||'none',bc);
  }else{
    var b=c.border||'';
    if(b==='none')st+='border:none;';
    else if(b==='thick')st+='border:2px solid '+bc+';';
    else if(b==='medium')st+='border:1.5px solid '+bc+';';
    else st+='border:1px solid '+bc+';';
  }
  if(c.bold)st+='font-weight:bold;';
  if(c.italic)st+='font-style:italic;';
  if(c.fontSize)st+='font-size:'+c.fontSize+'pt;';
  if(c.fontFamily)st+='font-family:'+c.fontFamily+';';
  if(c.backColor)st+='background-color:'+c.backColor+';';
  if(c.textColor)st+='color:'+c.textColor+';';
  if(c.align)st+='text-align:'+c.align+';';
  if(c.valign==='middle')st+='vertical-align:middle;';
  else if(c.valign==='top')st+='vertical-align:top;';
  else if(c.valign==='bottom')st+='vertical-align:bottom;';
  return st+(extra||'');
}
function _ldColgroup(d){
  var cols=d.columns||[];
  if(!cols.length)return '';
  var h='<colgroup>';
  for(var i=0;i<cols.length;i++){
    h+=cols[i]&&cols[i].width?'<col style="width:'+esc(cols[i].width)+'">':'<col>';
  }
  return h+'</colgroup>';
}
// _ldColCount возвращает максимальное число колонок макета (учитывая colspan
// и общий массив columns).
function _ldColCount(n){
  var as=_ldAreas(n),max=0,s=_led[n];
  if(s&&s.data&&Array.isArray(s.data.columns)&&s.data.columns.length>max)max=s.data.columns.length;
  for(var i=0;i<as.length;i++){
    var rows=as[i].rows||[];
    for(var ri=0;ri<rows.length;ri++){
      var cells=rows[ri].cells||[],w=0;
      for(var ci=0;ci<cells.length;ci++)w+=(cells[ci]&&cells[ci].colspan>1)?cells[ci].colspan:1;
      if(w>max)max=w;
    }
  }
  return max;
}
// _ldGridCols — число колонок фоновой сетки конструктора (как табличный документ
// 1С): не меньше 8, иначе по фактической ширине макета.
function _ldGridCols(n){var c=_ldColCount(n);return c>8?c:8;}
// ── Границы листа (формат/ориентация/поля) ────────────────────────
// Чтобы вписать форму в размер: печатная ширина = ширина формата − левое − правое
// поле; колонки px→мм при 96dpi (как pxToMM в sheet/pdf.go).
// _ldFmtDims — портретные размеры формата (мм), как formatSizeMM в sheet/pdf.go.
function _ldFmtDims(fmt){
  var t={A4:[210,297],A5:[148,210],A3:[297,420],LETTER:[215.9,279.4],LEGAL:[215.9,355.6]};
  return t[String(fmt||'A4').toUpperCase().trim()]||t.A4;
}
// _ldEffMargins — действующие поля (мм). Повторяет движок: page отсутствует →
// 10мм (DefaultPageSetup); page есть, но поле не задано → 0 (declarative.go:29).
function _ldEffMargins(n){
  var s=_led[n],p=(s&&s.data&&s.data.page)?s.data.page:null;
  var d=p?0:10,m=(p&&p.margins)?p.margins:{};
  return {top:(m.top!=null?+m.top:d),right:(m.right!=null?+m.right:d),
          bottom:(m.bottom!=null?+m.bottom:d),left:(m.left!=null?+m.left:d)};
}
// _ldPageInfo — печатная ширина листа (мм и CSS-px) + подпись формата.
function _ldPageInfo(n){
  var s=_led[n],p=(s&&s.data&&s.data.page)?s.data.page:null;
  var fmt=String((p&&p.format)||'A4').toUpperCase().trim();
  var dm=_ldFmtDims(fmt),w=dm[0],hh=dm[1];
  var land=/^(land|альб|гор)/i.test((p&&p.orientation)||'');
  if(land){var tmp=w;w=hh;hh=tmp;}
  var em=_ldEffMargins(n);
  var cmm=w-em.left-em.right;if(cmm<10)cmm=10;
  var disp={A4:'A4',A5:'A5',A3:'A3',LETTER:'Letter',LEGAL:'Legal'}[fmt]||'A4';
  return {contentMM:cmm,contentPx:cmm*96/25.4,fmt:disp,land:land,
          label:disp+(land?' '+T("альбомная"):' '+T("книжная"))+' · '+Math.round(cmm)+' '+T("мм")};
}
// ldEnsurePage создаёт page с действующими значениями (вывод не меняется).
function ldEnsurePage(n){
  var s=_led[n];if(!s)return null;
  var info=_ldPageInfo(n),em=_ldEffMargins(n);
  if(!s.data.page)s.data.page={};
  var p=s.data.page;
  if(!p.format)p.format=info.fmt;
  if(!p.orientation)p.orientation=info.land?'landscape':'portrait';
  if(!p.margins)p.margins={top:em.top,right:em.right,bottom:em.bottom,left:em.left};
  return p;
}
function ldSetPageField(n,field,val){var p=ldEnsurePage(n);if(!p)return;p[field]=val;renderLayoutEditor(n);}
function ldSetPageMargin(n,side,val){var p=ldEnsurePage(n);if(!p)return;var x=parseFloat(val);p.margins[side]=isNaN(x)?0:x;renderLayoutEditor(n);}
// _ldSyncPagePanel заполняет контролы «Лист» действующими значениями.
function _ldSyncPagePanel(n){
  var info=_ldPageInfo(n),em=_ldEffMargins(n);
  _setVal('pg-fmt-'+n,info.fmt);
  _setVal('pg-ori-'+n,info.land?'landscape':'portrait');
  _setVal('pg-ml-'+n,em.left);_setVal('pg-mt-'+n,em.top);
  _setVal('pg-mr-'+n,em.right);_setVal('pg-mb-'+n,em.bottom);
  var el=document.getElementById('pg-info-'+n);
  if(el)el.textContent=T("печатная ширина")+' '+Math.round(info.contentMM)+' '+T("мм")+' (≈'+Math.round(info.contentPx)+' px)';
}
// _ldRuler рисует линейку колонок (6.2): по клетке на колонку, клик → ширина.
// columns — ГЛОБАЛЬНЫЕ для макета; линейка выводится один раз над первой областью.
function _ldRuler(n){
  var nc=_ldGridCols(n);if(nc<=0)return '';
  var s=_led[n],cols=(s&&s.data&&Array.isArray(s.data.columns))?s.data.columns:[];
  var h='<div style="display:flex;margin:0 0 2px 18px" title="'+T("Ширины колонок")+'">';
  for(var i=0;i<nc;i++){
    var w=(cols[i]&&cols[i].width)?cols[i].width:'';
    var lbl=w?esc(w):'авто';
    h+='<div onclick="ldColWidth(\''+n+'\','+i+')" style="flex:1;min-width:42px;text-align:center;font-size:9px;color:#888;background:#f1f5f9;border:1px solid #e2e8f0;padding:1px 2px;cursor:pointer;overflow:hidden;white-space:nowrap" title="'+T("Колонка")+' '+(i+1)+'">'+lbl+'</div>';
  }
  return h+'</div>';
}
// noYamlSync=true prevents overwriting textarea (used when only selection changes)
function renderLayoutEditor(n,noYamlSync){
  var s=_led[n];if(!s)return;
  if(!noYamlSync&&window.jsyaml&&!s.init){
    _ldSyncTextarea(n);
  }
  if(s.init)s.init=false;
  var areas=_ldAreas(n);
  var cg=_ldColgroup(s.data),gc=_ldGridCols(n);
  var pi=_ldPageInfo(n),guideLeft=Math.round(18+pi.contentPx);
  var h='<div style="font-family:Arial,sans-serif;font-size:12px;position:relative;min-width:'+(guideLeft+40)+'px">';
  h+=_ldRuler(n);
  for(var ai=0;ai<areas.length;ai++){
    var ar=areas[ai],an=ar.name;
    h+='<div style="margin-bottom:16px">';
    h+='<div style="display:flex;align-items:center;gap:6px;margin-bottom:4px">';
    h+='<span style="font-weight:bold;color:#4a9">'+esc(an)+'</span>';
    h+='<button type="button" title="'+T("Вверх")+'" '+(ai===0?'disabled ':'')+'style="font-size:10px;padding:1px 6px;border:1px solid #ccc;border-radius:3px;cursor:pointer'+(ai===0?';opacity:.3':'')+'" onclick="moveLayoutArea(\''+n+'\','+ai+',-1)">↑</button>';
    h+='<button type="button" title="'+T("Вниз")+'" '+(ai===areas.length-1?'disabled ':'')+'style="font-size:10px;padding:1px 6px;border:1px solid #ccc;border-radius:3px;cursor:pointer'+(ai===areas.length-1?';opacity:.3':'')+'" onclick="moveLayoutArea(\''+n+'\','+ai+',1)">↓</button>';
    h+='<button type="button" style="font-size:10px;padding:1px 6px;border:1px solid #ccc;border-radius:3px;cursor:pointer" onclick="addLayoutRow(\''+escJsAttr(n)+"','"+escJsAttr(an)+"')\">+ "+T("Строка")+"</button>";
    h+='<button type="button" style="font-size:10px;padding:1px 6px;border:1px solid #fcc;border-radius:3px;cursor:pointer;color:#c33" onclick="delLayoutArea(\''+escJsAttr(n)+"','"+escJsAttr(an)+"')\">✕</button>";
    h+='</div>';
    h+=_ldAreaBindingRow(n,an);
    h+='<table style="border-collapse:collapse">'+cg;
    var rows=ar.rows||[];
    for(var ri=0;ri<rows.length;ri++){
      var rowStyle=rows[ri].height?' style="height:'+esc(rows[ri].height)+'"':'';
      h+='<tr'+rowStyle+'>';
      // Левая ручка строки (6.2): клик → высота строки.
      var hLbl=rows[ri].height?esc(rows[ri].height):'↕';
      h+='<td onclick="ldRowHeight(\''+escJsAttr(n)+"','"+escJsAttr(an)+"',"+ri+')" style="border:1px solid #e2e8f0;background:#f1f5f9;color:#888;font-size:9px;text-align:center;cursor:pointer;width:16px;padding:0" title="'+T("Высота строки")+'">'+hLbl+'</td>';
      var cells=rows[ri].cells||[];
      for(var ci=0;ci<cells.length;ci++){
        var c=cells[ci];
        var isSel=s.sel&&s.sel.area===an&&s.sel.row===ri&&s.sel.col===ci;
        var extra='cursor:pointer;';
        if(isSel)extra+='outline:2px solid #1a73e8;outline-offset:-2px;';
        var st=_ldCellStyle(c,extra);
        var at='';
        if(c.colspan&&c.colspan>1)at+=' colspan="'+c.colspan+'"';
        if(c.rowspan&&c.rowspan>1)at+=' rowspan="'+c.rowspan+'"';
        var txt=c.text?esc(c.text):'';
        if(c.parameter)txt='<span style="color:#888">['+esc(c.parameter)+']</span>';
        if(!txt)txt='&nbsp;';
        h+='<td style="'+st+'"'+at+' onclick="selectCell(\''+escJsAttr(n)+"','"+escJsAttr(an)+"',"+ri+','+ci+')">'+txt+'</td>';
      }
      // Фоновая сетка (как в 1С): добиваем строку пустыми кликабельными клетками
      // до ширины сетки. Клик по пустой клетке добавляет ячейку в строку.
      var used=0;for(var ui=0;ui<cells.length;ui++){var uc=cells[ui];used+=(uc&&uc.colspan>1)?uc.colspan:1;}
      for(var pe=used;pe<gc;pe++){
        h+='<td onclick="ldAddCellAt(\''+escJsAttr(n)+"','"+escJsAttr(an)+"',"+ri+')" style="border:1px dashed #dce4ee;min-width:38px;height:16px;cursor:cell" title="'+T("Добавить ячейку")+'">&nbsp;</td>';
      }
      h+='<td style="border:none;padding:2px;white-space:nowrap">';
      if(ri>0){h+='<button type="button" title="'+T("Разрезать область перед этой строкой")+'" style="font-size:10px;color:#0369a1;border:none;cursor:pointer;background:transparent" onclick="splitLayoutArea(\''+escJsAttr(n)+"','"+escJsAttr(an)+"',"+ri+')\">✂</button>';}
      h+='<button type="button" title="'+T("Удалить строку")+'" style="font-size:10px;color:#c33;border:none;cursor:pointer;background:transparent" onclick="delLayoutRow(\''+escJsAttr(n)+"','"+escJsAttr(an)+"',"+ri+')\">✕</button></td>';
      h+='</tr>';
    }
    h+='</table></div>';
  }
  // Граница листа: красная пунктирная линия + подпись на печатной ширине страницы.
  h+='<div style="position:absolute;top:0;bottom:0;left:'+guideLeft+'px;border-left:2px dashed #ef4444;pointer-events:none;z-index:3"></div>';
  h+='<div style="position:absolute;top:2px;left:'+(guideLeft+4)+'px;font-size:9px;color:#ef4444;background:rgba(255,255,255,.85);padding:0 3px;pointer-events:none;z-index:3;white-space:nowrap" title="'+T("Край печатной области листа")+'">'+esc(pi.label)+'</div>';
  h+='</div>';
  var ved=document.getElementById('veditor-'+n);
  if(ved)ved.innerHTML=h;
  _ldSyncPagePanel(n);
  renderPreviewOnly(n);
  syncProps(n);
  renderDataPanel(n);
}
// _ldSyncTextarea сериализует s.data → textarea. areas пишутся как sequence;
// jsyaml уже хранит areas массивом, поэтому dump даёт v2-формат.
function _ldSyncTextarea(n){
  var s=_led[n];if(!s||!window.jsyaml)return;
  var y=jsyaml.dump(s.data,{lineWidth:-1,quotingType:'"'});
  var ta=document.getElementById('ta-mkt-'+n);
  if(ta)ta.value=y;
}
function renderPreviewOnly(n){
  var pv=document.getElementById('vpreview-'+n);
  if(!pv)return;
  var s=_led[n];if(!s){pv.innerHTML='';return;}
  var areas=_ldAreas(n),h='<div style="font-family:Arial,sans-serif;font-size:12px">';
  var cg=_ldColgroup(s.data);
  for(var ai=0;ai<areas.length;ai++){
    var ar=areas[ai],an=ar.name;
    h+='<div style="margin-bottom:16px"><div style="font-weight:bold;color:#4a9;margin-bottom:4px">'+esc(an)+'</div>';
    h+='<table style="border-collapse:collapse">'+cg;
    var rows=ar.rows||[];
    for(var ri=0;ri<rows.length;ri++){
      var rowStyle=rows[ri].height?' style="height:'+esc(rows[ri].height)+'"':'';
      h+='<tr'+rowStyle+'>';
      var cells=rows[ri].cells||[];
      for(var ci=0;ci<cells.length;ci++){
        var c=cells[ci];
        var st=_ldCellStyle(c,'');
        var at='';
        if(c.colspan&&c.colspan>1)at+=' colspan="'+c.colspan+'"';
        if(c.rowspan&&c.rowspan>1)at+=' rowspan="'+c.rowspan+'"';
        var txt=c.text?esc(c.text):'';
        if(c.parameter)txt='<span style="color:#888">['+esc(c.parameter)+']</span>';
        if(!txt)txt='&nbsp;';
        h+='<td style="'+st+'"'+at+'>'+txt+'</td>';
      }
      h+='</tr>';
    }
    h+='</table></div>';
  }
  h+='</div>';
  pv.innerHTML=h;
}
function selectCell(n,a,r,c){
  var s=_led[n];if(!s)return;
  s.sel={area:a,row:r,col:c};
  renderLayoutEditor(n,true); // don't reformat YAML on cell selection
}
// ldAddCellAt добавляет новую пустую ячейку в конец строки (клик по фоновой
// сетке) и выделяет её.
function ldAddCellAt(n,a,r){
  var s=_led[n];if(!s)return;
  var ar=_ldAreaByName(n,a);if(!ar||!ar.rows||!ar.rows[r])return;
  var row=ar.rows[r];if(!row.cells)row.cells=[];
  row.cells.push({});
  s.sel={area:a,row:r,col:row.cells.length-1};
  renderLayoutEditor(n);
}
// ldDeselect снимает выделение (закрывает закреплённую панель свойств).
function ldDeselect(n){var s=_led[n];if(!s)return;s.sel=null;renderLayoutEditor(n,true);}
function _setVal(id,v){var el=document.getElementById(id);if(el)el.value=v;}
function _setChk(id,v){var el=document.getElementById(id);if(el)el.checked=v;}
// _ldSelCell возвращает выделенную ячейку {area,row,cell,c} или null.
function _ldSelCell(n){
  var s=_led[n];if(!s||!s.sel)return null;
  var ar=_ldAreaByName(n,s.sel.area);
  if(!ar||!ar.rows||!ar.rows[s.sel.row])return null;
  var row=ar.rows[s.sel.row];
  if(!row.cells)row.cells=[];
  if(!row.cells[s.sel.col])row.cells[s.sel.col]={};
  return {ar:ar,row:row,c:row.cells[s.sel.col]};
}
function syncProps(n){
  var s=_led[n];if(!s)return;
  var pp=document.getElementById('vprops-'+n);
  if(!pp)return;
  if(!s.sel){pp.style.display='none';return;}
  var sc=_ldSelCell(n);
  if(!sc){pp.style.display='none';return;}
  var c=sc.c;
  pp.style.display='block';
  _setVal('vp-text-'+n,c.text||'');
  _setVal('vp-param-'+n,c.parameter||'');
  _setChk('vp-bold-'+n,!!c.bold);
  _setChk('vp-italic-'+n,!!c.italic);
  _setVal('vp-align-'+n,c.align||'');
  _setVal('vp-valign-'+n,c.valign||'');
  _setVal('vp-bg-'+n,c.backColor||'#ffffff');
  _setVal('vp-fg-'+n,c.textColor||'#000000');
  _setVal('vp-ff-'+n,c.fontFamily||'');
  _setVal('vp-fs-'+n,c.fontSize||'');
  _setVal('vp-border-'+n,c.border||'');
  _setVal('vp-bc-'+n,c.borderColor||'#cccccc');
  _setVal('vp-colspan-'+n,c.colspan||1);
  _setVal('vp-rowspan-'+n,c.rowspan||1);
  _setVal('vp-fmt-'+n,_ldParamFormat(n,c.parameter||''));
  _ldSyncBorderUI(n,c);
}
function updateCellProp(n,prop,val){
  var sc=_ldSelCell(n);if(!sc)return;
  var c=sc.c;
  var isSpan=(prop==='colspan'||prop==='rowspan');
  if(val===''||val===0||val===false||(typeof val==='number'&&isNaN(val))||(isSpan&&val<=1)){
    delete c[prop];
  }else{
    c[prop]=val;
  }
  renderLayoutEditor(n);
}
// ── Привязка данных (план 64, этап 5b, 6.5) ───────────────────────
// _ldBinding возвращает (создавая при ensure) объект binding макета.
function _ldBinding(n,ensure){
  var s=_led[n];if(!s||!s.data)return null;
  if(!s.data.binding&&ensure)s.data.binding={};
  return s.data.binding||null;
}
// _ldRepeatForArea возвращает запись binding.repeat для области areaName или null.
function _ldRepeatForArea(n,areaName){
  var b=_ldBinding(n,false);
  if(!b||!Array.isArray(b.repeat))return null;
  var al=(areaName||'').toLowerCase();
  for(var i=0;i<b.repeat.length;i++){if((b.repeat[i].area||'').toLowerCase()===al)return b.repeat[i];}
  return null;
}
// _ldParamMapFor возвращает (создавая при ensure) карту parameters для области:
// для repeat-области это repeat[i].parameters, иначе binding.parameters.
function _ldParamMapFor(n,areaName,ensure){
  var rb=_ldRepeatForArea(n,areaName);
  if(rb){
    if(!rb.parameters&&ensure)rb.parameters={};
    return rb.parameters||null;
  }
  var b=_ldBinding(n,ensure);
  if(!b)return null;
  if(!b.parameters&&ensure)b.parameters={};
  return b.parameters||null;
}
// _ldParamFormat читает формат («| fmt») параметра выделенной ячейки из карты
// параметров соответствующей области; пусто — нет формата или нет записи.
function _ldParamFormat(n,param){
  if(!param)return '';
  var s=_led[n];if(!s||!s.sel)return '';
  var pm=_ldParamMapFor(n,s.sel.area,false);
  if(!pm)return '';
  var expr=null;
  for(var k in pm){if(Object.prototype.hasOwnProperty.call(pm,k)&&k.toLowerCase()===param.toLowerCase()){expr=pm[k];break;}}
  if(typeof expr!=='string')return '';
  var bar=expr.indexOf('|');
  if(bar<0)return '';
  return expr.slice(bar+1).trim();
}
// ldSetFormat дописывает «| формат» к выражению параметра выделенной ячейки.
// Если выражение совпадает с именем (автопривязка) и формат пуст — запись из
// parameters удаляется (не засоряем binding).
function ldSetFormat(n,fmt){
  var sc=_ldSelCell(n);if(!sc)return;
  var param=sc.c.parameter||'';
  if(!param){alert(T("У ячейки нет параметра"));return;}
  var s=_led[n];if(!s||!s.sel)return;
  var pm=_ldParamMapFor(n,s.sel.area,true);
  if(!pm)return;
  // найдём существующий ключ (регистронезависимо) или используем имя параметра.
  var key=param,curExpr=param;
  for(var k in pm){if(Object.prototype.hasOwnProperty.call(pm,k)&&k.toLowerCase()===param.toLowerCase()){key=k;curExpr=pm[k];break;}}
  var bar=curExpr.indexOf('|');
  var baseExpr=(bar<0?curExpr:curExpr.slice(0,bar)).trim();
  if(!baseExpr)baseExpr=param;
  if(fmt){
    pm[key]=baseExpr+' | '+fmt;
  }else{
    // нет формата: если выражение == имени параметра (автопривязка) — убираем запись.
    if(baseExpr.toLowerCase()===param.toLowerCase())delete pm[key];
    else pm[key]=baseExpr;
  }
  _ldCleanupParams(n,s.sel.area);
  renderLayoutEditor(n);
}
// _ldBindParameter привязывает поле к параметру ячейки. Ставит cell.parameter и
// добавляет запись в parameters ТОЛЬКО если выражение ≠ имени параметра.
function _ldBindParameter(n,areaName,paramName,expr){
  var sc=_ldSelCell(n);if(!sc)return;
  sc.c.parameter=paramName;
  delete sc.c.text; // параметр вытесняет статический текст
  if(expr&&expr.toLowerCase()!==paramName.toLowerCase()){
    var pm=_ldParamMapFor(n,areaName,true);
    if(pm)pm[paramName]=expr;
  }else{
    // автопривязка по имени — убираем лишнюю запись, если была.
    var pm2=_ldParamMapFor(n,areaName,false);
    if(pm2){for(var k in pm2){if(Object.prototype.hasOwnProperty.call(pm2,k)&&k.toLowerCase()===paramName.toLowerCase())delete pm2[k];}}
  }
  _ldCleanupParams(n,areaName);
}
// _ldCleanupParams убирает пустые карты parameters/binding из YAML.
function _ldCleanupParams(n,areaName){
  var b=_ldBinding(n,false);if(!b)return;
  var rb=_ldRepeatForArea(n,areaName);
  if(rb&&rb.parameters&&Object.keys(rb.parameters).length===0)delete rb.parameters;
  if(b.parameters&&Object.keys(b.parameters).length===0)delete b.parameters;
}
// ── Повтор по ТЧ / RepeatHeader у области (6.5) ────────────────────
// ldSetAreaRepeat включает/выключает повтор области по табличной части source.
// source='' — выключить повтор (удалить запись binding.repeat).
function ldSetAreaRepeat(n,areaName,source){
  var b=_ldBinding(n,true);if(!b)return;
  if(!Array.isArray(b.repeat))b.repeat=[];
  var al=(areaName||'').toLowerCase();
  var idx=-1;
  for(var i=0;i<b.repeat.length;i++){if((b.repeat[i].area||'').toLowerCase()===al){idx=i;break;}}
  if(source){
    if(idx>=0)b.repeat[idx].source=source;
    else b.repeat.push({area:areaName,source:source});
  }else if(idx>=0){
    b.repeat.splice(idx,1);
  }
  if(b.repeat.length===0)delete b.repeat;
  renderLayoutEditor(n);
}
// ldSetRepeatHeader ставит/снимает binding.repeat_header = areaName.
function ldSetRepeatHeader(n,areaName,on){
  var b=_ldBinding(n,true);if(!b)return;
  if(on)b.repeat_header=areaName;
  else if((b.repeat_header||'').toLowerCase()===(areaName||'').toLowerCase())delete b.repeat_header;
  renderLayoutEditor(n);
}
// _ldAreaBindingRow строит строку привязки области: select «Повтор по ТЧ» +
// чекбокс «Повторять на каждой странице». Список ТЧ берётся из метаданных
// сущности макета.
function _ldAreaBindingRow(n,areaName){
  var ent=_ldEntityForForm(n);
  var rb=_ldRepeatForArea(n,areaName);
  var b=_ldBinding(n,false);
  var rh=(b&&(b.repeat_header||'').toLowerCase()===(areaName||'').toLowerCase());
  var jn=escJsAttr(areaName);
  var h='<div style="display:flex;align-items:center;gap:8px;margin:0 0 4px;font-size:11px;color:#64748b">';
  h+='<label style="display:flex;align-items:center;gap:3px">'+esc(T("Повтор по ТЧ"))+':';
  h+='<select style="font-size:11px;padding:1px 2px" onchange="ldSetAreaRepeat(\''+n+'\',\''+jn+'\',this.value)">';
  h+='<option value="">'+esc(T("нет"))+'</option>';
  var tps=(ent&&ent.tableParts)?ent.tableParts:[];
  var cur=rb?(rb.source||''):'';
  for(var t=0;t<tps.length;t++){
    var sel=(tps[t].name.toLowerCase()===cur.toLowerCase())?' selected':'';
    h+='<option value="'+esc(tps[t].name)+'"'+sel+'>'+esc(tps[t].name)+'</option>';
  }
  // если в binding указана ТЧ, которой нет в метаданных (или метаданных нет) — покажем её.
  if(cur){
    var found=false;
    for(var t2=0;t2<tps.length;t2++){if(tps[t2].name.toLowerCase()===cur.toLowerCase())found=true;}
    if(!found)h+='<option value="'+esc(cur)+'" selected>'+esc(cur)+'</option>';
  }
  h+='</select></label>';
  h+='<label style="display:flex;align-items:center;gap:3px"><input type="checkbox"'+(rh?' checked':'')+' onchange="ldSetRepeatHeader(\''+n+'\',\''+jn+'\',this.checked)"> '+esc(T("Повторять на каждой странице"))+'</label>';
  h+='</div>';
  return h;
}
// ── Дерево данных (6.5) ───────────────────────────────────────────
// renderDataPanel рисует дерево «Реквизиты / Табличные части / Константы».
// Клик по полю при выделенной ячейке вызывает onDataFieldClick.
function renderDataPanel(n){
  var box=document.getElementById('vdata-'+n);
  if(!box)return;
  var ent=_ldEntityForForm(n);
  var h='';
  if(!ent){
    h='<p style="color:#999;font-size:11px;margin:4px 0">'+T("Сущность не определена. Укажите document: в YAML.")+'</p>';
  }else{
    var s=_led[n];
    var selArea=(s&&s.sel)?s.sel.area:null;
    var rb=selArea?_ldRepeatForArea(n,selArea):null;
    if(selArea){
      h+='<div style="font-size:10px;color:#94a3b8;margin-bottom:4px">'+esc(selArea)+(rb?' ↻ '+esc(rb.source||''):'')+'</div>';
    }
    // Реквизиты документа.
    h+='<div style="font-weight:600;color:#475569;margin:2px 0">'+esc(T("Реквизиты"))+'</div>';
    h+=_ldFieldList(n,ent.fields,'doc');
    // Табличные части.
    var tps=ent.tableParts||[];
    for(var t=0;t<tps.length;t++){
      h+='<div style="font-weight:600;color:#475569;margin:6px 0 2px">↻ '+esc(tps[t].name)+'</div>';
      h+=_ldFieldList(n,tps[t].fields,'tp:'+tps[t].name);
    }
    // Константы.
    var cs=(_ldMeta&&Array.isArray(_ldMeta.constants))?_ldMeta.constants:[];
    if(cs.length){
      h+='<div style="font-weight:600;color:#475569;margin:6px 0 2px">'+esc(T("Константы"))+'</div>';
      for(var ci=0;ci<cs.length;ci++){
        h+='<div class="ld-data-fld" style="padding:2px 4px;cursor:pointer;border-radius:3px" onclick="onDataFieldClick(\''+escJsAttr(n)+'\',\'const\',\''+escJsAttr(cs[ci])+'\')" title="'+T("Кликните по ячейке, затем по полю")+'">'+esc(cs[ci])+'</div>';
      }
    }
  }
  box.innerHTML=h;
}
// _ldFieldList рисует список полей одного раздела (реквизиты или колонки ТЧ).
function _ldFieldList(n,fields,scope){
  fields=fields||[];
  var h='';
  for(var i=0;i<fields.length;i++){
    var f=fields[i];
    var sub=(f.ref?' →':'');
    h+='<div class="ld-data-fld" style="padding:2px 4px;cursor:pointer;border-radius:3px" onclick="onDataFieldClick(\''+escJsAttr(n)+'\',\''+escJsAttr(scope)+'\',\''+escJsAttr(f.name)+'\')" title="'+T("Кликните по ячейке, затем по полю")+'">'+esc(f.name)+sub+'</div>';
  }
  if(!fields.length)h='<div style="color:#cbd5e1;font-size:11px;padding:2px 4px">—</div>';
  return h;
}
// onDataFieldClick привязывает выбранное поле к параметру выделенной ячейки.
//   scope='doc'    — реквизит документа;
//   scope='tp:Имя' — колонка ТЧ (выражение — имя колонки, действует в repeat-области);
//   scope='const'  — Константы.Имя.
function onDataFieldClick(n,scope,field){
  var s=_led[n];
  if(!s||!s.sel){alert(T("Сначала выделите ячейку"));return;}
  var areaName=s.sel.area;
  var paramName=field;
  var expr=field;
  if(scope==='const'){expr='Константы.'+field;paramName=field;}
  else if(scope.indexOf('tp:')===0){
    // колонка ТЧ: выражение — имя колонки; имеет смысл, если область привязана к этой ТЧ.
    expr=field;
  }
  _ldBindParameter(n,areaName,paramName,expr);
  renderLayoutEditor(n);
}
function applyYaml(n){
  var ta=document.getElementById('ta-mkt-'+n);
  if(!ta)return;
  var d=null;
  try{d=jsyaml.load(ta.value);}catch(e){return;}
  if(!d||typeof d!=='object')return; // invalid YAML — keep current state
  _ldNormAreas(d);
  if(!_led[n])_led[n]={data:d,sel:null};
  else _led[n].data=d;
  // re-render designer without syncing YAML back (avoid cursor jump)
  _led[n].init=true;
  renderLayoutEditor(n);
}
// Debounced YAML sync — fires while user is typing in the textarea
var _yamlTimers={};
function scheduleYamlSync(n){
  if(_yamlTimers[n])clearTimeout(_yamlTimers[n]);
  _yamlTimers[n]=setTimeout(function(){applyYaml(n);},400);
}
function saveLayoutEditor(n){
  // Flush debounced YAML input timer first.
  if(_yamlTimers[n]){clearTimeout(_yamlTimers[n]);delete _yamlTimers[n];}
  // Sync in-memory state → textarea (in case visual editor made changes without syncing).
  if(window.jsyaml&&_led[n]){_ldSyncTextarea(n);}
  return true;
}
// ── Предпросмотр (план 64, этап 5b, 6.6) ──────────────────────────
// ldPreview отправляет текущий YAML макета на сервер и показывает HTML/PDF в
// модальном iframe. format: 'html' | 'pdf'.
function ldPreview(n,entity,format){
  // синхронизируем визуальную модель в textarea, берём свежий YAML.
  if(window.jsyaml&&_led[n])_ldSyncTextarea(n);
  var ta=document.getElementById('ta-mkt-'+n);
  var yaml=ta?ta.value:'';
  var url='/bases/'+_dbgBase+'/configurator/layout/preview';
  var isPdf=(format==='pdf');
  // PDF: в WebView2 нет встроенного просмотрщика (inline-iframe → «заблокировано
  // Microsoft Edge»), поэтому сервер открывает PDF во внешнем приложении ОС.
  if(isPdf)url+='?format=pdf&open=1';
  fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({yaml:yaml,entity:entity||''})})
    .then(function(resp){
      if(!resp.ok)return resp.text().then(function(t){throw new Error(t||('HTTP '+resp.status));});
      if(isPdf){cfgToast(T("PDF открыт во внешнем приложении"));return null;}
      return resp.blob();
    })
    .then(function(blob){
      if(!blob)return;
      var u=URL.createObjectURL(blob);
      var frame=document.getElementById('ld-preview-frame');
      if(frame)frame.src=u;
      document.getElementById('ld-preview-overlay').classList.add('active');
    })
    .catch(function(err){alert(T("Ошибка предпросмотра")+': '+err.message);});
}
function ldClosePreview(){
  var ov=document.getElementById('ld-preview-overlay');
  if(ov)ov.classList.remove('active');
  var frame=document.getElementById('ld-preview-frame');
  if(frame)frame.src='about:blank';
}
// ldToggleYaml сворачивает/разворачивает левую YAML-панель редактора макета,
// отдавая всю ширину конструктору.
function ldToggleYaml(n){
  var pane=document.getElementById('yamlpane-'+n);if(!pane)return;
  var ta=document.getElementById('ta-mkt-'+n);
  var btn=document.getElementById('yamltgl-'+n);
  var lbl=document.getElementById('yamllbl-'+n);
  if(pane.getAttribute('data-collapsed')==='1'){
    pane.style.flex='0 0 42%';
    if(ta)ta.style.display='';
    if(lbl)lbl.style.display='';
    if(btn)btn.textContent='⮜';
    pane.setAttribute('data-collapsed','0');
  }else{
    pane.style.flex='0 0 30px';
    if(ta)ta.style.display='none';
    if(lbl)lbl.style.display='none';
    if(btn)btn.textContent='⮞';
    pane.setAttribute('data-collapsed','1');
  }
}
// _ldEnsure инициализирует _led[n] из textarea, если ещё не инициализирован.
function _ldEnsure(n){
  var s=_led[n];
  if(s)return s;
  var ta=document.getElementById('ta-mkt-'+n);
  if(!ta||!window.jsyaml)return null;
  var d=null;try{d=jsyaml.load(ta.value);}catch(e){}
  if(!d||typeof d!=='object')d={areas:[]};
  _ldNormAreas(d);
  s={data:d,sel:null};_led[n]=s;
  return s;
}
function addLayoutArea(n){
  var name=prompt(T("Имя новой области:"));
  if(!name)return;
  var s=_ldEnsure(n);if(!s)return;
  if(!Array.isArray(s.data.areas))s.data.areas=[];
  s.data.areas.push({name:name,rows:[{cells:[{text:T("Ячейка")}]}]});
  renderLayoutEditor(n);
}
function delLayoutArea(n,a){
  if(!confirm(T("Удалить область")+' '+a+'?'))return;
  var s=_led[n];if(!s)return;
  var i=_ldAreaIndex(n,a);
  if(i>=0)s.data.areas.splice(i,1);
  s.sel=null;
  renderLayoutEditor(n);
}
// moveLayoutArea переставляет область по индексу на dir (-1 вверх / +1 вниз).
function moveLayoutArea(n,idx,dir){
  var s=_led[n];if(!s||!Array.isArray(s.data.areas))return;
  var as=s.data.areas,j=idx+dir;
  if(j<0||j>=as.length)return;
  var tmp=as[idx];as[idx]=as[j];as[j]=tmp;
  renderLayoutEditor(n);
}
function addLayoutRow(n,a){
  var s=_led[n];if(!s)return;
  var ar=_ldAreaByName(n,a);if(!ar)return;
  if(!ar.rows)ar.rows=[];
  var maxCols=1;
  for(var i=0;i<ar.rows.length;i++){if((ar.rows[i].cells||[]).length>maxCols)maxCols=ar.rows[i].cells.length;}
  var cells=[];for(var j=0;j<maxCols;j++)cells.push({});
  ar.rows.push({cells:cells});
  renderLayoutEditor(n);
}
function delLayoutRow(n,a,ri){
  var s=_led[n];if(!s)return;
  var ar=_ldAreaByName(n,a);if(!ar||!ar.rows)return;
  ar.rows.splice(ri,1);
  s.sel=null;
  renderLayoutEditor(n);
}
// splitLayoutArea разрезает область a перед строкой ri на две: строки [0..ri)
// остаются в первой части, [ri..] переходят во вторую. Нужно, чтобы выделить
// из импортированной «Страница1» область-повтор (план 64, этап 6). Имена по
// умолчанию «<a>_1»/«<a>_2», но запрашиваются у пользователя.
function splitLayoutArea(n,a,ri){
  var s=_led[n];if(!s||!Array.isArray(s.data.areas))return;
  var idx=_ldAreaIndex(n,a);if(idx<0)return;
  var ar=s.data.areas[idx];
  var rows=ar.rows||[];
  if(ri<=0||ri>=rows.length){alert(T("Разрез возможен только перед строкой внутри области (не первой и не после последней)."));return;}
  var n1=prompt(T("Имя первой части (строки до разреза):"),a+'_1');
  if(n1===null)return; n1=n1.trim(); if(!n1)return;
  var n2=prompt(T("Имя второй части (строки от разреза):"),a+'_2');
  if(n2===null)return; n2=n2.trim(); if(!n2)return;
  if(_ldAreaIndex(n,n1)>=0&&n1!==a){alert(T("Область с таким именем уже есть")+': '+n1);return;}
  if(_ldAreaIndex(n,n2)>=0){alert(T("Область с таким именем уже есть")+': '+n2);return;}
  var top=rows.slice(0,ri), bottom=rows.slice(ri);
  ar.name=n1; ar.rows=top;
  s.data.areas.splice(idx+1,0,{name:n2,rows:bottom});
  s.sel=null;
  renderLayoutEditor(n);
}
// ── Ширины колонок / высоты строк (6.2) ───────────────────────────
// ldColWidth правит ГЛОБАЛЬНЫЙ массив columns: ширина i-й колонки макета.
function ldColWidth(n,i){
  var s=_ldEnsure(n);if(!s)return;
  if(!Array.isArray(s.data.columns))s.data.columns=[];
  while(s.data.columns.length<=i)s.data.columns.push({});
  var cur=s.data.columns[i].width||'';
  var v=prompt(T("Ширина колонки (напр. 120px, 30mm, пусто = авто). %-ширины печатью НЕ поддерживаются."),cur);
  if(v===null)return;
  v=v.trim();
  if(v.indexOf('%')>=0){alert(T("%-ширины не поддерживаются печатью (PDF). Используйте px или mm."));return;}
  if(v==='')delete s.data.columns[i].width; else s.data.columns[i].width=v;
  // подрезаем хвост пустых элементов columns
  while(s.data.columns.length&&!s.data.columns[s.data.columns.length-1].width)s.data.columns.pop();
  renderLayoutEditor(n);
}
// ldRowHeight правит высоту строки rows[ri].height.
function ldRowHeight(n,a,ri){
  var ar=_ldAreaByName(n,a);if(!ar||!ar.rows||!ar.rows[ri])return;
  var cur=ar.rows[ri].height||'';
  var v=prompt(T("Высота строки (напр. 24px, пусто = авто):"),cur);
  if(v===null)return;
  v=v.trim();
  if(v==='')delete ar.rows[ri].height; else ar.rows[ri].height=v;
  renderLayoutEditor(n);
}
// ── Toolbar operations ─────────────────────────────────────────────
function _ldSel(n){
  var s=_led[n];if(!s||!s.sel)return null;
  var ar=_ldAreaByName(n,s.sel.area);
  if(!ar||!ar.rows||!ar.rows[s.sel.row])return null;
  return {s:s,ar:ar,row:ar.rows[s.sel.row],ri:s.sel.row,ci:s.sel.col,area:s.sel.area};
}
function _ldFirstArea(n){
  var as=_ldAreas(n);
  return as.length?as[0].name:null;
}
function ldAddRow(n){
  var s=_led[n];if(!s)return;
  var area=s.sel?s.sel.area:_ldFirstArea(n);
  if(!area){alert(T("Сначала добавьте область"));return;}
  addLayoutRow(n,area);
}
function ldDelRow(n){
  var sel=_ldSel(n);
  if(!sel){alert(T("Выделите ячейку в строке, которую нужно удалить"));return;}
  if(!confirm(T("Удалить строку?")))return;
  delLayoutRow(n,sel.area,sel.ri);
}
function ldAddColumn(n){
  var s=_led[n];if(!s)return;
  var area=s.sel?s.sel.area:_ldFirstArea(n);
  if(!area){alert(T("Сначала добавьте область"));return;}
  var ar=_ldAreaByName(n,area);if(!ar||!ar.rows)return;
  for(var i=0;i<ar.rows.length;i++){
    if(!ar.rows[i].cells)ar.rows[i].cells=[];
    ar.rows[i].cells.push({});
  }
  // also extend columns array (column-level widths) if defined
  if(Array.isArray(s.data.columns)&&s.data.columns.length){s.data.columns.push({});}
  renderLayoutEditor(n);
}
function ldDelColumn(n){
  var sel=_ldSel(n);
  if(!sel){alert(T("Выделите ячейку в колонке, которую нужно удалить"));return;}
  if(!confirm(T("Удалить колонку?")))return;
  var s=sel.s,ar=sel.ar,ci=sel.ci;
  for(var i=0;i<ar.rows.length;i++){
    var cs=ar.rows[i].cells||[];
    if(ci<cs.length)cs.splice(ci,1);
  }
  if(Array.isArray(s.data.columns)&&ci<s.data.columns.length){s.data.columns.splice(ci,1);}
  s.sel=null;
  renderLayoutEditor(n);
}
function ldMerge(n){
  var sel=_ldSel(n);
  if(!sel){alert(T("Выделите ячейку, которую нужно объединить с правой соседкой"));return;}
  var ar=sel.ar,row=sel.row,ri=sel.ri,ci=sel.ci;
  if(ci+1>=row.cells.length){alert(T("Нет ячейки справа для объединения"));return;}
  var c=row.cells[ci];
  var span=(c.colspan&&c.colspan>1)?c.colspan:1;
  var rs=(c.rowspan&&c.rowspan>1)?c.rowspan:1;
  // Новая визуальная колонка, которую накроет расширенный colspan.
  var col=_ldVisualCol(ar,ri,ci);
  var newCol=col+span; // первая колонка, добавляемая к спану
  // Если ячейка многострочная (rowspan>1), расширение вправо накрывает newCol
  // и в каждой строке ниже (ri+1..ri+rs-1). Симметрично vertical-фиксу
  // (ldMergeDown): без удаления накрытая ячейка из строки ниже «всплыла» бы
  // вправо. Удаляем сначала нижние строки, потом соседа в текущей строке.
  for(var dr=rs-1;dr>=1;dr--){
    var di=_ldCellIndexAtCol(ar,ri+dr,newCol);
    if(di>=0)ar.rows[ri+dr].cells.splice(di,1);
  }
  c.colspan=span+1;
  row.cells.splice(ci+1,1);
  renderLayoutEditor(n);
}
function ldSplit(n){
  var sel=_ldSel(n);
  if(!sel){alert(T("Выделите объединённую ячейку"));return;}
  var c=sel.row.cells[sel.ci];
  if(!c.colspan||c.colspan<=1){alert(T("Ячейка не объединена"));return;}
  var span=c.colspan;
  delete c.colspan;
  // insert (span-1) empty cells to the right
  for(var i=0;i<span-1;i++){
    sel.row.cells.splice(sel.ci+1+i,0,{});
  }
  renderLayoutEditor(n);
}
// _ldColLayout раскладывает область по канону модели: для каждой строки строит
// массив map (cellIndex → начальная визуальная колонка) с учётом спанов выше
// и colspan внутри строки. Накрытые позиции в массиве cells ОТСУТСТВУЮТ (как в
// BuildAreaCells / declarative.go). Возвращает {starts:[[...]], covered:{}}.
function _ldColLayout(ar){
  var rows=(ar&&ar.rows)?ar.rows:[];
  var covered={};
  var starts=[];
  for(var r=0;r<rows.length;r++){
    var cells=rows[r].cells||[];
    var rowStarts=[];
    var col=0;
    for(var ci=0;ci<cells.length;ci++){
      while(covered[r+','+col])col++;
      rowStarts.push(col);
      var c=cells[ci]||{};
      var cs=(c.colspan&&c.colspan>1)?c.colspan:1;
      var rs=(c.rowspan&&c.rowspan>1)?c.rowspan:1;
      for(var dr=0;dr<rs;dr++)for(var dc=0;dc<cs;dc++){
        if(dr===0&&dc===0)continue;
        covered[(r+dr)+','+(col+dc)]=true;
      }
      col+=cs;
    }
    starts.push(rowStarts);
  }
  return {starts:starts,covered:covered};
}
// _ldVisualCol возвращает визуальную колонку выделенной ячейки (ci) в строке ri.
function _ldVisualCol(ar,ri,ci){
  var lay=_ldColLayout(ar);
  if(ri<lay.starts.length&&ci<lay.starts[ri].length)return lay.starts[ri][ci];
  return -1;
}
// _ldCellIndexAtCol находит индекс ячейки в строке ri, чья визуальная колонка == col.
// Возвращает -1, если в этой строке нет ячейки, начинающейся в col (позиция накрыта).
function _ldCellIndexAtCol(ar,ri,col){
  var lay=_ldColLayout(ar);
  if(ri>=lay.starts.length)return -1;
  var rs=lay.starts[ri];
  for(var i=0;i<rs.length;i++)if(rs[i]===col)return i;
  return -1;
}
// ── Удаление одиночной ячейки (5b блок A.1) ───────────────────────
// Удаляет выделенную ячейку из rows[ri].cells (соседи сдвигаются влево —
// семантика модели: колонки определяются порядком в массиве cells).
function ldDelCell(n){
  var sel=_ldSel(n);
  if(!sel){alert(T("Выделите ячейку для удаления"));return;}
  var cells=sel.row.cells||[];
  if(sel.ci>=cells.length)return;
  cells.splice(sel.ci,1);
  sel.s.sel=null;
  renderLayoutEditor(n);
}
// ── Вертикальный merge / unmerge (5b блок A.2) ────────────────────
// ldMergeDown увеличивает rowspan выделенной ячейки на 1 И удаляет ячейку,
// которая по канону модели стоит под ней в следующей строке (в той же
// визуальной колонке). Без удаления накрытая ячейка «всплыла» бы вправо.
function ldMergeDown(n){
  var sel=_ldSel(n);
  if(!sel){alert(T("Выделите ячейку, которую нужно объединить с нижней"));return;}
  var ar=sel.ar,ri=sel.ri,ci=sel.ci;
  var c=sel.row.cells[ci];
  var span=(c.rowspan&&c.rowspan>1)?c.rowspan:1;
  var nextRi=ri+span; // строка под нижней границей текущего спана
  if(nextRi>=ar.rows.length){alert(T("Нет строки снизу для объединения"));return;}
  var col=_ldVisualCol(ar,ri,ci);
  // удаляем ВСЕ ячейки строки nextRi, которые накроет спан: при colspan>1
  // накрывается несколько визуальных позиций (col..col+cs-1), иначе ячейка
  // из-под широкого спана «всплывала» со сдвигом вправо.
  var cs=(c.colspan&&c.colspan>1)?c.colspan:1;
  var toDelete=[];
  for(var dc=0;dc<cs;dc++){
    var di=_ldCellIndexAtCol(ar,nextRi,col+dc);
    if(di>=0&&toDelete.indexOf(di)<0)toDelete.push(di);
  }
  toDelete.sort(function(a,b){return b-a;});
  var nrCells=ar.rows[nextRi].cells||[];
  for(var d2=0;d2<toDelete.length;d2++)nrCells.splice(toDelete[d2],1);
  c.rowspan=span+1;
  renderLayoutEditor(n);
}
// ldUnmergeDown возвращает rowspan=1 и ВСТАВЛЯЕТ пустые ячейки обратно в строки,
// которые были накрыты (по канону модели — в нужную визуальную позицию).
function ldUnmergeVertical(n){
  var sel=_ldSel(n);
  if(!sel){alert(T("Выделите объединённую по вертикали ячейку"));return;}
  var ar=sel.ar,ri=sel.ri,ci=sel.ci;
  var c=sel.row.cells[ci];
  if(!c.rowspan||c.rowspan<=1){alert(T("Ячейка не объединена по вертикали"));return;}
  var span=c.rowspan;
  var cs=(c.colspan&&c.colspan>1)?c.colspan:1;
  var col=_ldVisualCol(ar,ri,ci);
  delete c.rowspan;
  // в каждую ранее накрытую строку вставляем пустую ячейку (с colspan, если был).
  for(var k=1;k<span;k++){
    var tr=ri+k;
    if(tr>=ar.rows.length)break;
    if(!ar.rows[tr].cells)ar.rows[tr].cells=[];
    // позиция вставки: индекс ячейки, чья визуальная колонка >= col (или конец).
    var lay=_ldColLayout(ar);
    var rowStarts=lay.starts[tr]||[];
    var insAt=rowStarts.length;
    for(var i=0;i<rowStarts.length;i++){if(rowStarts[i]>=col){insAt=i;break;}}
    var blank={};
    if(cs>1)blank.colspan=cs;
    ar.rows[tr].cells.splice(insAt,0,blank);
  }
  renderLayoutEditor(n);
}
// ── Границы по сторонам (6.3) ─────────────────────────────────────
// _ldBorderSides — стороны в порядке кнопок Л/В/П/Н → ключи borders.
var _ldBorderSides=['left','top','right','bottom'];
// ldToggleBorderSide включает/выключает сторону рамки выделенной ячейки;
// толщина берётся из select vp-bw. При установке per-side legacy border чистим.
function ldToggleBorderSide(n,side){
  var sc=_ldSelCell(n);if(!sc)return;
  var c=sc.c;
  if(!c.borders||typeof c.borders!=='object')c.borders={};
  var bw=document.getElementById('vp-bw-'+n);
  var w=(bw&&bw.value)?bw.value:'thin';
  if(c.borders[side]&&c.borders[side]!=='none')delete c.borders[side];
  else c.borders[side]=w;
  _ldNormalizeBorders(c);
  renderLayoutEditor(n);
}
// ldBorderPreset применяет пресет ко ВСЕМ сторонам текущей ячейки.
// kind: 'all' (все стороны), 'none' (убрать).
function ldBorderPreset(n,kind){
  var sc=_ldSelCell(n);if(!sc)return;
  var c=sc.c;
  if(kind==='none'){delete c.borders;delete c.border;renderLayoutEditor(n);return;}
  var bw=document.getElementById('vp-bw-'+n);
  var w=(bw&&bw.value)?bw.value:'thin';
  c.borders={left:w,top:w,right:w,bottom:w};
  delete c.border; // per-side имеет приоритет; чистим legacy для чистоты YAML
  renderLayoutEditor(n);
}
// ldBorderGridArea рисует сетку (все стороны) по всем ячейкам области (6.3).
function ldBorderGridArea(n){
  var s=_led[n];if(!s||!s.sel)return;
  var ar=_ldAreaByName(n,s.sel.area);if(!ar||!ar.rows)return;
  var bw=document.getElementById('vp-bw-'+n);
  var w=(bw&&bw.value)?bw.value:'thin';
  for(var ri=0;ri<ar.rows.length;ri++){
    var cells=ar.rows[ri].cells||[];
    for(var ci=0;ci<cells.length;ci++){
      cells[ci].borders={left:w,top:w,right:w,bottom:w};
      delete cells[ci].border;
    }
  }
  renderLayoutEditor(n);
}
// _ldNormalizeBorders убирает пустой объект borders.
function _ldNormalizeBorders(c){
  var b=c.borders;
  if(b&&typeof b==='object'){
    if(!b.left&&!b.top&&!b.right&&!b.bottom)delete c.borders;
    else{ // если per-side задан — чистим legacy
      delete c.border;
    }
  }
}
// _ldSyncBorderUI подсвечивает активные тоггл-кнопки сторон у выбранной ячейки.
function _ldSyncBorderUI(n,c){
  var b=(c&&c.borders&&typeof c.borders==='object')?c.borders:{};
  for(var i=0;i<_ldBorderSides.length;i++){
    var side=_ldBorderSides[i];
    var btn=document.getElementById('vp-bd-'+side+'-'+n);
    if(!btn)continue;
    var on=b[side]&&b[side]!=='none';
    btn.style.background=on?'#1a73e8':'#fff';
    btn.style.color=on?'#fff':'#334155';
  }
}
// Init layout editors on load
function initAllLayoutEditors(){
  console.log('[layout] initAllLayoutEditors called, jsyaml=', !!window.jsyaml, 'found', document.querySelectorAll('[id^="ta-mkt-"]').length, 'editors');
  var tas=document.querySelectorAll('[id^="ta-mkt-"]');
  for(var i=0;i<tas.length;i++){
    var n=tas[i].id.replace('ta-mkt-','');
    (function(nn){initLayoutEditor(nn);})(n);
  }
}
// js-yaml is embedded inline so always ready; wait for DOM
if(document.readyState==='loading'){
  document.addEventListener('DOMContentLoaded',initAllLayoutEditors);
}else{
  initAllLayoutEditors();
}

// ── HTML escape (shared) ────────────────────────────────────────
function esc(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}
// escJsAttr экранирует строку для безопасной вставки в одинарно-кавыченный
// JS-строковый литерал ВНУТРИ HTML-атрибута onclick="..." (двойные кавычки).
// Без этого апостроф в имени области/формы (имена приходят из prompt() и из
// ключей YAML — произвольный текст) рвёт JS-строку, а &/< — HTML-атрибут.
// Экранируем backslash, апостроф (для JS) и & < > " (для HTML-атрибута).
function escJsAttr(s){return String(s).replace(/\\/g,'\\\\').replace(/'/g,"\\'").replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}

// ── Syntax highlight ───────────────────────────────────────────
(function(){
var KW=['Процедура','КонецПроцедуры','Функция','КонецФункции',
  'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
  'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
  'Возврат','Прервать','Продолжить','Истина','Ложь','Неопределено','Новый',
  'И','ИЛИ','НЕ','Не',
  'Procedure','EndProcedure','Function','EndFunction',
  'If','Then','ElseIf','Else','EndIf',
  'For','Each','In','Do','EndDo','While','EndWhile',
  'Return','Break','Continue','True','False','Undefined','New',
  'And','Or','Not','Var'];
var FN=['Error','Ошибка','Сообщить','Формат','ФорматСтроки','СтрЗаменить'];
var SP=['this','Движения','Параметры'];
// DSL регистронезависим — подсветку ключевых слов сравниваем по нижнему регистру (issue #104).
var KWl=KW.map(function(s){return s.toLowerCase();});
var FNl=FN.map(function(s){return s.toLowerCase();});
var SPl=SP.map(function(s){return s.toLowerCase();});

function hl(code){
  var r='',i=0,n=code.length;
  while(i<n){
    if(code[i]==='/' && code[i+1]==='/'){
      var e=code.indexOf('\n',i);if(e<0)e=n;
      r+='<span class="hl-cmt">'+esc(code.slice(i,e))+'</span>';i=e;continue;
    }
    if(code[i]==='"'){
      var j=i+1;while(j<n && code[j]!=='"')j++;
      r+='<span class="hl-str">'+esc(code.slice(i,j+1))+'</span>';i=j+1;continue;
    }
    if(/[0-9]/.test(code[i])){
      var j=i;while(j<n && /[0-9.]/.test(code[j]))j++;
      r+='<span class="hl-num">'+esc(code.slice(i,j))+'</span>';i=j;continue;
    }
    if(/[а-яёА-ЯЁa-zA-Z_]/.test(code[i])){
      var j=i;while(j<n && /[а-яёА-ЯЁa-zA-Z0-9_]/.test(code[j]))j++;
      var w=code.slice(i,j),wl=w.toLowerCase();
      if(KWl.indexOf(wl)>=0)r+='<span class="hl-kw">'+esc(w)+'</span>';
      else if(FNl.indexOf(wl)>=0)r+='<span class="hl-fn">'+esc(w)+'</span>';
      else if(SPl.indexOf(wl)>=0)r+='<span class="hl-sp">'+esc(w)+'</span>';
      else r+=esc(w);
      i=j;continue;
    }
    r+=esc(code[i]);i++;
  }
  return r;
}
document.querySelectorAll('pre.os-code').forEach(function(el){
  el.innerHTML=hl(el.textContent);
});
})();

// ── Monaco Editor initialization ────────────────────────────────
(function(){
if (typeof require === 'undefined') { window._monacoReady = false; document.getElementById('monaco-status').textContent='Monaco:FAIL(no require)'; return; }
require.config({ paths: { 'vs': '/vendor/monaco/vs' }});
require(['vs/editor/editor.main'], function() {
  // Единый загрузчик справочника языка (кешируется; используется провайдерами и окном-справочником)
  window._langref = window._langref || null;
  window._langrefPromise = window._langrefPromise || null;
  function loadLangref() {
    if (window._langref) return Promise.resolve(window._langref);
    if (window._langrefPromise) return window._langrefPromise;
    window._langrefPromise = fetch('/bases/' + _dbgBase + '/configurator/langref')
      .then(function(r){ return r.json(); })
      .then(function(d){ window._langref = d || []; return window._langref; })
      .catch(function(){ window._langref = []; return window._langref; });
    return window._langrefPromise;
  }
  window.loadLangref = loadLangref; // доступ из окна-справочника (отдельный <script>-скоуп)
  // Register OneBase DSL language
  monaco.languages.register({ id: 'onebase-dsl' });
  function _onebaseMonarch(extraBuiltins){
    var builtins = ['Error','Ошибка','Сообщить','Формат','ФорматСтроки','СтрЗаменить',
      'Запрос','Результат','Выполнить','УстановитьПараметр','Текст',
      'ВЫБРАТЬ','ИЗ','ГДЕ','УПОРЯДОЧИТЬ','ПО','СГРУППИРОВАТЬ',
      'ЛЕВОЕ','ПРАВОЕ','ВНУТРЕННЕЕ','ПОЛНОЕ','СОЕДИНЕНИЕ',
      'КАК','ВОЗР','УБЫВ','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ'].concat(extraBuiltins||[]);
    return {
      // DSL регистронезависим — подсветка ключевых слов тоже (issue #104).
      ignoreCase: true,
      keywords: [
        'Процедура','КонецПроцедуры','Функция','КонецФункции',
        'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
        'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
        'Возврат','Прервать','Продолжить','Истина','Ложь','Неопределено','Новый',
        'И','ИЛИ','НЕ','Не','Перем',
        'Procedure','EndProcedure','Function','EndFunction',
        'If','Then','ElseIf','Else','EndIf',
        'For','Each','In','Do','EndDo','While','EndWhile',
        'Return','Break','Continue','True','False','Undefined','New',
        'And','Or','Not','Var'
      ],
      builtins: builtins,
      special: ['this','ЭтотОбъект','Движения','Параметры'],
      tokenizer: {
        root: [
          [/#.*$/, 'comment'],
          ["\/\/.*$", 'comment'],
          [/"/, 'string', '@string'],
          [/\d+(\.\d+)?/, 'number'],
          [/[a-zA-Z_А-яЁё][a-zA-Z0-9_А-яЁё]*/, {
            cases: { '@keywords': 'keyword', '@builtins': 'type', '@special': 'variable.predefined', '@default': 'identifier' }
          }]
        ],
        string: [ [/[^"]+/, 'string'], [/"/, 'string', '@pop'] ]
      }
    };
  }
  monaco.languages.setMonarchTokensProvider('onebase-dsl', _onebaseMonarch());
  // Иконка по виду дескриптора
  function _lrKind(kind){
    var K = monaco.languages.CompletionItemKind;
    if (kind === 'method') return K.Method;
    if (kind === 'keyword') return K.Keyword;
    if (kind === 'query') return K.Struct;
    return K.Function;
  }
  // Огороженный markdown-блок bsl (символ ограждения собираем из кода 96,
  // чтобы не вставлять его литерал в Go raw-string шаблона).
  var _bt = String.fromCharCode(96);
  function _lrFence(code){
    return '\n\n' + _bt + _bt + _bt + 'bsl\n' + code + '\n' + _bt + _bt + _bt;
  }
  // Сниппет вставки: Имя(${1:П1}, ${2:П2})
  function _lrSnippet(d){
    if (d.snippet) return d.snippet; // готовый шаблон конструкции (Процедура…, Если… и т.п.) — issue #105
    if (!d.params || !d.params.length) return d.display;
    var parts = d.params.map(function(p,i){ return '${'+(i+1)+':'+p.name+'}'; });
    return d.display + '(' + parts.join(', ') + ')';
  }
  // Auto-completion (данные из langref, лениво)
  monaco.languages.registerCompletionItemProvider('onebase-dsl', {
    triggerCharacters: ['.'],
    provideCompletionItems: function(model, position) {
      loadLangref();
      var data = window._langref || [];
      var word = model.getWordUntilPosition(position);
      var range = { startLineNumber: position.lineNumber, endLineNumber: position.lineNumber, startColumn: word.startColumn, endColumn: word.endColumn };
      var line = model.getLineContent(position.lineNumber).substring(0, word.startColumn - 1);
      var dot = /([A-Za-zА-Яа-яЁё0-9_]+)\.\s*$/.exec(line);
      var obj = dot ? dot[1].toLowerCase() : null;
      var objExists = obj && data.some(function(d){ return d.kind==='method' && d.object && d.object.toLowerCase()===obj; });
      var suggestions = [];
      data.forEach(function(d){
        if (objExists && !(d.kind==='method' && d.object && d.object.toLowerCase()===obj)) return;
        suggestions.push({
          label: d.display,
          kind: _lrKind(d.kind),
          detail: d.signature || '',
          documentation: { value: (d.doc||'') + (d.example ? _lrFence(d.example) : '') },
          insertText: _lrSnippet(d),
          insertTextRules: (d.snippet || (d.params && d.params.length)) ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
          range: range
        });
      });
      return { suggestions: suggestions };
    }
  });
  // Hover — описание под курсором
  monaco.languages.registerHoverProvider('onebase-dsl', {
    provideHover: function(model, position) {
      loadLangref();
      var word = model.getWordAtPosition(position);
      if (!word) return null;
      var w = word.word.toLowerCase();
      var data = window._langref || [];
      var d = null;
      for (var i=0;i<data.length;i++){
        var x=data[i];
        if (x.name && x.name.toLowerCase()===w){ d=x; break; }
        if (x.aliases && x.aliases.some(function(a){return a.toLowerCase()===w;})){ d=x; break; }
      }
      if (!d) return null;
      var md = '**' + (d.signature||d.display) + '**\n\n' + (d.doc||'');
      if (d.returns) md += '\n\n_Возвращает:_ ' + d.returns;
      if (d.example) md += _lrFence(d.example);
      return { contents: [{ value: md }] };
    }
  });
  // Подсказка параметров
  monaco.languages.registerSignatureHelpProvider('onebase-dsl', {
    signatureHelpTriggerCharacters: ['(', ','],
    provideSignatureHelp: function(model, position) {
      loadLangref();
      var textBefore = model.getValueInRange({startLineNumber:1,startColumn:1,endLineNumber:position.lineNumber,endColumn:position.column});
      var m = /([A-Za-zА-Яа-яЁё0-9_]+)\s*\(([^()]*)$/.exec(textBefore);
      if (!m) return null;
      var name = m[1].toLowerCase();
      var data = window._langref || [];
      var d = null;
      for (var i=0;i<data.length;i++){
        var x=data[i];
        if ((x.name && x.name.toLowerCase()===name) || (x.aliases && x.aliases.some(function(a){return a.toLowerCase()===name;}))){ d=x; break; }
      }
      if (!d || !d.params || !d.params.length) return null;
      var active = (m[2].match(/,/g) || []).length;
      return { value: { signatures: [{
        label: d.signature || d.display,
        documentation: d.doc || '',
        parameters: d.params.map(function(p){ return { label: p.name, documentation: (p.type||'') + (p.doc ? ' — '+p.doc : '') }; })
      }], activeSignature: 0, activeParameter: Math.min(active, d.params.length-1) }, dispose: function(){} };
    }
  });
  // Dark theme matching existing #1e1e2e style
  monaco.editor.defineTheme('onebase-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'keyword', foreground: 'c792ea', fontStyle: 'bold' },
      { token: 'type', foreground: '82aaff' },
      { token: 'variable.predefined', foreground: 'ff5370', fontStyle: 'bold' },
      { token: 'string', foreground: 'c3e88d' },
      { token: 'number', foreground: 'f78c6c' },
      { token: 'comment', foreground: '546e7a', fontStyle: 'italic' }
    ],
    colors: {
      'editor.background': '#1e1e2e',
      'editor.foreground': '#cdd6f4',
      'editor.lineHighlightBackground': '#2a2a3e',
      'editorLineNumber.foreground': '#6c7086',
      'editorLineNumber.activeForeground': '#cdd6f4',
      'editor.selectionBackground': '#45475a',
      'editorCursor.foreground': '#f5e0dc'
    }
  });
  window._monacoReady = true;

  // Динамическая подсветка: дополнить список встроенных именами из langref
  loadLangref().then(function(data){
    var names = data.filter(function(d){ return d.kind==='func' || d.kind==='method' || d.kind==='query'; })
                    .map(function(d){ return d.display; });
    monaco.languages.setMonarchTokensProvider('onebase-dsl', _onebaseMonarch(names));
  });

  // Auto-open Monaco for visible code blocks
  document.querySelectorAll('pre.os-code').forEach(function(pre) {
    var name = pre.id.replace('pre-','');
    if (name) startEdit(name);
  });
  var ms = document.getElementById('monaco-status');
  if (ms) { ms.textContent = 'Monaco:OK(' + Object.keys(monacoEditors).length + ' ed)'; ms.style.color = '#16a34a'; }
});
})();

// ── Form submit: sync Monaco -> textarea ─────────────────────────
document.querySelectorAll('form').forEach(function(form) {
  form.addEventListener('submit', function() {
    form.querySelectorAll('.code-wrap').forEach(function(wrap) {
      var editorDiv = wrap.querySelector('.monaco-target');
      if (editorDiv && editorDiv._editorId && monacoEditors[editorDiv._editorId]) {
        var ta = wrap.querySelector('textarea.os-edit');
        if (ta) ta.value = monacoEditors[editorDiv._editorId].getValue();
      }
    });
  });
});

// ── Report params ──────────────────────────────────────────────
function repReindex(tableId) {
  var tbl = document.getElementById(tableId);
  if (!tbl) return;
  var rows = tbl.querySelectorAll('tbody tr, tr:not(:first-child)');
  // skip header row (first tr), iterate data rows
  var dataRows = Array.from(tbl.querySelectorAll('tr')).filter(function(r){ return r.querySelector('input[type=text]'); });
  dataRows.forEach(function(tr, i) {
    tr.querySelectorAll('input,select').forEach(function(el) {
      el.name = el.name.replace(/param\.\d+\./, 'param.' + i + '.');
    });
    var btn = tr.querySelector('button[type=button]');
    if (btn) btn.setAttribute('onclick', 'this.closest(\'tr\').remove();repReindex(\'' + tableId + '\')');
  });
}
// ── Enum values add/remove/reindex ────────────────────────────────────────────
function enumReindex(containerId) {
  var c = document.getElementById(containerId);
  if (!c) return;
  var rows = c.querySelectorAll('.enum-val-row');
  rows.forEach(function(row, i) {
    row.querySelectorAll('input,select').forEach(function(el) {
      if (el.name) {
        el.name = el.name.replace(/value\.\d+\./, 'value.' + i + '.');
      }
    });
    var btn = row.querySelector('button[type=button]');
    if (btn) btn.setAttribute('onclick', 'enumRemoveVal(this,\'' + containerId + '\')');
  });
}
function enumRemoveVal(btn, containerId) {
  btn.closest('.enum-val-row').remove();
  enumReindex(containerId);
}
function enumAddVal(containerId) {
  var c = document.getElementById(containerId);
  if (!c) return;
  var i = c.querySelectorAll('.enum-val-row').length;
  var div = document.createElement('div');
  div.className = 'enum-val-row';
  div.style.cssText = 'display:flex;gap:6px;align-items:flex-start;margin-bottom:4px';
  div.innerHTML = '<div style="flex:1"><input type="text" name="value.' + i + '.name" value=""'
    + ' style="width:100%;padding:4px 6px;border:1px solid #cbd5e1;border-radius:4px;font-size:13px"'
    + ' placeholder="ИмяЗначения"></div>'
    + '<button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:16px;padding:2px 4px;flex-shrink:0"'
    + ' onclick="enumRemoveVal(this,\'' + containerId + '\')">✕</button>';
  c.appendChild(div);
  div.querySelector('input[type=text]').focus();
}

function repAddParam(tableId) {
  var tbl = document.getElementById(tableId);
  if (!tbl) return;
  var dataRows = Array.from(tbl.querySelectorAll('tr')).filter(function(r){ return r.querySelector('input[type=text]'); });
  var i = dataRows.length;
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input type="text" name="param.' + i + '.name" value="" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px" placeholder="ИмяПараметра"></td>'
    + '<td><select name="param.' + i + '.type" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">'
    + '<option value="string">'+T("строка")+'</option><option value="date">'+T("дата")+'</option><option value="number">'+T("число")+'</option><option value="select">'+T("список")+'</option>'
    + window.__cfg.entityNames.map(function(n){return '<option value="reference:'+n+'">ссылка: '+n+'</option>';}).join('')
    + '</select></td>'
    + '<td><input type="text" name="param.' + i + '.label" value="" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px" placeholder="Заголовок"></td>'
    + '<td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest(\'tr\').remove();repReindex(\'' + tableId + '\')">✕</button></td>';
  tbl.appendChild(tr);
  tr.querySelector('input[type=text]').focus();
}

// ── Конструктор компоновки: динамические строки вкладки «Структура» (план 59) ──
function compAddRow(id,prefix){var t=document.getElementById(id);var i=t.rows.length;var tr=t.insertRow();
  tr.innerHTML='<td><input type="text" name="'+prefix+i+'" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>'+
  '<td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest(\'tr\').remove();compReindex(\''+id+'\',\''+prefix+'\')">✕</button></td>';}
function compReindex(id,prefix){var t=document.getElementById(id);for(var i=0;i<t.rows.length;i++){var inp=t.rows[i].querySelector('input,select');if(inp)inp.name=prefix+i;}}
function compAddMeasure(id){var t=document.getElementById(id);var i=0;for(var r=0;r<t.rows.length;r++){if(t.rows[r].querySelectorAll('input,select').length>=2)i++;}var tr=t.insertRow();
  tr.innerHTML='<td><input type="text" name="comp.measure.'+i+'.field" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>'+
  '<td><select name="comp.measure.'+i+'.agg" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"><option>sum</option><option>count</option><option>avg</option><option>min</option><option>max</option></select></td>'+
  '<td><input type="text" name="comp.measure.'+i+'.title" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>'+
  '<td><select name="comp.measure.'+i+'.align" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"><option value="">—</option><option value="left">влево</option><option value="right">вправо</option><option value="center">по центру</option></select></td>'+
  '<td><input type="text" name="comp.measure.'+i+'.format" placeholder="#,##0.00" style="width:80px;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>'+
  '<td><input type="text" name="comp.measure.'+i+'.expr" placeholder="ВаловаяПрибыль / Выручка * 100" title="выражение по другим показателям, напр. ВаловаяПрибыль / Выручка * 100" style="width:160px;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>'+
  '<td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest(\'tr\').remove();compReindexMeasure(\''+id+'\')">✕</button></td>';}
function compReindexMeasure(id){var t=document.getElementById(id);var k=0;for(var r=0;r<t.rows.length;r++){var ins=t.rows[r].querySelectorAll('input,select');if(ins.length<2)continue;ins[0].name='comp.measure.'+k+'.field';ins[1].name='comp.measure.'+k+'.agg';if(ins[2])ins[2].name='comp.measure.'+k+'.title';if(ins[3])ins[3].name='comp.measure.'+k+'.align';if(ins[4])ins[4].name='comp.measure.'+k+'.format';if(ins[5])ins[5].name='comp.measure.'+k+'.expr';k++;}}
function compAddSort(id){var t=document.getElementById(id);var i=t.rows.length;var tr=t.insertRow();
  tr.innerHTML='<td><input type="text" name="comp.sort.'+i+'.field" style="width:100%"></td>'+
  '<td><select name="comp.sort.'+i+'.dir"><option>asc</option><option>desc</option></select></td>'+
  '<td><button type="button" onclick="this.closest(\'tr\').remove();compReindexSort(\''+id+'\')">✕</button></td>';}
function compReindexSort(id){var t=document.getElementById(id);var k=0;for(var r=0;r<t.rows.length;r++){var ins=t.rows[r].querySelectorAll('input,select');if(ins.length<2)continue;ins[0].name='comp.sort.'+k+'.field';ins[1].name='comp.sort.'+k+'.dir';k++;}}
function compAddCond(id){var t=document.getElementById(id);var i=0;for(var r=0;r<t.rows.length;r++){if(t.rows[r].querySelector('input'))i++;}var tr=t.insertRow();
  tr.innerHTML='<td><input type="text" name="comp.cond.'+i+'.when" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>'+
  '<td><input type="text" name="comp.cond.'+i+'.field" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>'+
  '<td><input type="color" name="comp.cond.'+i+'.color" value="#000000"></td>'+
  '<td><input type="color" name="comp.cond.'+i+'.background" value="#ffffff"></td>'+
  '<td><input type="checkbox" name="comp.cond.'+i+'.bold"></td>'+
  '<td><input type="checkbox" name="comp.cond.'+i+'.italic"></td>'+
  '<td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest(\'tr\').remove();compReindexCond(\''+id+'\')">&#x2715;</button></td>';}
function compReindexCond(id){var t=document.getElementById(id);var k=0;for(var r=0;r<t.rows.length;r++){var ins=t.rows[r].querySelectorAll('input');if(ins.length<2)continue;ins[0].name='comp.cond.'+k+'.when';ins[1].name='comp.cond.'+k+'.field';ins[2].name='comp.cond.'+k+'.color';ins[3].name='comp.cond.'+k+'.background';ins[4].name='comp.cond.'+k+'.bold';ins[5].name='comp.cond.'+k+'.italic';k++;}}

// ── Editor context menu — наше меню для textarea, pre и Monaco ──
// Monaco создан с contextmenu:false, поэтому его внутренний обработчик не глотает событие.
(function(){
var _cTA=null,_cMonacoName=null,_cM=null,_cSelText='';
document.addEventListener('contextmenu',function(e){
  var ta=e.target.closest('.os-edit');
  var pre=e.target.closest('pre.os-code');
  var mon=e.target.closest('.monaco-target');
  if(!ta&&!pre&&!mon){hideC();return;}
  e.preventDefault();
  _cMonacoName=null; _cTA=null; _cSelText='';
  // Выделение фиксируем СРАЗУ, в момент ПКМ: Monaco сбрасывает его на
  // mousedown, а startEdit переключает pre→textarea (выделение было в pre).
  if(mon){
    _cMonacoName=mon._editorId||null;
    var ed=_cMonacoName?monacoEditors[_cMonacoName]:null;
    if(ed){
      var mt=ed.getModel().getValueInRange(ed.getSelection());
      _cSelText=(mt&&mt.trim())?mt:(ed._lastSelText||'');
    }
  } else if(pre&&!ta){
    // выделение сейчас в <pre> — забираем DOM-selection ДО startEdit
    _cSelText=(window.getSelection?String(window.getSelection()):'')||'';
    var nm=pre.id.replace('pre-','');startEdit(nm);ta=document.getElementById('ta-'+nm);
    _cTA=ta;
  } else {
    _cTA=ta;
    if(ta)_cSelText=ta.value.substring(ta.selectionStart,ta.selectionEnd);
  }
  showC(e.clientX,e.clientY);
});
function showC(x,y){
  if(!_cM){_cM=document.createElement('div');_cM.className='cfg-ctx-menu';
    _cM.innerHTML='<div class="cfg-ctx-item" onclick="cfgOpenQB()">🔍 Конструктор запроса</div>';
    document.body.appendChild(_cM);}
  _cM.style.display='block';
  _cM.style.left=Math.min(x,window.innerWidth-240)+'px';
  _cM.style.top=Math.min(y,window.innerHeight-50)+'px';
}
function hideC(){if(_cM)_cM.style.display='none';}
document.addEventListener('click',hideC);
window.cfgOpenQB=function(){
  hideC();
  if(_cMonacoName && monacoEditors[_cMonacoName]) openQBModalMonaco(_cMonacoName,_cSelText);
  else if(_cTA) openQBModal(_cTA,_cSelText);
};
})();

// ── Inline query builder ──────────────────────────────────
var _mqbTA=null,_mqbMonacoId=null,_mqbSchema=null,_mqbSrcMap={};
var _mqbCurFields=[],_mqbSel={},_mqbJoins=[];
(function(){
_mqbSchema = window.__qbSchema;
if(!_mqbSchema)_mqbSchema=[];
_mqbSchema.forEach(function(s){_mqbSrcMap[s.id]=s;});
// populate select
var sel=document.getElementById('mqb-src');
var groups={};
_mqbSchema.forEach(function(s){if(!groups[s.group])groups[s.group]=[];groups[s.group].push(s);});
Object.keys(groups).forEach(function(g){
  var og=document.createElement('optgroup');og.label=g;
  groups[g].forEach(function(s){var o=document.createElement('option');o.value=s.id;o.textContent=s.label;og.appendChild(o);});
  sel.appendChild(og);
});
document.getElementById('qb-close').onclick=function(){document.getElementById('qb-overlay').classList.remove('active');};
var _qbAiGen=document.getElementById('qb-ai-gen');
if(_qbAiGen)_qbAiGen.onclick=function(){
  var desc=document.getElementById('qb-ai-desc').value.trim();if(!desc)return;
  var btn=this;btn.disabled=true;var old=btn.textContent;btn.textContent='...';
  fetch('/bases/'+_dbgBase+'/configurator/ai-query',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({description:desc})})
    .then(function(r){return r.json();})
    .then(function(d){
      if(d&&d.ok&&d.query){document.getElementById('mqb-qry').value=d.query;document.getElementById('qb-mode').value='query';}
      else{alert((d&&d.error)||'Ошибка генерации запроса');}
    })
    .catch(function(){alert('Ошибка сети');})
    .finally(function(){btn.disabled=false;btn.textContent=old;});
};
document.getElementById('qb-insert').onclick=function(){
  var mode=document.getElementById('qb-mode').value;
  var txt=mode==='query'?document.getElementById('mqb-qry').value:document.getElementById('mqb-dsl').value;
  if(!txt)return;
  if(_mqbMonacoId && monacoEditors[_mqbMonacoId]){
    var editor=monacoEditors[_mqbMonacoId];
    var sel=editor.getSelection();
    editor.executeEdits('qb',[{range:sel,text:txt}]);
    _mqbMonacoId=null;
  } else if(_mqbTA){
    var ta=_mqbTA,s=ta.selectionStart,en=ta.selectionEnd;
    ta.value=ta.value.substring(0,s)+txt+ta.value.substring(en);
    var nm=ta.id.replace('ta-',''),pre=document.getElementById('pre-'+nm);
    if(pre)pre.innerHTML=hl(ta.value);
    ta.selectionStart=ta.selectionEnd=s+txt.length;
    ta.focus();
  }
  _mqbTA=null;
  document.getElementById('qb-overlay').classList.remove('active');
};
// close on overlay click
document.getElementById('qb-overlay').addEventListener('click',function(e){if(e.target===this)this.classList.remove('active');});
})();

function openQBModal(ta,presetSel){
  _mqbTA=ta;
  // reset state
  _mqbSel={};_mqbJoins=[];_mqbCurFields=[];
  document.getElementById('mqb-src').value='';
  document.getElementById('mqb-alias').value='';
  document.getElementById('mqb-vtp').style.display='none';
  document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">'+T("Нет")+'</p>';
  document.getElementById('mqb-conds').innerHTML='';
  document.getElementById('mqb-ords').innerHTML='';
  document.getElementById('mqb-fields').innerHTML='';
  document.getElementById('mqb-dsl').value='';
  document.getElementById('mqb-qry').value='';
  // parse selected text — приоритет у выделения, снятого в момент ПКМ
  var sel=(presetSel!=null&&String(presetSel).trim())
    ?String(presetSel).trim()
    :ta.value.substring(ta.selectionStart,ta.selectionEnd).trim();
  if(sel){
    var q=qbExtractQuery(sel);
    if(q){document.getElementById('mqb-qry').value=q;qbParseToFields(q);}
  }
  document.getElementById('qb-overlay').classList.add('active');
}
function openQBModalMonaco(editorId,presetSel){
  var editor = monacoEditors[editorId];
  if (!editor) return;
  _mqbTA = null;
  _mqbMonacoId = editorId;
  _mqbSel={};_mqbJoins=[];_mqbCurFields=[];
  document.getElementById('mqb-src').value='';
  document.getElementById('mqb-alias').value='';
  document.getElementById('mqb-vtp').style.display='none';
  document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">'+T("Нет")+'</p>';
  document.getElementById('mqb-conds').innerHTML='';
  document.getElementById('mqb-ords').innerHTML='';
  document.getElementById('mqb-fields').innerHTML='';
  document.getElementById('mqb-dsl').value='';
  document.getElementById('mqb-qry').value='';
  // Приоритет — выделение, снятое в момент ПКМ; затем текущее; затем
  // последнее запомненное (правый клик мог сбросить selection).
  var sel = (presetSel!=null&&String(presetSel).trim())?String(presetSel).trim():'';
  if(!sel) sel = editor.getModel().getValueInRange(editor.getSelection()).trim();
  if(!sel && editor._lastSelText) sel = editor._lastSelText.trim();
  if(sel){
    var q=qbExtractQuery(sel);
    if(q){document.getElementById('mqb-qry').value=q;qbParseToFields(q);}
  }
  document.getElementById('qb-overlay').classList.add('active');
}
function qbExtractQuery(text){
  // 1С-style: Запрос.Текст = "ВЫБРАТЬ ... |..."
  var m=text.match(/Текст\s*=\s*\n?\s*"([\s\S]*?)"/i);
  if(m)return m[1].replace(/\n\s*\|/g,'\n').trim();
  // прямой текст запроса. \b в JS не учитывает кириллицу (\w = [A-Za-z0-9_]),
  // поэтому границу слова проверяем явным символьным классом.
  var sm=text.match(/(^|[^A-Za-zА-Яа-яЁё0-9_])ВЫБРАТЬ(?=[^A-Za-zА-Яа-яЁё0-9_]|$)/i);
  if(sm){
    var i=sm.index+sm[1].length;
    return text.substring(i).replace(/\n\s*\|/g,'\n').replace(/^"|"$/g,'').trim();
  }
  return null;
}

// Ищет в _mqbSchema источник по совпадению label-prefix (до '('), регистронезависимо.
function qbFindSource(name){
  var key=String(name||'').split('(')[0].trim().toLowerCase();
  var found=null;
  _mqbSchema.forEach(function(s){
    if(s.label.split('(')[0].toLowerCase()===key)found=s;
  });
  return found;
}

// Разбивает строку по разделителю, игнорируя содержимое скобок.
// \w в JS НЕ включает кириллицу — поэтому проверка границ слов сделана через qbIsWord.
function qbIsWord(ch){return /[A-Za-zА-Яа-яЁё0-9_]/.test(ch);}
function qbSplitTopLevel(s,sep){
  var out=[],depth=0,buf='';
  var sepU=sep.toUpperCase();
  for(var i=0;i<s.length;i++){
    var ch=s[i];
    if(ch==='(')depth++;
    else if(ch===')')depth--;
    if(depth===0 && s.substring(i,i+sep.length).toUpperCase()===sepU &&
       (i===0||!qbIsWord(s[i-1])) && (i+sep.length===s.length||!qbIsWord(s[i+sep.length]))){
      out.push(buf); buf=''; i+=sep.length-1; continue;
    }
    buf+=ch;
  }
  if(buf.length)out.push(buf);
  return out;
}

function qbParseToFields(q){
  try { return qbParseToFieldsImpl(q); }
  catch(err){ if(window.console)console.warn('[QB] parse failed:',err,'\nquery:',q); }
}

function qbParseToFieldsImpl(q){
  q=q.replace(/\r/g,'').trim();

  // Разбить запрос на секции по ключевым словам верхнего уровня.
  var kwRe=/(^|[^\wА-Яа-яЁё])(ВЫБРАТЬ|ИЗ|ГДЕ|СГРУППИРОВАТЬ\s+ПО|УПОРЯДОЧИТЬ\s+ПО)(?=[^\wА-Яа-яЁё]|$)/gi;
  var marks=[],mk;
  while((mk=kwRe.exec(q))){
    marks.push({k:mk[2].toUpperCase().replace(/\s+/g,' '),i:mk.index+mk[1].length,l:mk[2].length});
  }
  function section(name){
    for(var i=0;i<marks.length;i++){
      if(marks[i].k===name){
        var st=marks[i].i+marks[i].l;
        var en=(i+1<marks.length)?marks[i+1].i:q.length;
        return q.substring(st,en).trim();
      }
    }
    return '';
  }
  var selSec=section('ВЫБРАТЬ');
  var fromSec=section('ИЗ');
  var whereSec=section('ГДЕ');
  var orderSec=section('УПОРЯДОЧИТЬ ПО');
  if(!fromSec){if(window.console)console.warn('[QB] no ИЗ section in:',q);return;}

  // FROM: <source>[(...)] [КАК <alias>] [JOIN ...]
  var joinRe=/(^|[^\wА-Яа-яЁё])(ЛЕВОЕ|ПРАВОЕ|ВНУТРЕННЕЕ|ПОЛНОЕ)\s+СОЕДИНЕНИЕ\b/i;
  var firstJ=fromSec.search(joinRe);
  var mainFrom=(firstJ>=0?fromSec.substring(0,firstJ):fromSec).trim();
  var rest=(firstJ>=0?fromSec.substring(firstJ):'').trim();
  var mm=mainFrom.match(/^(\S+(?:\([^)]*\))?)\s*(?:КАК\s+(\S+))?/i);
  if(!mm){if(window.console)console.warn('[QB] cannot parse FROM:',mainFrom);return;}
  var src=qbFindSource(mm[1]);
  if(!src){if(window.console)console.warn('[QB] unknown source:',mm[1]);return;}

  // 1) Выбираем источник (mqbSetSrc сбросит alias, vt и поля без префикса)
  document.getElementById('mqb-src').value=src.id;
  mqbSetSrc(src.id);
  // 2) Алиас и параметр виртуальной таблицы
  if(mm[2])document.getElementById('mqb-alias').value=mm[2];
  var vtm=mm[1].match(/\(([^)]*)\)/);
  if(vtm && src.vtParam){document.getElementById('mqb-vtpv').value=vtm[1];}

  // 3) JOINs — добавляем все, заполняем поля
  if(rest){
    var jRe=/(?:^|[^\wА-Яа-яЁё])(ЛЕВОЕ|ПРАВОЕ|ВНУТРЕННЕЕ|ПОЛНОЕ)\s+СОЕДИНЕНИЕ\s+(\S+(?:\([^)]*\))?)\s+КАК\s+(\S+)\s+ПО\s+([\s\S]*?)(?=(?:^|[^\wА-Яа-яЁё])(?:ЛЕВОЕ|ПРАВОЕ|ВНУТРЕННЕЕ|ПОЛНОЕ)\s+СОЕДИНЕНИЕ\b|$)/gi;
    var jm;
    while((jm=jRe.exec(rest))){
      mqbAddJoin();
      var lastJ=_mqbJoins[_mqbJoins.length-1];
      lastJ.typeSel.value=jm[1].toUpperCase();
      var jSrc=qbFindSource(jm[2]);
      if(jSrc)lastJ.srcSel.value=jSrc.id;
      lastJ.aliasInp.value=jm[3];
      lastJ.onInp.value=jm[4].trim();
    }
  }

  // 4) Пересобрать список полей с учётом алиаса и JOINов
  mqbRebuild();

  // 5) SELECT — заменить автозаполненный _mqbSel выделением из запроса
  if(selSec && selSec!=='*'){
    var newSel={};
    qbSplitTopLevel(selSec,',').forEach(function(fld){
      fld=fld.trim(); if(!fld||fld==='*')return;
      var agg='',alias='',name=fld;
      var aggM=fld.match(/^(СУММА|КОЛИЧЕСТВО|МИНИМУМ|МАКСИМУМ|СРЕДНЕЕ)\s*\(([\s\S]+)\)\s*(?:КАК\s+(\S+))?\s*$/i);
      if(aggM){agg=aggM[1].toUpperCase();name=aggM[2].trim();if(aggM[3])alias=aggM[3];}
      else{
        var aliasM=fld.match(/^([\s\S]+?)\s+КАК\s+(\S+)\s*$/i);
        if(aliasM){name=aliasM[1].trim();alias=aliasM[2];}
      }
      newSel[name]={alias:alias,agg:agg};
    });
    if(Object.keys(newSel).length){_mqbSel=newSel; mqbRenderFields();}
  }

  // 6) WHERE
  if(whereSec){
    qbSplitTopLevel(whereSec,'И').forEach(function(c){
      c=c.trim(); if(!c)return;
      mqbAddCond();
      var rows=document.querySelectorAll('#mqb-conds > div');
      var row=rows[rows.length-1];
      var sels=row.querySelectorAll('select');
      var inp=row.querySelector('input[type=text]');
      var em=c.match(/^([\s\S]+?)\s+(НЕ\s+ЕСТЬ\s+ПУСТО|ЕСТЬ\s+ПУСТО)\s*$/i);
      if(em){
        sels[0].value=em[1].trim();
        sels[1].value=em[2].toUpperCase().replace(/\s+/g,' ');
        inp.style.display='none';
        return;
      }
      var im=c.match(/^([\s\S]+?)\s+В\s*\(([\s\S]+)\)\s*$/i);
      if(im){sels[0].value=im[1].trim();sels[1].value='В';inp.value=im[2].trim();return;}
      var pm=c.match(/^([\s\S]+?)\s+ПОДОБНО\s+([\s\S]+)$/i);
      if(pm){sels[0].value=pm[1].trim();sels[1].value='ПОДОБНО';inp.value=pm[2].trim();return;}
      var om=c.match(/^([\s\S]+?)\s*(<>|>=|<=|=|>|<)\s*([\s\S]+)$/);
      if(om){sels[0].value=om[1].trim();sels[1].value=om[2];inp.value=om[3].trim();}
    });
  }

  // 7) ORDER BY
  if(orderSec){
    qbSplitTopLevel(orderSec,',').forEach(function(o){
      o=o.trim(); if(!o)return;
      mqbAddOrd();
      var rows=document.querySelectorAll('#mqb-ords > div');
      var row=rows[rows.length-1];
      var sels=row.querySelectorAll('select');
      var ubM=o.match(/^(.+?)\s+УБЫВ\s*$/i);
      if(ubM){sels[0].value=ubM[1].trim();sels[1].value='УБЫВ';}
      else{sels[0].value=o.replace(/\s+ВОЗР\s*$/i,'').trim();sels[1].value='ВОЗР';}
    });
  }

  mqbGen();
}

function mqbSetSrc(id){
  var src=_mqbSrcMap[id];_mqbSel={};_mqbJoins=[];
  document.getElementById('mqb-conds').innerHTML='';
  document.getElementById('mqb-ords').innerHTML='';
  document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">'+T("Нет")+'</p>';
  document.getElementById('mqb-alias').value='';
  var vtp=document.getElementById('mqb-vtp');
  if(src&&src.vtParam){vtp.style.display='';document.getElementById('mqb-vtpv').value=src.vtParam;}
  else{vtp.style.display='none';}
  mqbRebuild();
}

function mqbRebuild(){
  var srcId=document.getElementById('mqb-src').value;
  var mainSrc=_mqbSrcMap[srcId];
  var mainAlias=document.getElementById('mqb-alias').value.trim();
  var all=[];
  if(mainSrc){
    mainSrc.fields.forEach(function(f){
      var n=mainAlias?mainAlias+'.'+f.name:f.name;
      all.push({name:n,label:n,type:f.type});
    });
    if(mainAlias)all.push({name:mainAlias+'.Ссылка',label:mainAlias+'.Ссылка (id)',type:'ref'});
  }
  _mqbJoins.forEach(function(j){
    var src=_mqbSrcMap[j.srcSel.value],alias=j.aliasInp.value.trim();
    if(!src||!alias)return;
    src.fields.forEach(function(f){all.push({name:alias+'.'+f.name,label:alias+'.'+f.name,type:f.type});});
    all.push({name:alias+'.Ссылка',label:alias+'.Ссылка (id)',type:'ref'});
  });
  var ns={};all.forEach(function(f){ns[f.name]=true;});
  var nw={};Object.keys(_mqbSel).forEach(function(k){if(ns[k])nw[k]=_mqbSel[k];});
  if(!Object.keys(nw).length&&mainSrc){
    all.forEach(function(f){if(!f.name.endsWith('.Ссылка')&&f.name!=='Ссылка')nw[f.name]={alias:'',agg:''};});
  }
  _mqbSel=nw;_mqbCurFields=all;mqbRenderFields();mqbGen();
}

function mqbRenderFields(){
  var div=document.getElementById('mqb-fields');div.innerHTML='';
  if(!_mqbCurFields.length){div.innerHTML='<p style="font-size:12px;color:#94a3b8">Выберите источник</p>';return;}
  var lastP=null;
  _mqbCurFields.forEach(function(f){
    var di=f.name.indexOf('.'),pf=di>=0?f.name.substring(0,di):'';
    if(pf&&pf!==lastP){lastP=pf;
      var sep=document.createElement('div');sep.style.cssText='font-size:11px;font-weight:600;color:#64748b;margin:4px 0 2px;border-top:1px solid #f1f5f9;padding-top:4px';
      sep.textContent=pf;div.appendChild(sep);
    }
    var row=document.createElement('div');row.style.cssText='display:flex;align-items:center;gap:5px;margin-bottom:2px;font-size:12px';
    var chk=document.createElement('input');chk.type='checkbox';chk.checked=!!_mqbSel[f.name];chk.dataset.field=f.name;
    chk.onchange=function(){if(chk.checked)_mqbSel[f.name]={alias:'',agg:''};else delete _mqbSel[f.name];mqbGen();};
    var lbl=document.createElement('label');lbl.textContent=di>=0?f.name.substring(di+1):f.name;lbl.title=f.name;
    lbl.style.cssText='flex:1;cursor:pointer;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';lbl.onclick=function(){chk.click();};
    var agg=document.createElement('select');agg.style.cssText='font-size:11px;padding:1px 2px;border:1px solid #e2e8f0;border-radius:3px;width:82px';
    ['','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ'].forEach(function(a){var o=document.createElement('option');o.value=a;o.textContent=a||'—';agg.appendChild(o);});
    if(_mqbSel[f.name])agg.value=_mqbSel[f.name].agg||'';
    agg.onchange=function(){if(_mqbSel[f.name])_mqbSel[f.name].agg=agg.value;mqbGen();};
    var al=document.createElement('input');al.type='text';al.placeholder='КАК';al.style.cssText='font-size:11px;width:60px;padding:1px 3px;border:1px solid #e2e8f0;border-radius:3px';
    if(_mqbSel[f.name])al.value=_mqbSel[f.name].alias||'';
    al.oninput=function(){if(_mqbSel[f.name])_mqbSel[f.name].alias=al.value.trim();mqbGen();};
    row.appendChild(chk);row.appendChild(lbl);row.appendChild(agg);row.appendChild(al);div.appendChild(row);
  });
}

function mqbAll(v){
  _mqbCurFields.forEach(function(f){
    if(f.name.endsWith('.Ссылка')||f.name==='Ссылка')return;
    if(v)_mqbSel[f.name]={alias:'',agg:''};else delete _mqbSel[f.name];
  });mqbRenderFields();mqbGen();
}

function mqbAddJoin(){
  var mainA=document.getElementById('mqb-alias');
  if(!mainA.value.trim()){var ms=_mqbSrcMap[document.getElementById('mqb-src').value];
    if(ms){var p=ms.label.split('.');mainA.value=p.length>=2?p[1].replace(/\(.*$/,''):p[0];}}
  var hint=document.getElementById('mqb-joins-hint');if(hint)hint.remove();
  var jid=Date.now(),div=document.createElement('div');
  div.style.cssText='border:1px solid #e2e8f0;border-radius:5px;padding:6px;margin-bottom:6px;background:#fff';
  var r1=document.createElement('div');r1.style.cssText='display:flex;gap:4px;align-items:center;margin-bottom:4px;flex-wrap:wrap';
  var ts=document.createElement('select');ts.style.cssText='width:110px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  [['ЛЕВОЕ','⬅ ЛЕВОЕ'],['ВНУТРЕННЕЕ','✕ ВНУТРЕННЕЕ'],['ПРАВОЕ','➡ ПРАВОЕ'],['ПОЛНОЕ','⟺ ПОЛНОЕ']].forEach(function(x){var o=document.createElement('option');o.value=x[0];o.textContent=x[1];ts.appendChild(o);});
  var ss=document.createElement('select');ss.style.cssText='flex:1;min-width:120px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  var o0=document.createElement('option');o0.value='';o0.textContent='— источник —';ss.appendChild(o0);
  var jg={};_mqbSchema.forEach(function(s){if(!jg[s.group])jg[s.group]=[];jg[s.group].push(s);});
  Object.keys(jg).forEach(function(g){var og=document.createElement('optgroup');og.label=g;jg[g].forEach(function(s){var o=document.createElement('option');o.value=s.id;o.textContent=s.label;og.appendChild(o);});ss.appendChild(og);});
  var ai=document.createElement('input');ai.type='text';ai.placeholder='Псевдоним';ai.style.cssText='width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  var del=document.createElement('button');del.type='button';del.textContent='×';
  del.style.cssText='background:none;border:none;color:#ef4444;cursor:pointer;font-size:16px;line-height:1;padding:0 2px';
  del.onclick=function(){div.remove();_mqbJoins=_mqbJoins.filter(function(j){return j.id!==jid;});
    if(!_mqbJoins.length)document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">'+T("Нет")+'</p>';
    mqbRebuild();};
  r1.appendChild(ts);r1.appendChild(ss);r1.appendChild(ai);r1.appendChild(del);
  var r2=document.createElement('div');r2.style.cssText='display:flex;gap:4px;align-items:center';
  var onL=document.createElement('span');onL.textContent='ПО:';onL.style.cssText='font-size:12px;font-weight:600;color:#475569;width:24px';
  var onI=document.createElement('input');onI.type='text';onI.placeholder='Пс1.Поле = Пс2.Ссылка';
  onI.style.cssText='flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 4px';
  r2.appendChild(onL);r2.appendChild(onI);div.appendChild(r1);div.appendChild(r2);
  document.getElementById('mqb-joins').appendChild(div);
  var jd={id:jid,el:div,typeSel:ts,srcSel:ss,aliasInp:ai,onInp:onI};_mqbJoins.push(jd);
  ss.onchange=function(){var src=_mqbSrcMap[ss.value];if(src&&!ai.value.trim()){var p=src.label.split('.');ai.value=p.length>=2?p[1].replace(/\(.*$/,''):p[0];}mqbRebuild();};
  ts.onchange=function(){mqbGen();};ai.oninput=function(){mqbRebuild();};onI.oninput=function(){mqbGen();};
  mqbRebuild();
}

function mqbAddCond(){
  var div=document.createElement('div');div.style.cssText='display:flex;gap:3px;margin-bottom:4px;align-items:center;flex-wrap:wrap';
  var fs=document.createElement('select');fs.style.cssText='flex:1;min-width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  _mqbCurFields.forEach(function(f){var o=document.createElement('option');o.value=f.name;o.textContent=f.name;fs.appendChild(o);});
  var ops=document.createElement('select');ops.style.cssText='width:90px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  ['=','<>','>','<','>=','<=','ЕСТЬ ПУСТО','НЕ ЕСТЬ ПУСТО','ПОДОБНО','В'].forEach(function(op){var o=document.createElement('option');o.value=op;o.textContent=op;ops.appendChild(o);});
  var vi=document.createElement('input');vi.type='text';vi.placeholder='&Параметр';vi.style.cssText='flex:1;min-width:60px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 4px';
  ops.onchange=function(){var nv=ops.value==='ЕСТЬ ПУСТО'||ops.value==='НЕ ЕСТЬ ПУСТО';vi.style.display=nv?'none':'';mqbGen();};
  fs.onchange=vi.oninput=function(){mqbGen();};
  var del=document.createElement('button');del.type='button';del.textContent='×';del.style.cssText='background:none;border:none;color:#ef4444;cursor:pointer;font-size:14px;line-height:1';
  del.onclick=function(){div.remove();mqbGen();};
  div.appendChild(fs);div.appendChild(ops);div.appendChild(vi);div.appendChild(del);
  document.getElementById('mqb-conds').appendChild(div);mqbGen();
}

function mqbAddOrd(){
  var div=document.createElement('div');div.style.cssText='display:flex;gap:3px;margin-bottom:4px;align-items:center';
  var fs=document.createElement('select');fs.style.cssText='flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  _mqbCurFields.forEach(function(f){var o=document.createElement('option');o.value=f.name;o.textContent=f.name;fs.appendChild(o);});
  var ds=document.createElement('select');ds.style.cssText='width:70px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  [['ВОЗР','↑ ВОЗР'],['УБЫВ','↓ УБЫВ']].forEach(function(x){var o=document.createElement('option');o.value=x[0];o.textContent=x[1];ds.appendChild(o);});
  var del=document.createElement('button');del.type='button';del.textContent='×';del.style.cssText='background:none;border:none;color:#ef4444;cursor:pointer;font-size:14px;line-height:1';
  del.onclick=function(){div.remove();mqbGen();};
  fs.onchange=ds.onchange=function(){mqbGen();};
  div.appendChild(fs);div.appendChild(ds);div.appendChild(del);
  document.getElementById('mqb-ords').appendChild(div);mqbGen();
}

function mqbGen(){
  var srcId=document.getElementById('mqb-src').value,src=_mqbSrcMap[srcId];
  if(!src){document.getElementById('mqb-qry').value='';document.getElementById('mqb-dsl').value='';return;}
  var mainAlias=document.getElementById('mqb-alias').value.trim();
  var activeJ=_mqbJoins.filter(function(j){return!!j.srcSel.value&&!!j.aliasInp.value.trim();});
  var hasJ=activeJ.length>0,selP=[],hasAgg=false,grpF=[];
  _mqbCurFields.forEach(function(f){
    var info=_mqbSel[f.name];if(!info)return;var expr=f.name;
    if(info.agg){expr=info.agg+'('+f.name+')';hasAgg=true;}else{grpF.push(f.name);}
    if(info.alias)expr+=' КАК '+info.alias;selP.push('  '+expr);
  });
  if(!selP.length)selP=['  *'];
  var from=src.label;
  if(src.vtParam){var vv=document.getElementById('mqb-vtpv').value.trim()||src.vtParam;from=from.replace(/\(.*?\)/,'('+vv+')');}
  if(mainAlias||hasJ)from+=' КАК '+(mainAlias||'Т');
  activeJ.forEach(function(j){
    var jSrc=_mqbSrcMap[j.srcSel.value],jA=j.aliasInp.value.trim(),jL=jSrc.label;
    var onC=j.onInp.value.trim();
    from+='\n  '+j.typeSel.value+' СОЕДИНЕНИЕ '+jL+' КАК '+jA;
    from+='\n  ПО '+(onC||'/* условие */');
  });
  var wP=[],params={};
  document.getElementById('mqb-conds').querySelectorAll('div').forEach(function(row){
    var sels=row.querySelectorAll('select'),inp=row.querySelector('input[type=text]');
    if(!sels[0])return;var field=sels[0].value,op=sels[1]?sels[1].value:'=',val=(inp&&inp.style.display!=='none')?inp.value.trim():'';
    if(op==='ЕСТЬ ПУСТО'||op==='НЕ ЕСТЬ ПУСТО'){wP.push(field+' '+op);}
    else if(val){var m=val.match(/&[А-Яа-яёЁA-Za-z_]\w*/g);if(m)m.forEach(function(p){params[p]=true;});
      wP.push(op==='В'?field+' В ('+val+')':field+' '+op+' '+val);}
  });
  activeJ.forEach(function(j){var m=j.onInp.value.match(/&[А-Яа-яёЁA-Za-z_]\w*/g);if(m)m.forEach(function(p){params[p]=true;});});
  var oP=[];
  document.getElementById('mqb-ords').querySelectorAll('div').forEach(function(row){
    var sels=row.querySelectorAll('select');if(!sels[0])return;var f=sels[0].value,d=sels[1]?sels[1].value:'ВОЗР';
    oP.push(d==='УБЫВ'?f+' УБЫВ':f);
  });
  var q='ВЫБРАТЬ\n'+selP.join(',\n')+'\nИЗ '+from;
  if(wP.length)q+='\nГДЕ '+wP.join('\n  И ');
  if(hasAgg&&grpF.length)q+='\nСГРУППИРОВАТЬ ПО '+grpF.join(', ');
  if(oP.length)q+='\nУПОРЯДОЧИТЬ ПО '+oP.join(', ');
  document.getElementById('mqb-qry').value=q;
  var pL=Object.keys(params);
  var qL=q.split('\n'),strLit='"'+qL[0];
  for(var i=1;i<qL.length;i++)strLit+='\n|'+qL[i];strLit+='"';
  var dsl='Запрос = Новый Запрос;\nЗапрос.Текст =\n  '+strLit+';\n';
  pL.forEach(function(p){dsl+='Запрос.УстановитьПараметр("'+p.slice(1)+'", '+p+');\n';});
  dsl+='Результат = Запрос.Выполнить();\n\nДля Каждого Строка Из Результат Цикл\n';
  var ff=_mqbCurFields.find(function(f){return!!_mqbSel[f.name]&&!f.name.endsWith('.Ссылка')&&f.name!=='Ссылка';});
  if(ff){var fn=(_mqbSel[ff.name]&&_mqbSel[ff.name].alias)||ff.name.replace(/.*\./,'');dsl+='  Сообщить(Строка.'+fn+');\n';}
  dsl+='КонецЦикла;';
  document.getElementById('mqb-dsl').value=dsl;
}

// ── Debug panel ──────────────────────────────────────────────────
var _dbgBase = window.__cfg.baseId; // base ID for debug proxy
var _basePort = window.__cfg.basePort;
var _hasSession = window.__cfg.hasSession; // сырой токен в страницу не вшиваем (план 53)
var _treeGroupOrder = window.__cfg.groupOrder; // пользовательский порядок групп дерева
var _dbgEnabled = false;
var _dbgPollTimer = null;
var _dbgPollCount = 0;
var _dbgBreakpoints = {}; // { editorId: { line: true } }
var _lastVarsKey = '';
var _lastStackHtml = '';
var _lastDiagHtml = '';
var _dbgCurrentLineDecos = {}; // { editorId: decorationIds }

// ── Backup progress helper ────────────────────────────────────────
function cfgBackupStart(form, label) {
  var btn = form.querySelector('[type=submit]');
  if (btn) { btn.disabled = true; btn.textContent = label || '⏳ Выполняется...'; }
  // Show page-level overlay so user can't click anything else
  var ov = document.getElementById('_backup-overlay');
  if (!ov) {
    ov = document.createElement('div');
    ov.id = '_backup-overlay';
    ov.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.3);z-index:8000;display:flex;align-items:center;justify-content:center';
    ov.innerHTML = '<div style="background:#fff;border-radius:10px;padding:28px 40px;text-align:center;box-shadow:0 8px 32px rgba(0,0,0,.18);font-size:15px;color:#1e293b">' + (label||'⏳ Выполняется...') + '<br><span style="font-size:12px;color:#64748b;margin-top:6px;display:block">Пожалуйста, подождите…</span></div>';
    document.body.appendChild(ov);
  }
}

// ── Configurator admin panels ────────────────────────────────────
function cfgAdmin(name) {
  document.getElementById('cfg-menu').classList.remove('open');
  // Use global overlay instead of panel-admin
  var overlay = document.getElementById('admin-overlay');
  if(!overlay)return;
  overlay.style.display='flex';
  overlay.innerHTML = '<div style="background:#fff;border-radius:8px;box-shadow:0 8px 32px rgba(0,0,0,.2);width:90%;max-width:800px;max-height:85vh;overflow-y:auto;position:relative"><div style="padding:20px;text-align:center;color:#888">'+T("Загрузка...")+'</div></div>';
  fetch('/bases/' + _dbgBase + '/configurator/admin/' + name)
    .then(function(r){ return r.text(); })
    .then(function(html){
      overlay.innerHTML = '<div style="background:#fff;border-radius:8px;box-shadow:0 8px 32px rgba(0,0,0,.2);width:90%;max-width:800px;max-height:85vh;overflow-y:auto;position:relative">'
        +'<div style="position:sticky;top:0;background:#fff;padding:8px 16px;border-bottom:1px solid #e2e8f0;display:flex;justify-content:space-between;align-items:center"><span style="font-weight:600;font-size:13px">Администрирование</span><button onclick="document.getElementById(\'admin-overlay\').style.display=\'none\'" style="background:none;border:none;font-size:20px;cursor:pointer;color:#666">×</button></div>'
        +'<div style="padding:16px">'+html+'</div></div>';
      // Scripts inside innerHTML don't execute — re-run them manually
      overlay.querySelectorAll('script').forEach(function(s){
        var ns = document.createElement('script');
        ns.textContent = s.textContent;
        document.body.appendChild(ns);
        document.body.removeChild(ns);
      });
    });
}

function cfgMenuToggle() {
  var m = document.getElementById('cfg-menu');
  m.classList.toggle('open');
}
// cfgTitlesToggle — глобальный режим переводов: показывает/прячет поля переводов
// (.titles-block) у всех объектов разом. По умолчанию выключен, чтобы экран не
// захламляли реквизиты, которые локализуют редко. Состояние — в localStorage;
// класс на <html>, подсветка кнопки — через CSS (html.cfg-titles-on …).
function cfgTitlesToggle() {
  var on = document.documentElement.classList.toggle('cfg-titles-on');
  try { localStorage.setItem('cfgTitlesOn', on ? '1' : '0'); } catch (e) {}
}
function cfgModalClose() {
  var modal = document.getElementById('cfg-modal');
  modal.classList.remove('active');
  var iframe = document.getElementById('cfg-modal-iframe');
  iframe.src = 'about:blank';
}
// close menu on outside click
document.addEventListener('click', function(e) {
  var wrap = e.target.closest('.cfg-menu-wrap');
  if (!wrap) {
    var m = document.getElementById('cfg-menu');
    if (m) m.classList.remove('open');
  }
});

// Возвращает Promise с URL пользовательского режима. Сессия передаётся через
// одноразовый bootstrap-код (план 53): токен не попадает в URL/логи/историю.
function _enterpriseURL() {
  var plain = 'http://localhost:' + _basePort + '/ui';
  if (!_hasSession) return Promise.resolve(plain);
  return fetch('/bases/' + _dbgBase + '/one-time-code', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      return (d && d.code)
        ? 'http://localhost:' + _basePort + '/auth/bootstrap?code=' + encodeURIComponent(d.code) + '&return=%2Fui'
        : plain;
    })
    .catch(function(){ return plain; });
}

// Открывает конкретный отчёт в запущенной базе (предпросмотр конструктора).
// Требует запущенной базы (как и кнопка «Запустить предприятие»).
function previewReport(name){
  var rep = '/ui/report/' + encodeURIComponent(name);
  var plain = 'http://localhost:' + _basePort + rep;
  if (!_hasSession) { window.open(plain, '_blank'); return; }
  fetch('/bases/' + _dbgBase + '/one-time-code', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      var url = (d && d.code)
        ? 'http://localhost:' + _basePort + '/auth/bootstrap?code=' + encodeURIComponent(d.code) + '&return=' + encodeURIComponent(rep)
        : plain;
      window.open(url, '_blank');
    })
    .catch(function(){ window.open(plain, '_blank'); });
}

// Запускает (restart=false) или перезапускает (restart=true) базу и открывает
// пользовательский режим. Дожидается готовности сервера перед открытием окна.
function _doLaunch(restart) {
  var btn = document.querySelector('.run-enterprise-btn');
  if (btn) btn.style.background = '#a3a3a3';
  // одноразовый код запрашиваем ПОСЛЕ старта базы — его выдаёт сам процесс базы
  var openUI = function(){ _enterpriseURL().then(function(url){ window.open(url, '_blank'); }); };
  var endpoint = restart
    ? '/bases/' + _dbgBase + '/configurator/restart'
    : '/bases/' + _dbgBase + '/start';
  fetch(endpoint, {method:'POST'})
    .then(function(r){ return r.json().catch(function(){ return {}; }); })
    .then(function(d){
      if (btn) btn.style.background = '';
      if (d && d.error) { alert('Не удалось запустить базу: ' + d.error); return; }
      openUI();
    })
    .catch(function(){
      if (btn) btn.style.background = '';
      openUI();
    });
}

// Точка входа жёлтой ▶: проверяет, отстала ли БД от конфигурации (как F5 в 1С),
// и при необходимости предлагает обновить структуру БД перед запуском.
function launchEnterprise() {
  fetch('/bases/' + _dbgBase + '/configurator/launch-state')
    .then(function(r){ return r.json(); })
    .then(function(st){
      if (st && st.configChanged) {
        _showLaunchDialog(!!(st && st.running));
      } else {
        _doLaunch(false);
      }
    })
    .catch(function(){ _doLaunch(false); });
}

function _closeLaunchDialog() {
  var ov = document.getElementById('launch-dialog-overlay');
  if (ov) ov.remove();
}

function _showLaunchDialog(running) {
  _closeLaunchDialog();
  var rv = running ? 'true' : 'false';
  var ov = document.createElement('div');
  ov.id = 'launch-dialog-overlay';
  ov.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);display:flex;align-items:center;justify-content:center;z-index:10000';
  var box = document.createElement('div');
  box.className = 'cfg-modal-box';
  box.style.maxWidth = '470px';
  box.innerHTML =
      '<div class="cfg-modal-hd"><span style="font-weight:600;font-size:13px">Запуск предприятия</span>'
    + '<button onclick="_closeLaunchDialog()" style="background:none;border:none;font-size:20px;cursor:pointer;color:#666">×</button></div>'
    + '<div style="padding:16px 18px;font-size:13px;color:#334;line-height:1.5">'
    + 'Конфигурация изменена с момента последнего обновления базы данных.<br>Обновить структуру БД и запустить?'
    + '<div id="launch-dialog-msg" style="display:none;margin-top:10px;font-size:11px;padding:8px;border-radius:4px;max-height:140px;overflow-y:auto;white-space:pre-wrap"></div>'
    + '</div>'
    + '<div style="display:flex;gap:8px;justify-content:flex-end;padding:12px 18px;border-top:1px solid #e2e8f0;flex-wrap:wrap">'
    + '<button id="ld-cancel" onclick="_closeLaunchDialog()" style="padding:7px 14px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;font-size:12px">Отмена</button>'
    + '<button id="ld-nomigrate" onclick="_launchNoMigrate(' + rv + ')" style="padding:7px 14px;background:#fff;border:1px solid #1a4a80;color:#1a4a80;border-radius:4px;cursor:pointer;font-size:12px">Запустить без обновления</button>'
    + '<button id="ld-migrate" onclick="_launchWithMigrate(' + rv + ')" style="padding:7px 14px;background:#1a4a80;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:12px">Обновить БД и запустить</button>'
    + '</div>';
  ov.appendChild(box);
  document.body.appendChild(ov);
}

function _launchNoMigrate(running) {
  _closeLaunchDialog();
  _doLaunch(running);
}

function _launchWithMigrate(running) {
  var msg = document.getElementById('launch-dialog-msg');
  var mb = document.getElementById('ld-migrate');
  var nb = document.getElementById('ld-nomigrate');
  if (mb) { mb.disabled = true; mb.textContent = 'Обновление БД...'; }
  if (nb) nb.disabled = true;
  fetch('/bases/' + _dbgBase + '/configurator/migrate', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d && d.error) {
        if (mb) { mb.disabled = false; mb.textContent = 'Обновить БД и запустить'; }
        if (nb) nb.disabled = false;
        if (msg) { msg.style.display='block'; msg.style.background='#fff0f0'; msg.style.color='#c00'; msg.textContent = d.error + (d.output ? '\n' + d.output : ''); }
        return;
      }
      _closeLaunchDialog();
      _doLaunch(running);
    })
    .catch(function(e){
      if (mb) { mb.disabled = false; mb.textContent = 'Обновить БД и запустить'; }
      if (nb) nb.disabled = false;
      if (msg) { msg.style.display='block'; msg.style.background='#fff0f0'; msg.style.color='#c00'; msg.textContent = 'Ошибка: ' + e.message; }
    });
}

function runMigrate() {
  var btn = document.getElementById('btn-migrate');
  var result = document.getElementById('migrate-result');
  btn.disabled = true;
  btn.textContent = 'Обновление...';
  result.style.display = 'none';
  fetch('/bases/' + _dbgBase + '/configurator/migrate', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      btn.disabled = false;
      btn.textContent = 'Обновить БД';
      result.style.display = 'block';
      if (d.error) {
        result.style.background = '#fff0f0';
        result.style.color = '#c00';
        result.textContent = d.error;
      } else {
        result.style.background = '#f0fdf4';
        result.style.color = '#15803d';
        result.textContent = d.output || 'Обновление завершено';
      }
    })
    .catch(function(e){
      btn.disabled = false;
      btn.textContent = 'Обновить БД';
      result.style.display = 'block';
      result.style.background = '#fff0f0';
      result.style.color = '#c00';
      result.textContent = 'Ошибка: ' + e.message;
    });
}

function dbgToggle() {
  var btn = document.getElementById('dbg-toggle');
  var panel = document.getElementById('dbg-panel');
  if (!_dbgEnabled) {
    fetch('/bases/' + _dbgBase + '/debug/enable', {method:'POST'})
      .then(function(r){
        if (!r.ok) return r.json().then(function(d){ throw new Error(d.error || ('HTTP ' + r.status)); });
        return r.json();
      })
      .then(function(d){
        if (d.status !== 'enabled') { throw new Error(d.error || 'Unexpected response'); }
        _dbgEnabled = true;
        btn.innerHTML = '&#128027; Отладка: ВКЛ';
        btn.className = 'dbg-topbar-btn dbg-on';
        panel.style.display = 'flex';
        var rzOn = document.getElementById('dbg-resizer'); if (rzOn) rzOn.style.display = '';   // issue #131
        dbgUpdateStatus('running', 'init');
        var diagEl = document.getElementById('dbg-diag');
        if (diagEl) diagEl.innerHTML = '<div style="color:#22c55e;font-size:10px">Enable OK: dbg=' + (d.dbg_ptr||'?') + ' sess=' + (d.sess_ptr||'?') + '</div>';
        dbgStartPoll();
      })
      .catch(function(e){ alert('Ошибка включения отладки: ' + e.message); });
  } else {
    fetch('/bases/' + _dbgBase + '/debug/disable', {method:'POST'})
      .then(function(r){return r.json()})
      .then(function(d){
        _dbgEnabled = false;
        btn.innerHTML = '&#128027; Отладка: ВЫКЛ';
        btn.className = 'dbg-topbar-btn';
        panel.style.display = 'none';
        var rzOff = document.getElementById('dbg-resizer'); if (rzOff) rzOff.style.display = 'none';   // issue #131
        dbgStopPoll();
        dbgClearLineHighlight();
        dbgUpdateStatus('disabled', 'off');
      })
      .catch(function(e){ console.error('dbg disable error', e); });
  }
}

function dbgStartPoll() {
  dbgStopPoll();
  _dbgPollTimer = setInterval(dbgPoll, 500);
}
function dbgStopPoll() {
  if (_dbgPollTimer) { clearInterval(_dbgPollTimer); _dbgPollTimer = null; }
}

function dbgPoll() {
  fetch('/bases/' + _dbgBase + '/debug/status?_=' + Date.now())
    .then(function(r){return r.json()})
    .then(function(snap){
      _dbgPollCount++;
      if (snap.state === 'disabled') {
        dbgUpdateStatus('disabled', snap.state);
        return;
      }
      var st = snap.state; // "running" | "paused" | "stopped"

      // show/hide controls
      var ctrl = document.getElementById('dbg-controls');
      ctrl.style.display = (st === 'paused' || st === 'running') ? 'flex' : 'none';

      // if paused with location, highlight current line in Monaco — BEFORE dbgUpdateStatus
      if (st === 'paused' && snap.location) {
        var locFile = snap.location.file;
        var locLine = snap.location.line;
        dbgShowLocation(locFile, locLine);
        dbgWatchEvalAll();
      } else if (st === 'stopped' || st === 'disabled') {
        dbgClearLineHighlight();
      }

      // NOW update status (reads _dbgHighlightLog and _dbgPauseReason)
      _dbgPauseReason = snap.pause_reason || '';
      dbgUpdateStatus(st, snap.state);
      // update button color
      var btn = document.getElementById('dbg-toggle');
      if (st === 'paused') btn.className = 'dbg-topbar-btn dbg-paused';
      else if (st === 'running') btn.className = 'dbg-topbar-btn dbg-on';

      // variables — only update DOM if changed
      if (snap.variables && snap.variables.length) {
        var varsKey = snap.variables.map(function(v){return v.name+'='+v.value;}).join('|');
        if (varsKey !== _lastVarsKey) {
          _lastVarsKey = varsKey;
          var h = '';
          snap.variables.forEach(function(v){
            h += '<div class="dbg-var-row"><span class="dbg-var-name">' + esc(v.name) + '</span>'
              + '<span class="dbg-var-val dbg-val-clickable" title="Открыть значение" onclick="dbgInspectValue(\'' + esc(v.name).replace(/'/g,"\\'") + '\')">' + esc(v.value) + '</span><span class="dbg-var-type">' + esc(v.type) + '</span></div>';
          });
          document.getElementById('dbg-vars').innerHTML = h;
        }
      } else if (st !== 'paused') {
        if (_lastVarsKey !== '') {
          _lastVarsKey = '';
          document.getElementById('dbg-vars').innerHTML = '<div class="dbg-empty">Нет переменных</div>';
        }
      }

      // stack — only update DOM if changed
      if (snap.stack && snap.stack.length) {
        var h = '';
        snap.stack.forEach(function(f){
          h += '<div class="dbg-stack-row"><span class="proc">' + esc(f.procedure) + '</span> '
            + '<span class="loc">' + esc(f.module) + ':' + f.line + '</span></div>';
        });
        if (h !== _lastStackHtml) { _lastStackHtml = h; document.getElementById('dbg-stack').innerHTML = h; }
      }

      // breakpoints — always render from local state
      dbgRenderBPList();

      // diagnostics — show only detailed check messages in console tab
      var diagEl = document.getElementById('dbg-diag');
      if (diagEl && snap.state !== 'disabled') {
        var dh = '';
        if (snap.diag_messages && snap.diag_messages.length) {
          var msgs = snap.diag_messages.slice(-12);
          msgs.forEach(function(m){ dh += '<div class="dbg-console-line">' + esc(m) + '</div>'; });
        }
        if (dh !== _lastDiagHtml) { _lastDiagHtml = dh; diagEl.innerHTML = dh; }
      } else if (diagEl && snap.state === 'disabled') {
        if (_lastDiagHtml !== '__disabled__') { _lastDiagHtml = '__disabled__'; diagEl.innerHTML = '<div style="color:#9ca3af;font-size:10px">Debug disabled</div>'; }
      }
    })
    .catch(function(e){
      var diagEl = document.getElementById('dbg-diag');
      if (diagEl) { diagEl.innerHTML = '<div style="color:#ef4444;font-size:10px">Poll error: ' + esc(e.message) + '</div>' + diagEl.innerHTML; }
    });
}

var _dbgHighlightLog = '';
var _dbgPauseReason = '';
var _lastStatusHtml = '';
function dbgUpdateStatus(st, rawState) {
  var el = document.getElementById('dbg-status');
  var labels = {disabled:'Отладка: ВЫКЛ', running:'Отладка: выполнение...', paused:'Отладка: пауза', stopped:'Отладка: останов'};
  var dot = '<span class="dot ' + st + '"></span> ' + (labels[st]||st);
  if (_dbgPauseReason && st === 'paused') {
    dot += ' <span style="font-size:10px;color:#94a3b8">(' + (_dbgPauseReason === 'breakpoint' ? 'точка останова' : 'шаг') + ')</span>';
  }
  var html = _dbgHighlightLog ? (dot + ' <span style="font-size:9px;color:#f97316;user-select:text">' + _dbgHighlightLog + '</span>') : dot;
  // Avoid rewriting innerHTML on every poll — it clears any text the user is selecting.
  if (html === _lastStatusHtml) return;
  _lastStatusHtml = html;
  el.innerHTML = html;
}

function dbgContinue() {
  dbgClearLineHighlight();
  fetch('/bases/' + _dbgBase + '/debug/continue', {method:'POST'}).then(function(){
    setTimeout(dbgPoll, 100);
    setTimeout(dbgPoll, 500);
  }).catch(function(e){console.error(e);});
}
function dbgStep(mode) {
  dbgClearLineHighlight();
  fetch('/bases/' + _dbgBase + '/debug/step', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({mode:mode})}).then(function(){
    // Poll repeatedly until we see "paused" or timeout (2s)
    var tries = 0;
    function pollUntilPaused() {
      if (tries > 12) { dbgWatchDebug('step timeout'); return; }
      tries++;
      setTimeout(function(){
        fetch('/bases/' + _dbgBase + '/debug/status?_=' + Date.now())
          .then(function(r){return r.json()})
          .then(function(snap){
            dbgWatchDebug('step poll #' + tries + ' state=' + snap.state + ' reason=' + (snap.pause_reason||'?') + ' loc=' + (snap.location ? snap.location.file + ':' + snap.location.line : 'none'));
            if (snap.state === 'paused' && snap.location) {
              dbgPoll(); // run full poll to update everything
            } else {
              pollUntilPaused();
            }
          })
          .catch(function(e){ dbgWatchDebug('step poll err: ' + e.message); pollUntilPaused(); });
      }, 200);
    }
    pollUntilPaused();
  }).catch(function(e){console.error(e);});
}
document.addEventListener('keydown', function(e) {
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return;
  if (e.key === 'F10') { e.preventDefault(); e.stopPropagation(); dbgStep('over'); }
  else if (e.key === 'F11' && !e.shiftKey) { e.preventDefault(); e.stopPropagation(); dbgStep('into'); }
  else if (e.key === 'F11' && e.shiftKey) { e.preventDefault(); e.stopPropagation(); dbgStep('out'); }
}, true);
function dbgStop() {
  fetch('/bases/' + _dbgBase + '/debug/stop', {method:'POST'})
    .then(function(){
      var btn = document.getElementById('dbg-toggle');
      _dbgEnabled = false;
      btn.innerHTML = '&#128027; Отладка: ВЫКЛ';
      btn.className = 'dbg-topbar-btn';
      dbgStopPoll();
      dbgClearLineHighlight();
      dbgUpdateStatus('disabled', 'stop');
    }).catch(function(e){console.error(e);});
}

function dbgEval() {
  var inp = document.getElementById('dbg-expr');
  var expr = inp.value.trim();
  if (!expr) return;
  var out = document.getElementById('dbg-console-out');
  out.innerHTML += '<div class="dbg-console-line">&gt; ' + esc(expr) + '</div>';
  inp.value = '';
  fetch('/bases/' + _dbgBase + '/debug/evaluate', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expr:expr})})
    .then(function(r){return r.json()})
    .then(function(res){
      if (res.is_error) {
        out.innerHTML += '<div class="dbg-console-line dbg-err">' + esc(res.error) + '</div>';
      } else {
        var val = res.value !== null && res.value !== undefined ? String(res.value) : 'Неопределено';
        out.innerHTML += '<div class="dbg-console-line dbg-ok">= ' + esc(val) + ' (' + esc(res.type||'') + ')</div>';
      }
      out.scrollTop = out.scrollHeight;
    })
    .catch(function(e){
      out.innerHTML += '<div class="dbg-console-line dbg-err">Ошибка: ' + esc(e.message) + '</div>';
      out.scrollTop = out.scrollHeight;
    });
}

function dbgTab(name) {
  var tabs = ['vars','watch','bp','stack','console'];
  tabs.forEach(function(t){
    var el = document.getElementById('dbg-' + t);
    if (t === name) {
      el.style.display = 'flex';
      el.style.flexDirection = 'column';
    } else {
      el.style.display = 'none';
    }
  });
  document.querySelectorAll('.dbg-tab').forEach(function(btn, i){
    btn.classList.toggle('active', tabs[i] === name);
  });
}

// ── Watch (Табло v4) ──────────────────────────────────────────
var _dbgWatchList = []; // [{name: 'Перем', id: 'w0'}]
var _dbgWatchId = 0;

function dbgWatchDebug(msg) {
  var d = document.getElementById('dbg-watch-debug');
  if (d) d.textContent = msg;
}

function dbgWatchAdd() {
  var inp = document.getElementById('dbg-watch-add');
  if (!inp) { dbgWatchDebug('ERROR: input not found'); return; }
  var name = inp.value.trim();
  if (!name) return;
  inp.value = '';
  _dbgWatchId++;
  var wid = 'w' + _dbgWatchId;
  _dbgWatchList.push({name: name, id: wid});
  dbgWatchDebug('added "' + name + '" id=' + wid + ' total=' + _dbgWatchList.length);
  dbgWatchRender();
  dbgWatchEvalAll();
}

function dbgWatchRemove(id) {
  _dbgWatchList = _dbgWatchList.filter(function(w){ return w.id !== id; });
  dbgWatchRender();
}

function dbgWatchRender() {
  var el = document.getElementById('dbg-watch-list');
  if (!el) { dbgWatchDebug('ERROR: list element not found'); return; }
  if (!_dbgWatchList.length) {
    el.innerHTML = '<div style="color:#94a3b8;padding:8px 0;text-align:center;font-size:12px">Добавьте выражение</div>';
    dbgWatchDebug('empty list');
    return;
  }
  var h = '';
  _dbgWatchList.forEach(function(w){
    h += '<div class="dbg-var-row">'
      + '<span class="dbg-var-name">' + esc(w.name) + '</span>'
      + ' <span style="color:#ef4444;cursor:pointer;font-size:9px" onclick="dbgWatchRemove(\'' + w.id + '\')">&#10005;</span>'
      + ' <span class="dbg-var-val dbg-val-clickable" title="Открыть значение" onclick="dbgWatchOpen(\'' + w.id + '\')" id="wv-' + w.id + '">...</span>'
      + ' <span class="dbg-var-type" id="wt-' + w.id + '"></span>'
      + '</div>';
  });
  el.innerHTML = h;
  dbgWatchDebug('rendered ' + _dbgWatchList.length + ' items');
}

function dbgValToStr(v) {
  if (v === null || v === undefined) return 'Неопределено';
  if (typeof v === 'object') { try { return JSON.stringify(v, null, 2); } catch(e) { return String(v); } }
  return String(v);
}

var _dbgWatchGen = 0;
function dbgWatchEvalAll() {
  if (!_dbgWatchList.length) return;
  _dbgWatchGen++;
  var gen = _dbgWatchGen;
  _dbgWatchList.forEach(function(w){
    var valEl = document.getElementById('wv-' + w.id);
    if (!valEl) return;
    if (!_dbgEnabled) { valEl.textContent = '(отладка выкл)'; return; }
    fetch('/bases/' + _dbgBase + '/debug/evaluate', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expr:w.name})})
      .then(function(r){return r.json()})
      .then(function(res){
        if (gen !== _dbgWatchGen) return; // a newer evaluation round started — drop stale result
        var v = document.getElementById('wv-' + w.id);
        var t = document.getElementById('wt-' + w.id);
        if (!v) return;
        if (res.is_error) {
          // Usually a transient "interpreter not paused" window between steps —
          // keep the last good value, just dim it instead of flickering to an error.
          v.style.opacity = '0.4';
          return;
        }
        var txt = dbgValToStr(res.value);
        w._full = txt;
        w._type = res.type || '';
        var disp = txt.length > 300 ? txt.substring(0, 300) + '…' : txt;
        if (v.textContent !== disp) v.textContent = disp;
        v.style.opacity = '';
        v.style.color = '';
        v.title = (txt.length > 60) ? 'Открыть значение полностью' : 'Открыть значение';
        var tp = res.type || '';
        if (t && t.textContent !== tp) t.textContent = tp;
      })
      .catch(function(e){
        if (gen !== _dbgWatchGen) return;
        var v = document.getElementById('wv-' + w.id);
        if (v) v.style.opacity = '0.4';
      });
  });
}

function dbgWatchOpen(id) {
  var w = null;
  for (var i = 0; i < _dbgWatchList.length; i++) { if (_dbgWatchList[i].id === id) { w = _dbgWatchList[i]; break; } }
  if (!w) return;
  if (w._full !== undefined) dbgShowBigValue(w.name, w._type || '', w._full);
  else dbgInspectValue(w.name);
}

// Inspect a variable/expression: re-evaluate it (full, untruncated) and show in a modal.
function dbgInspectValue(expr) {
  if (!_dbgEnabled) { dbgShowBigValue(expr, '', '(отладка выключена)'); return; }
  fetch('/bases/' + _dbgBase + '/debug/evaluate', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expr:expr})})
    .then(function(r){return r.json()})
    .then(function(res){
      if (res.is_error) { dbgShowBigValue(expr, 'Ошибка', String(res.error)); return; }
      dbgShowBigValue(expr, res.type || '', dbgValToStr(res.value));
    })
    .catch(function(e){ dbgShowBigValue(expr, 'Ошибка', String(e.message)); });
}

function dbgShowBigValue(name, type, value) {
  var t = document.getElementById('dbg-val-modal-title');
  if (t) t.textContent = String(name) + (type ? '  ·  ' + type : '');
  var ta = document.getElementById('dbg-val-modal-text');
  if (ta) { ta.value = (value === null || value === undefined) ? '' : String(value); ta.scrollTop = 0; }
  var m = document.getElementById('dbg-val-modal');
  if (m) m.classList.add('active');
}
function dbgValModalClose() {
  var m = document.getElementById('dbg-val-modal');
  if (m) m.classList.remove('active');
}
function dbgValModalCopy() {
  var ta = document.getElementById('dbg-val-modal-text');
  if (!ta) return;
  ta.focus(); ta.select();
  try { document.execCommand('copy'); } catch(e){}
  if (navigator.clipboard) { try { navigator.clipboard.writeText(ta.value); } catch(e){} }
}

function dbgHighlightLine(file, line) {
  dbgClearLineHighlight();
  var ed = dbgFindEditor(file);
  if (ed) dbgApplyHighlight(ed, line);
}

function dbgClearLineHighlight() {
  Object.keys(_dbgCurrentLineDecos).forEach(function(id){
    var ed = monacoEditors[id];
    if (ed) ed.deltaDecorations(_dbgCurrentLineDecos[id], []);
  });
  _dbgCurrentLineDecos = {};
}

function dbgShowLocation(file, line) {
  dbgClearLineHighlight();
  var edKeys = Object.keys(monacoEditors);
  var ed = dbgFindEditor(file);
  if (ed) {
    dbgApplyHighlight(ed, line);
    _dbgHighlightLog = file + ':' + line + ' OK ed=' + ed._fileId;
    return;
  }
  _dbgHighlightLog = 'NO_ED "' + file + '" keys=[' + edKeys.join(',') + ']';
  // 2. Editor not created — activate the module pane and create editor
  var modId = dbgResolveModulePane(file);
  if (modId) {
    var pane = document.getElementById(modId);
    if (pane && !pane.classList.contains('active')) {
      var wrap = pane.closest('.module-editor-wrap');
      if (wrap) {
        wrap.querySelectorAll('.module-tab').forEach(function(t){t.classList.remove('active')});
        wrap.querySelectorAll('.module-pane').forEach(function(p){p.classList.remove('active')});
      }
      pane.classList.add('active');
      if (wrap) {
        wrap.querySelectorAll('.module-tab').forEach(function(t){
          if (t.getAttribute('onclick') && t.getAttribute('onclick').indexOf(modId) !== -1) {
            t.classList.add('active');
          }
        });
      }
    }
    var pre = pane ? pane.querySelector('pre.os-code') : null;
    if (pre) {
      var editorName = pre.id.replace('pre-', '');
      startEdit(editorName);
      setTimeout(function(){
        var ed2 = monacoEditors[editorName];
        if (ed2) {
          dbgApplyHighlight(ed2, line);
          _dbgHighlightLog = 'LATE ' + editorName + ':' + line;
        } else {
          _dbgHighlightLog = 'NO_ED2 ' + editorName;
        }
      }, 300);
    } else {
      _dbgHighlightLog += ' noPre modId=' + modId;
    }
  }
}

function dbgApplyHighlight(ed, line) {
  var deco = ed.deltaDecorations([], [{
    range: new monaco.Range(line, 1, line, 1),
    options: {
      isWholeLine: true,
      className: 'dbg-current-line-bg',
      glyphMarginClassName: 'dbg-current-line-glyph',
      overviewRuler: { color: '#d97706', position: monaco.editor.OverviewRulerLane.Full }
    }
  }]);
  _dbgCurrentLineDecos[ed._fileId] = deco;
  ed.revealLineInCenter(line);
}

// dbgResolveModulePane maps a normalized file ID like "post-ПоступлениеТоваров"
// to the DOM pane ID like "mp-post-ПоступлениеТоваров"
// Uses case-insensitive DOM lookup since normalizeFilePath lowercases.
function dbgResolveModulePane(file) {
  var prefixes = ['post-', 'mod-', 'proc-', 'rep-', 'pf-'];
  var candidate = null;
  for (var i = 0; i < prefixes.length; i++) {
    if (file.toLowerCase().indexOf(prefixes[i]) === 0) {
      candidate = 'mp-' + file;
      break;
    }
  }
  if (!candidate) candidate = 'mp-obj-' + file;
  // Try exact match first, then case-insensitive search
  if (document.getElementById(candidate)) return candidate;
  var allPanes = document.querySelectorAll('[id^="mp-"]');
  var cLow = candidate.toLowerCase();
  for (var j = 0; j < allPanes.length; j++) {
    if (allPanes[j].id.toLowerCase() === cLow) return allPanes[j].id;
  }
  return candidate;
}

function dbgFindEditor(file) {
  // file from server is normalizeFilePath'd (lowercased + capitalizeFirst)
  // Monaco editor ids preserve original casing, so we need case-insensitive lookup
  var fileLow = file.toLowerCase();
  var keys = Object.keys(monacoEditors);
  for (var i = 0; i < keys.length; i++) {
    if (keys[i].toLowerCase() === fileLow) return monacoEditors[keys[i]];
  }
  // Fallback: try prefixed variants
  var prefixes = ['post-', 'mod-', 'proc-', 'rep-', 'pf-'];
  for (var p = 0; p < prefixes.length; p++) {
    var candidate = prefixes[p] + fileLow;
    for (var j = 0; j < keys.length; j++) {
      if (keys[j].toLowerCase() === candidate) return monacoEditors[keys[j]];
    }
  }
  return null;
}

function dbgFindTabId(file) {
  // Same candidate logic but searches tab data-file-id attributes
  var candidates = [file, 'post-' + file, 'mod-' + file, 'proc-' + file, 'rep-' + file];
  for (var i = 0; i < candidates.length; i++) {
    var tab = document.querySelector('[data-file-id="' + candidates[i] + '"]');
    if (tab) return candidates[i];
  }
  // Try matching by suffix (server may send full path, tabs have short IDs)
  var tabs = document.querySelectorAll('[data-file-id]');
  for (var t = 0; t < tabs.length; t++) {
    var tid = tabs[t].getAttribute('data-file-id');
    if (file.indexOf(tid) !== -1 || tid.indexOf(file) !== -1) return tid;
  }
  return null;
}

function dbgToggleBreakpoint(editorId, line) {
  var diagEl = document.getElementById('dbg-diag');
  if(diagEl) diagEl.innerHTML += '<div style="color:#fbbf24;font-size:10px">BP click: ' + esc(editorId) + ':' + line + ' hasEd=' + !!monacoEditors[editorId] + ' enabled=' + _dbgEnabled + '</div>';
  if (!monacoEditors[editorId]) return;
  var ed = monacoEditors[editorId];
  if (!_dbgBreakpoints[editorId]) _dbgBreakpoints[editorId] = {};
  var has = _dbgBreakpoints[editorId][line];
  if (has) {
    delete _dbgBreakpoints[editorId][line];
  } else {
    _dbgBreakpoints[editorId][line] = true;
  }
  try { dbgRenderBreakpoints(editorId); } catch(e) { console.error('renderBP', e); }
  try { dbgRenderBPList(); } catch(e) { console.error('renderBPList', e); }
  // sync with server
  var diagEl = document.getElementById('dbg-diag');
  if (_dbgEnabled) {
    if(diagEl) diagEl.innerHTML += '<div style="color:#60a5fa;font-size:10px">BP send: ' + esc(editorId) + ':' + line + '</div>';
    fetch('/bases/' + _dbgBase + '/debug/breakpoint', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({file: editorId, line: line, action: 'toggle'})
    }).then(function(r){
      if (!r.ok) { if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP error: HTTP ' + r.status + '</div>'; return r.text(); }
      return r.json();
    }).then(function(d){
      if(d && d.id) {
        if(diagEl) diagEl.innerHTML += '<div style="color:#16a34a;font-size:10px">BP OK: ' + esc(d.file) + ':' + d.line + ' count=' + (d.bp_count||0) + '</div>';
      }
      else if(d && d.status) { if(diagEl) diagEl.innerHTML += '<div style="color:#fbbf24;font-size:10px">BP: ' + d.status + '</div>'; }
      else if(d && d.error) { if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP error: ' + d.error + '</div>'; }
    }).catch(function(e){
      if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP fetch error: ' + e.message + '</div>';
    });
  } else {
    if(diagEl) diagEl.innerHTML += '<div style="color:#fbbf24;font-size:10px">BP saved locally (debug not enabled)</div>';
  }
}

function dbgManualBP() {
  var fileInp = document.getElementById('dbg-bp-file');
  var lineInp = document.getElementById('dbg-bp-line');
  if (!fileInp || !lineInp) return;
  var file = fileInp.value.trim();
  var line = parseInt(lineInp.value);
  if (!file || !line) { dbgWatchDebug('Укажите файл и строку'); return; }
  // Save locally
  if (!_dbgBreakpoints[file]) _dbgBreakpoints[file] = {};
  _dbgBreakpoints[file][line] = true;
  // Also set in Monaco if available
  if (monacoEditors[file]) dbgRenderBreakpoints(file);
  dbgRenderBPList();
  // Send to server
  var diagEl = document.getElementById('dbg-diag');
  if (_dbgEnabled) {
    if(diagEl) diagEl.innerHTML += '<div style="color:#60a5fa;font-size:10px">Manual BP: ' + esc(file) + ':' + line + '</div>';
    fetch('/bases/' + _dbgBase + '/debug/breakpoint', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({file: file, line: line, action: 'set'})
    }).then(function(r){ return r.json(); }).then(function(d){
      if(d && d.id) {
        if(diagEl) diagEl.innerHTML += '<div style="color:#16a34a;font-size:10px">BP OK: ' + esc(d.file) + ':' + d.line + ' count=' + (d.bp_count||0) + '</div>';
      } else if(d && d.error) {
        if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP err: ' + d.error + '</div>';
      }
    }).catch(function(e){
      if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP fetch err: ' + e.message + '</div>';
    });
  } else {
    dbgWatchDebug('BP saved locally (debug not enabled)');
  }
  fileInp.value = '';
  lineInp.value = '';
}

function dbgRenderBPList() {
  var el = document.getElementById('dbg-bp-list');
  if (!el) return;
  var keys = Object.keys(_dbgBreakpoints);
  var h = '';
  keys.forEach(function(file){
    var lines = Object.keys(_dbgBreakpoints[file]);
    lines.forEach(function(ln){
      h += '<div class="dbg-bp-row" style="cursor:pointer" onclick="dbgGoToBP(\'' + esc(file).replace(/'/g,"\\'") + '\',' + ln + ')">'
        + '<span style="color:#ef4444">&#9679;</span>'
        + '<span class="bp-file">' + esc(file) + '</span>'
        + '<span class="bp-line">:' + ln + '</span></div>';
    });
  });
  if (!h) h = '<div class="dbg-empty">Нет точек останова</div>';
  el.innerHTML = h;
}
function dbgGoToBP(file, line) {
  // Open the editor tab if available
  var tab = document.querySelector('[data-file-id="' + file + '"]');
  if (tab) { selItem(tab); tab.scrollIntoView(); }
  // Activate Monaco and scroll to line
  var pre = document.getElementById('pre-' + file);
  if (pre && !pre.querySelector('.monaco-target')) { startEdit(file); }
  setTimeout(function(){
    var ed = monacoEditors[file];
    if (ed) { ed.revealLineInCenter(line); ed.setPosition({lineNumber:line,column:1}); ed.focus(); }
  }, 200);
}

function dbgRenderBreakpoints(editorId) {
  var ed = monacoEditors[editorId];
  if (!ed) return;
  var bps = _dbgBreakpoints[editorId] || {};
  var decos = [];
  Object.keys(bps).forEach(function(ln){
    decos.push({
      range: new monaco.Range(parseInt(ln), 1, parseInt(ln), 1),
      options: {
        isWholeLine: false,
        glyphMarginClassName: 'dbg-bp-glyph',
        glyphMarginHoverMessage: {value: 'Точка останова: строка ' + ln},
        stickiness: monaco.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges
      }
    });
  });
  ed._dbgBpDecos = ed.deltaDecorations(ed._dbgBpDecos || [], decos);
}
document.querySelectorAll('details.cfg-tree').forEach(function(d){
  var a=d.querySelector('.tree-toggle');if(!a)return;
  function u(){a.textContent=d.open?'▾':'▸'}
  d.addEventListener('toggle',u);u();
});
(function(){
  if(window.__cfgAiInit)return;window.__cfgAiInit=true;
  var base = window.__cfg.baseId;
  function anyPanelOpen(){
    return document.getElementById('cfgai-panel').style.display==='flex'||document.getElementById('cfggen-panel').style.display==='flex';
  }
  function openPanel(id){
    document.getElementById('cfgai-panel').style.display='none';
    document.getElementById('cfggen-panel').style.display='none';
    document.getElementById('cfgai-btn').style.display='none';
    document.getElementById('cfggen-btn').style.display='none';
    document.getElementById(id).style.display='flex';
  }
  function closePanel(id){
    document.getElementById(id).style.display='none';
    document.getElementById('cfgai-btn').style.display='';
    document.getElementById('cfggen-btn').style.display='';
  }
  // Глобально доступна: перезапрашивает состояние ИИ и показывает/прячет
  // плавающие кнопки. Зовётся при загрузке и после сохранения настроек ИИ —
  // поэтому кнопки появляются без перезахода в конфигуратор.
  window.cfgAiRefresh=function(){
    fetch('/bases/'+base+'/configurator/ai-enabled').then(function(r){return r.json();}).then(function(d){
      if(anyPanelOpen())return;
      var on=!!(d&&d.enabled);
      document.getElementById('cfgai-btn').style.display=on?'':'none';
      document.getElementById('cfggen-btn').style.display=on?'':'none';
    }).catch(function(){});
  };
  initAiHandlers();
  initGenHandlers();
  cfgAiRefresh();

  function initAiHandlers(){
    var btn=document.getElementById('cfgai-btn');
    var prompt=document.getElementById('cfgai-prompt'),send=document.getElementById('cfgai-send'),out=document.getElementById('cfgai-out');
    var msg=document.getElementById('cfgai-msg'),copy=document.getElementById('cfgai-copy'),useCode=document.getElementById('cfgai-usecode');
    btn.addEventListener('click',function(){openPanel('cfgai-panel');prompt.focus();});
    document.getElementById('cfgai-close').addEventListener('click',function(){closePanel('cfgai-panel');});
    // Код одного блока .code-wrap: живой Monaco-редактор внутри него,
    // textarea (fallback-редактирование без Monaco) или подсвеченный pre.
    function wrapCode(w){
      if(window.monacoEditors)for(var k in monacoEditors){
        var ed=monacoEditors[k],dom=ed&&ed.getDomNode&&ed.getDomNode();
        if(dom&&w.contains(dom))return ed.getValue();
      }
      var ta=w.querySelector('textarea.os-edit');
      if(ta&&ta.style.display!=='none')return ta.value;
      var pre=w.querySelector('pre.os-code');
      return pre?pre.textContent:'';
    }
    // Контекст для модели: последний фокусированный редактор (тот же механизм,
    // что у синтакс-помощника), если он на экране; иначе — все блоки кода
    // видимой панели. Глобальный перебор monaco.editor.getEditors() не годится:
    // редакторы накапливаются при переключении панелей и не уничтожаются,
    // поэтому первым в списке навсегда остаётся самый старый.
    function activeCode(){
      try{
        var id=window._lastFocusedEditorId,ed=id&&window.monacoEditors&&monacoEditors[id];
        if(ed){var dom=ed.getDomNode&&ed.getDomNode();if(dom&&dom.offsetParent!==null){var v=ed.getValue();if(v&&v.trim())return{text:v,label:id};}}
      }catch(e){}
      try{
        var panel=document.querySelector('.cfg-panel.active');
        if(panel){
          var parts=[];
          panel.querySelectorAll('.code-wrap').forEach(function(w){var t=wrapCode(w);if(t&&t.trim())parts.push(t.trim());});
          if(parts.length){
            var ttl=panel.querySelector('.panel-title');
            return{text:parts.join('\n\n'),label:ttl?ttl.textContent.trim():panel.id};
          }
        }
      }catch(e){}
      return null;
    }
    send.addEventListener('click',function(){
      var p=prompt.value.trim();if(!p){msg.textContent='Введите запрос';msg.style.color='#c00';return;}
      var body={prompt:p},ctxNote='';
      if(useCode.checked){
        var c=activeCode();
        if(c){body.code=c.text;ctxNote=' (контекст: '+c.label+')';}
        else ctxNote=' (код не найден — запрос без контекста)';
      }
      msg.textContent='Генерация...'+ctxNote;msg.style.color='#666';out.textContent='';copy.style.display='none';send.disabled=true;
      fetch('/bases/'+base+'/configurator/ai-assist',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
        .then(function(r){return r.json();})
        .then(function(d){
          if(d&&d.ok){out.textContent=d.text;copy.style.display='';msg.textContent='Модель: '+(d.model||'')+ctxNote;msg.style.color='#16a34a';}
          else{out.textContent=(d&&d.error)||'Ошибка';msg.textContent='Ошибка';msg.style.color='#c00';}
        })
        .catch(function(){msg.textContent='Ошибка сети';msg.style.color='#c00';})
        .finally(function(){send.disabled=false;});
    });
    copy.addEventListener('click',function(){try{navigator.clipboard.writeText(out.textContent);msg.textContent='Скопировано';msg.style.color='#16a34a';}catch(e){}});
  }

  // Генератор каркаса: ТЗ → ai-generate (предложенный diff) → Применить (ai-apply).
  function initGenHandlers(){
    var btn=document.getElementById('cfggen-btn');
    var prompt=document.getElementById('cfggen-prompt'),send=document.getElementById('cfggen-send');
    var out=document.getElementById('cfggen-out'),msg=document.getElementById('cfggen-msg'),apply=document.getElementById('cfggen-apply');
    var lastChanges=null;
    btn.addEventListener('click',function(){openPanel('cfggen-panel');prompt.focus();});
    document.getElementById('cfggen-close').addEventListener('click',function(){closePanel('cfggen-panel');});
    function renderCheck(check,checkText,repairRounds){
      if(!check&&!checkText)return;
      var ok=check&&check.ok;
      var box=document.createElement('div');box.className='cfggen-check';box.style.cssText='margin-bottom:8px;border:1px solid '+(ok?'#bbf7d0':'#fecaca')+';border-radius:8px;background:'+(ok?'#f0fdf4':'#fef2f2')+';overflow:hidden';
      var head=document.createElement('div');head.style.cssText='padding:7px 8px;font-size:12px;font-weight:600;color:'+(ok?'#166534':'#991b1b');
      head.textContent=ok?'Проверка: OK':'Проверка: ошибки '+((check&&check.total)||'');
      if(repairRounds){head.textContent+='; исправлений: '+repairRounds;}
      box.appendChild(head);
      if(checkText){
        var pre=document.createElement('pre');pre.style.cssText='margin:0;border-top:1px solid '+(ok?'#bbf7d0':'#fecaca')+';padding:7px 8px;font-size:11px;white-space:pre-wrap;word-break:break-word;color:#111827;background:rgba(255,255,255,.55)';pre.textContent=checkText;
        box.appendChild(pre);
      }
      out.appendChild(box);
    }
    function renderChanges(changes,note,check,checkText,repairRounds){
      lastChanges=changes||[];
      out.innerHTML='';
      if(note){var n=document.createElement('div');n.style.cssText='color:#475569;font-size:12px;margin-bottom:6px;white-space:pre-wrap';n.textContent=note;out.appendChild(n);}
      renderCheck(check,checkText,repairRounds||0);
      if(!lastChanges.length){var e=document.createElement('div');e.style.cssText='color:#94a3b8;font-size:12px';e.textContent='Модель не предложила объектов.';out.appendChild(e);apply.style.display='none';return;}
      lastChanges.forEach(function(ch,idx){
        var wrap=document.createElement('div');
        wrap.className='cfggen-change';
        wrap.dataset.idx=String(idx);
        wrap.style.cssText='margin-bottom:10px;border:1px solid #e2e8f0;border-radius:8px;overflow:hidden;background:#fff';
        var h=document.createElement('label');
        h.style.cssText='display:flex;gap:8px;align-items:center;padding:7px 9px;background:#f8fafc;border-bottom:1px solid #e2e8f0;font-weight:600;font-size:12px;color:#0f172a;cursor:pointer';
        var cb=document.createElement('input');
        cb.type='checkbox';
        cb.checked=true;
        cb.className='cfggen-change-check';
        cb.style.cssText='width:14px;height:14px;margin:0;flex:0 0 auto';
        var title=document.createElement('span');
        title.textContent=(ch.kind||'')+': '+ch.path;
        title.style.cssText='overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
        h.appendChild(cb);h.appendChild(title);wrap.appendChild(h);
        var grid=document.createElement('div');
        grid.style.cssText='display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:0';
        if(ch.oldContent){
          var oldBox=document.createElement('div');
          oldBox.style.cssText='padding:8px;border-right:1px solid #e2e8f0;min-width:0';
          var oldLbl=document.createElement('div');
          oldLbl.textContent='Было';
          oldLbl.style.cssText='font-size:11px;color:#64748b;margin-bottom:4px';
          var oldPre=document.createElement('pre');
          oldPre.className='cfggen-old-content';
          oldPre.style.cssText='margin:0;min-height:160px;max-height:320px;overflow:auto;background:#fff7ed;border:1px solid #fed7aa;border-radius:6px;padding:6px;font-size:11px;white-space:pre-wrap;word-break:break-word';
          oldPre.textContent=ch.oldContent||'';
          oldBox.appendChild(oldLbl);oldBox.appendChild(oldPre);grid.appendChild(oldBox);
        }
        var newBox=document.createElement('div');
        newBox.style.cssText='padding:8px;min-width:0';
        var newLbl=document.createElement('div');
        newLbl.textContent='Будет';
        newLbl.style.cssText='font-size:11px;color:#64748b;margin-bottom:4px';
        var ta=document.createElement('textarea');
        ta.className='cfggen-new-content';
        ta.value=ch.newContent||'';
        ta.spellcheck=false;
        ta.style.cssText='width:100%;box-sizing:border-box;min-height:180px;max-height:420px;resize:vertical;background:#f8fafc;border:1px solid #cbd5e1;border-radius:6px;padding:6px;font-size:11px;line-height:1.35;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;white-space:pre;overflow:auto';
        newBox.appendChild(newLbl);newBox.appendChild(ta);grid.appendChild(newBox);
        wrap.appendChild(grid);out.appendChild(wrap);
      });
      apply.style.display='';
    }
    function selectedChanges(){
      var selected=[];
      out.querySelectorAll('.cfggen-change').forEach(function(card){
        var idx=Number(card.dataset.idx||'-1');
        var src=lastChanges&&lastChanges[idx];
        if(!src)return;
        var cb=card.querySelector('.cfggen-change-check');
        if(cb&&!cb.checked)return;
        var ta=card.querySelector('.cfggen-new-content');
        var ch={path:src.path,kind:src.kind,newContent:ta?ta.value:(src.newContent||'')};
        if(src.oldContent)ch.oldContent=src.oldContent;
        selected.push(ch);
      });
      return selected;
    }
    function genNote(text,trace){
      var note=text||'';
      if(trace&&trace.length){
        note += (note?'\n\n':'')+'Tool trace:\n' + trace.map(function(t){
          return '- '+t.name+(t.isError?' [error]':'')+': '+(t.result||'');
        }).join('\n');
      }
      return note;
    }
    send.addEventListener('click',function(){
      var p=prompt.value.trim();if(!p){msg.textContent='Введите описание';msg.style.color='#c00';return;}
      msg.textContent='Генерация каркаса…';msg.style.color='#666';out.innerHTML='';apply.style.display='none';lastChanges=null;send.disabled=true;
      fetch('/bases/'+base+'/configurator/ai-generate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({prompt:p})})
        .then(function(r){return r.json();})
        .then(function(d){
          if(d&&d.ok){msg.textContent='Модель: '+(d.model||'');msg.style.color='#16a34a';renderChanges(d.changes,genNote(d.text,d.toolTrace),d.check,d.checkText,d.repairRounds);}
          else{msg.textContent='Ошибка';msg.style.color='#c00';renderChanges((d&&d.changes)||[],genNote((d&&d.error)||'Ошибка',d&&d.toolTrace),d&&d.check,d&&d.checkText,d&&d.repairRounds);}
        })
        .catch(function(){msg.textContent='Ошибка сети';msg.style.color='#c00';})
        .finally(function(){send.disabled=false;});
    });
    apply.addEventListener('click',function(){
      if(!lastChanges||!lastChanges.length)return;
      var changes=selectedChanges();
      if(!changes.length){msg.textContent='Выберите хотя бы один файл';msg.style.color='#c00';return;}
      msg.textContent='Применение…';msg.style.color='#666';apply.disabled=true;
      fetch('/bases/'+base+'/configurator/ai-apply',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({changes:changes})})
        .then(function(r){return r.json();})
        .then(function(d){
          if(d&&d.ok){
            var suffix=d.check&&d.check.ok?' Проверка: OK.':'';
            var versions=d.beforeVersion&&d.afterVersion?' Snapshot: '+d.beforeVersion+' → '+d.afterVersion+'.':'';
            msg.textContent='Применено файлов: '+d.applied+'.'+suffix+versions+' Выполните миграцию базы, чтобы создать таблицы. Обновление…';msg.style.color='#16a34a';setTimeout(function(){location.reload();},1800);
          }
          else{
            msg.textContent=(d&&d.error)||'Ошибка применения';msg.style.color='#c00';
            if(d&&d.check&&d.check.issues){
              var note='Ошибки check:\n'+d.check.issues.slice(0,8).map(function(i){return '- '+(i.file||'')+' '+(i.code?'['+i.code+'] ':'')+i.message;}).join('\n');
              renderChanges(changes,note,d.check);
            }
          }
        })
        .catch(function(){msg.textContent='Ошибка сети';msg.style.color='#c00';})
        .finally(function(){apply.disabled=false;});
    });
  }
})();
