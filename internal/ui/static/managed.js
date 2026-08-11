// Runtime управляемых форм. Данные страницы приходят через JSON script tags
// в templates_managed.go; здесь не должно быть Go-template интерполяций.
function obManagedReadJSON(id, fallback) {
  if (typeof obReadJSONScript === 'function') return obReadJSONScript(id, fallback);
  var el = document.getElementById(id);
  if (!el) return fallback;
  try { return JSON.parse(el.textContent || ''); } catch (e) { return fallback; }
}

function obManagedConfig() {
  return obManagedReadJSON('ob-managed-config', {}) || {};
}

function obManagedReady(fn) {
  if (typeof obReady === 'function') return obReady(fn);
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', fn);
  else fn();
}

// WeakSet не даёт повторному requestSubmit() снова попасть в асинхронное
// ожидание FileReader и защищает от нескольких отправок, пока чтение ещё идёт.
var obManagedFileSubmitPending = new WeakSet();
var obManagedFileSubmitReentry = new WeakSet();

function obManagedTableReadOnly(tbody) {
  return !!tbody && tbody.getAttribute('data-ob-table-readonly') === '1';
}

function obManagedTableBodies(id, metadataAttr) {
  var matches = [];
  var candidates = document.querySelectorAll ? document.querySelectorAll('tbody[' + metadataAttr + ']') : [];
  for (var i = 0; i < candidates.length; i++) {
    var candidateID = candidates[i].id || (candidates[i].getAttribute && candidates[i].getAttribute('id'));
    if (candidateID === id) matches.push(candidates[i]);
  }
  if (!matches.length) {
    var fallback = document.getElementById(id);
    if (fallback) matches.push(fallback);
  }
  return matches;
}

function obManagedWritableTableBody(id, metadataAttr) {
  var bodies = obManagedTableBodies(id, metadataAttr);
  for (var i = 0; i < bodies.length; i++) {
    if (!obManagedTableReadOnly(bodies[i])) return bodies[i];
  }
  return null;
}

// Shared by the managed-form response renderer and the separate SlickGrid
// runtime below. Keep this outside either IIFE: Slick submit/event sync must
// be able to update the authoritative hidden payload in the real browser
// scope, not only in tests that happen to provide a same-named global.
function obManagedSetTablePartJSON(tpName, rows) {
  var fieldName = 'tp_json.' + tpName;
  var inputs = document.getElementsByName ? document.getElementsByName(fieldName) : [];
  if (!inputs || !inputs.length) {
    var fallback = document.getElementById('tp-json-' + tpName);
    inputs = fallback ? [fallback] : [];
  }
  var value = JSON.stringify(rows || []);
  for (var i = 0; i < inputs.length; i++) {
    // Readonly duplicate placements are display-only and must never become
    // successful form controls, even if an older/custom template rendered
    // a disabled mirror with the canonical name.
    if (!inputs[i].disabled) inputs[i].value = value;
  }
}
window.obManagedSetTablePartJSON = obManagedSetTablePartJSON;

// Отправляет текущие form-values + имя элемента/события в /ui/.../form-event,
// получает JSON с новыми значениями и сообщениями от Сообщить(), применяет их.
(function(){
  var cfg = obManagedConfig();
  if (!cfg.url) return;
  var URL = String(cfg.url || '');
  var DOC_ID = cfg.docId == null ? '' : String(cfg.docId);
  var SERVICE_FIELDS = cfg.serviceFields && typeof cfg.serviceFields === 'object' ? cfg.serviceFields : {};
  function serviceField(name){
    var mapped = SERVICE_FIELDS[name];
    return typeof mapped === 'string' && mapped ? mapped : name;
  }
  // Реквизиты формы (attributes с save:false) переживают полную перезагрузку.
  // «Записать» уходит POST'ом с редиректом, страница рисуется заново — и всё,
  // что оператор выбрал в реквизите формы, пропадало (в 1С форма живёт в памяти
  // клиента, и реквизиты запись переживают). Значения кладём в sessionStorage
  // ровно на время перезагрузки: пишем перед отправкой, применяем и СРАЗУ
  // удаляем при следующей загрузке — дольше одной навигации они не живут.
  var FORM_ATTRS = Array.isArray(cfg.formAttrs) ? cfg.formAttrs : [];
  var ATTR_STASH_KEY = 'ob-form-attrs:' + String(cfg.entity || '');
  function stashFormAttrs(){
    if (!FORM_ATTRS.length) return;
    var form = document.getElementById('main-form');
    if (!form) return;
    var data = {};
    for (var i = 0; i < FORM_ATTRS.length; i++) {
      var el = form.querySelector('[name="' + (window.CSS && CSS.escape ? CSS.escape(FORM_ATTRS[i]) : FORM_ATTRS[i]) + '"]');
      if (el && el.type !== 'checkbox' && el.value) data[FORM_ATTRS[i]] = el.value;
    }
    try {
      if (Object.keys(data).length) sessionStorage.setItem(ATTR_STASH_KEY, JSON.stringify(data));
      else sessionStorage.removeItem(ATTR_STASH_KEY);
    } catch (e) { /* приватный режим — просто не восстановим */ }
  }
  function restoreFormAttrs(){
    if (!FORM_ATTRS.length) return;
    var raw = null;
    try { raw = sessionStorage.getItem(ATTR_STASH_KEY); sessionStorage.removeItem(ATTR_STASH_KEY); } catch (e) { return; }
    if (!raw) return;
    var data;
    try { data = JSON.parse(raw); } catch (e) { return; }
    var form = document.getElementById('main-form');
    if (!form || !data) return;
    Object.keys(data).forEach(function(k){
      var el = form.querySelector('[name="' + (window.CSS && CSS.escape ? CSS.escape(k) : k) + '"]');
      if (el && !el.value) el.value = data[k];
    });
  }

  window._tpRefOpts = obManagedReadJSON('ob-managed-tp-ref-opts', window._tpRefOpts || {}) || {};
  window._tpEnumLabels = obManagedReadJSON('ob-managed-tp-enum-labels', window._tpEnumLabels || {}) || {};
  window._tpEnumOrder = obManagedReadJSON('ob-managed-tp-enum-order', window._tpEnumOrder || {}) || {};

  function ensureBanner(){
    let b = document.getElementById('ob-fmevt-banner');
    if (b) return b;
    b = document.createElement('div');
    b.id = 'ob-fmevt-banner';
    b.style.cssText = 'position:fixed;top:14px;right:14px;max-width:380px;z-index:9999;display:flex;flex-direction:column;gap:6px;pointer-events:none';
    document.body.appendChild(b);
    return b;
  }
  function flash(text, kind){
    const b = ensureBanner();
    const d = document.createElement('div');
    const palette = kind === 'err'
      ? 'background:#fee2e2;color:#991b1b;border:1px solid #fecaca'
      : 'background:#d1fae5;color:#065f46;border:1px solid #a7f3d0';
    d.style.cssText = palette + ';padding:8px 28px 8px 12px;border-radius:6px;font-size:12px;box-shadow:0 1px 3px rgba(0,0,0,.08);pointer-events:auto;cursor:copy;position:relative;white-space:pre-wrap;word-break:break-word';
    const msg = document.createElement('span');
    msg.textContent = text;
    d.appendChild(msg);
    // Клик по тосту — скопировать текст в буфер (удобно для ошибок). Тост не
    // закрывается по клику; для закрытия — крестик.
    d.title = 'Клик — скопировать текст';
    d.onclick = function(){
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(text); }
        else { const ta=document.createElement('textarea'); ta.value=text; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove(); }
        const prev = d.style.boxShadow; d.style.boxShadow='0 0 0 2px #16a34a';
        setTimeout(function(){ d.style.boxShadow=prev; }, 600);
      } catch(e){}
    };
    const x = document.createElement('span');
    x.textContent = '×';
    x.style.cssText = 'position:absolute;top:4px;right:8px;cursor:pointer;font-weight:700;opacity:.55;font-size:14px;line-height:1';
    x.onclick = function(ev){ ev.stopPropagation(); d.remove(); };
    d.appendChild(x);
    b.appendChild(d);
    // Ошибки держим до закрытия крестиком (чтобы успеть прочитать/скопировать);
    // info-сообщения сами исчезают через 5 c.
    if (kind !== 'err') setTimeout(() => d.remove(), 5000);
  }
  // Доступно другим скриптам (например, грид-IIFE показывает ошибки настройки).
  window.obFlash = flash;
  // ensureRefOption добавляет в <select> недостающий <option> для значения,
  // присвоенного обработчиком.
  //
  // Без этого inp.value = val на <select> без такого <option> браузер молча
  // отрабатывает как selectedIndex = -1: поле пустеет, и следующая запись
  // затирает ссылку в базе. А <select> ссылочного реквизита рисуется только
  // первой страницей справочника (50 записей), так что промах — не редкость,
  // а норма для любого живого справочника (#615).
  //
  // Подпись берём из refOptions ответа (сервер догрузил её той же дорогой, что
  // и выбранное значение при отрисовке, — с маской ПДн и проверкой доступа).
  // Если её нет, ставим сам идентификатор: он некрасив, но значение сохраняется,
  // а это важнее подписи.
  function ensureRefOption(sel, val, rows){
    if (!val) return;
    for (let i = 0; i < sel.options.length; i++){
      if (String(sel.options[i].value) === String(val)) return;
    }
    let label = val;
    if (rows) {
      for (let i = 0; i < rows.length; i++){
        if (rows[i] && String(rows[i].id) === String(val)) {
          if (rows[i]._label != null && rows[i]._label !== '') label = rows[i]._label;
          break;
        }
      }
    }
    const o = document.createElement('option');
    o.value = val;
    o.textContent = label;
    sel.appendChild(o);
  }
  function applyValues(values, refOptions){
    if (!values) return;
    const form = document.getElementById('main-form');
    if (!form) return;
    Object.keys(values).forEach(function(k){
      const v = values[k];
      // Пропускаем файловые поля: не подставляем содержимое в поле пути
      const fc = form.querySelector('[data-ob-file-content-for="' + (window.CSS && CSS.escape ? CSS.escape(k) : k) + '"]');
      if (fc) return;
      const inp = form.querySelector('[name="' + (window.CSS && CSS.escape ? CSS.escape(k) : k) + '"]');
      if (!inp) return;
      if (inp.type === 'checkbox') {
        inp.checked = v === true || v === 'true' || v === 1;
      } else {
        var val = (v === null || v === undefined) ? '' : String(v);
        // Сервер сериализует дату как «2026-08-04T00:00» (формат datetime-local).
        // Для <input type="date"> это невалидное значение: браузер молча очищает
        // поле — дата на форме пропадала после первого же события, а следующая
        // запись затирала её в базе.
        if (inp.type === 'date' && val.indexOf('T') > 0) val = val.slice(0, val.indexOf('T'));
        if (inp.tagName === 'SELECT') ensureRefOption(inp, val, refOptions && refOptions[k]);
        if (inp.classList && inp.classList.contains('code-field') && inp._obSetCodeValue) {
          inp._obSetCodeValue(val);
        } else {
          inp.value = val;
        }
      }
    });
  }
  // applyChoiceList — заполняет <select> элемента ПолеСписка динамическим списком
  // значений из ответа НачалоВыбора (choiceList). Текущее значение сохраняется,
  // если присутствует в новом списке.
  function applyChoiceList(elName, list){
    if (!elName || !list) return;
    const sel = document.querySelector('select[data-el="' + (window.CSS && CSS.escape ? CSS.escape(elName) : elName) + '"]');
    if (!sel) return;
    const cur = sel.value;
    while (sel.options.length) sel.remove(0);
    const o0 = document.createElement('option'); o0.value = ''; o0.textContent = '— выбрать —'; sel.appendChild(o0);
    for (let i = 0; i < list.length; i++){
      const o = document.createElement('option');
      o.value = list[i].value;
      o.textContent = (list[i].label != null && list[i].label !== '') ? list[i].label : list[i].value;
      if (String(list[i].value) === String(cur)) o.selected = true;
      sel.appendChild(o);
    }
  }
  // obStartListChoice — событие НачалоВыбора для ПолеСписка: на фокусе элемента
  // обработчик формы формирует список значений (ДобавитьЗначениеСписка), ответ
  // приходит в choiceList и применяется applyChoiceList. Флаг busy защищает от
  // повторных одновременных запросов по одному элементу.
  window.obStartListChoice = function(elName){
    window._obListChoiceBusy = window._obListChoiceBusy || {};
    if (!elName || window._obListChoiceBusy[elName] || !window.obFire) return;
    window._obListChoiceBusy[elName] = true;
    Promise.resolve(window.obFire(elName, 'НачалоВыбора')).catch(function(){}).then(function(){
      window._obListChoiceBusy[elName] = false;
    });
  };
  function obFormRowClass(row) {
    return row && row._form_row_class ? String(row._form_row_class) : '';
  }
  function obFormCellClass(row, field) {
    var cc = row && row._form_cell_classes;
    if (!cc || !field) return '';
    if (cc[field]) return String(cc[field]);
    var want = String(field).toLowerCase();
    for (var k in cc) {
      if (Object.prototype.hasOwnProperty.call(cc, k) && String(k).toLowerCase() === want) {
        return cc[k] ? String(cc[k]) : '';
      }
    }
    return '';
  }
  function applyFormConditionalCSS(css) {
    var id = 'ob-form-conditional-runtime-css';
    var el = document.getElementById(id);
    if (!el) {
      el = document.createElement('style');
      el.id = id;
      document.head.appendChild(el);
    }
    el.textContent = css || '';
  }
  window.applyFormConditionalCSS = applyFormConditionalCSS;
  // Перерисовка табчастей по ответу сервера. tbody у нас имеет
  // id=mtp-body-<TP> и атрибут data-tp-fields="name|type[:Ref],name|type,..."
  // где field-meta использовалось для определения типа input при первичном рендере;
  // тот же формат используется тут для повторного создания строк.
  // Keep every representation of the same entity table part in sync. A
  // managed form may place one TP more than once (for example, a readonly
  // summary and an editable grid), so getElementById alone is not sufficient.
  function applyTableParts(tps){
    if (!tps) return;
    Object.keys(tps).forEach(function(tpName){
      window.obManagedSetTablePartJSON(tpName, tps[tpName] || []);
      const tableBodies = obManagedTableBodies('tp-body-' + tpName, 'data-tp-fields');
      if (!tableBodies.length) return;
      tableBodies.forEach(function(tbody){
      const fieldsMeta = (tbody.getAttribute('data-tp-fields') || '').split(',').map(function(s){
        const idx = s.indexOf('|');
        if (idx < 0) return { name: s, type: 'string', ref: '' };
        const name = s.slice(0, idx);
        const rest = s.slice(idx + 1);
        const refIdx = rest.indexOf(':');
        if (refIdx >= 0) return { name: name, type: rest.slice(0, refIdx), ref: rest.slice(refIdx + 1) };
        return { name: name, type: rest, ref: '' };
      });
      const rows = tps[tpName] || [];
      const refOpts = (window._tpRefOpts && window._tpRefOpts[tpName]) || {};
      const tpEnumLabels = (window._tpEnumLabels && window._tpEnumLabels[tpName]) || {};
      const tpEnumOrder = (window._tpEnumOrder && window._tpEnumOrder[tpName]) || {};
      const hasCmd = tbody.getAttribute('data-tp-cmd') === '1';
      const readOnly = obManagedTableReadOnly(tbody);
      const domTable = tbody.closest && tbody.closest('table[data-ob-dom-table]');
      const domWritable = !!(domTable && domTable.getAttribute('data-ob-readonly') === '0');
      const focused = domTable && document.activeElement && domTable.contains(document.activeElement);
      const focusedRow = focused && document.activeElement.closest ? document.activeElement.closest('tr') : null;
      const restoreRow = focusedRow || (domTable && domTable._obCurrentRow && domTable.contains(domTable._obCurrentRow) ? domTable._obCurrentRow : null);
      const restoreIndex = restoreRow ? restoreRow.sectionRowIndex : -1;
      const focusedName = focused && document.activeElement.getAttribute ? (document.activeElement.getAttribute('name') || '') : '';
      const namePrefix = 'tp.' + tpName + '.';
      var restoreField = '';
      if (focusedName.indexOf(namePrefix) === 0) {
        const rest = focusedName.slice(namePrefix.length);
        const dot = rest.indexOf('.');
        if (dot >= 0) restoreField = rest.slice(dot + 1);
      }
      if (domTable) domTable._obCurrentRow = null;
      tbody.innerHTML = '';
      rows.forEach(function(row, idx){
        const tr = document.createElement('tr');
        tr.className = obFormRowClass(row);
        if (hasCmd) {
          const tdSel = document.createElement('td');
          tdSel.style.textAlign = 'center';
          const cbSel = document.createElement('input');
          cbSel.type = 'checkbox'; cbSel.className = '_tp-sel';
          cbSel.disabled = readOnly;
          tdSel.appendChild(cbSel);
          tr.appendChild(tdSel);
        }
        fieldsMeta.forEach(function(f){
          const td = document.createElement('td');
          td.className = obFormCellClass(row, f.name);
          const v = row[f.name];
          const isRef = f.type === 'reference' || f.type.indexOf('reference') === 0;
          const isEnum = f.type === 'enum' || f.type.indexOf('enum') === 0;
          if (isRef && refOpts[f.name]) {
            const sel = document.createElement('select');
            sel.name = 'tp.' + tpName + '.' + idx + '.' + f.name;
            const empty = document.createElement('option');
            empty.value = ''; empty.textContent = '— выбрать —';
            sel.appendChild(empty);
            // v приходит сериализованным как UUID-string (serializeTablePartRows),
            // но на всякий случай учитываем и legacy-формат с GetRefUUID-методом.
            const cur = (v && typeof v === 'object' && v.GetRefUUID) ? v.GetRefUUID() : (v == null ? '' : String(v));
            refOpts[f.name].forEach(function(opt){
              const o = document.createElement('option');
              o.value = opt.id;
              o.textContent = opt._label;
              if (String(opt.id) === cur) o.selected = true;
              sel.appendChild(o);
            });
            sel.disabled = readOnly;
            td.appendChild(sel);
          } else if (isEnum && tpEnumLabels[f.name]) {
            const enumLabMap = tpEnumLabels[f.name];
            const sel = document.createElement('select');
            sel.name = 'tp.' + tpName + '.' + idx + '.' + f.name;
            const cur = (v == null ? '' : String(v));
            // Используем _tpEnumOrder для правильного порядка значений (порядок
            // объявления values:), а не алфавитный Object.keys(enumLabMap).
            const orderedVals = (tpEnumOrder[f.name] && tpEnumOrder[f.name].length > 0)
              ? tpEnumOrder[f.name] : Object.keys(enumLabMap);
            orderedVals.forEach(function(val){
              const o = document.createElement('option');
              o.value = val;
              o.textContent = enumLabMap[val] !== undefined ? enumLabMap[val] : val;
              if (val === cur) o.selected = true;
              sel.appendChild(o);
            });
            sel.disabled = readOnly;
            td.appendChild(sel);
          } else {
            const inp = document.createElement('input');
            inp.name = 'tp.' + tpName + '.' + idx + '.' + f.name;
            if (isRef) {
              inp.type = 'text';
              inp.placeholder = 'UUID';
              inp.value = (v && typeof v === 'object' && v.GetRefUUID) ? v.GetRefUUID() : (v == null ? '' : String(v));
            } else if (f.type === 'number') {
              inp.type = 'number';
              inp.step = 'any';
              inp.value = (v == null ? '' : v);
            } else {
              inp.type = 'text';
              inp.value = (v == null ? '' : v);
            }
            inp.disabled = readOnly;
            td.appendChild(inp);
          }
          tr.appendChild(td);
        });
        const tdDel = document.createElement('td');
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'del-btn';
        btn.textContent = '×';
        btn.disabled = readOnly;
        if (domWritable) {
          btn.setAttribute('data-ob-remove-row', '');
          btn.title = 'Delete';
          btn.setAttribute('aria-keyshortcuts', 'Delete');
        }
        tdDel.appendChild(btn);
        tr.appendChild(tdDel);
        tbody.appendChild(tr);
        if (domTable && window.obDOMPrepareRow) window.obDOMPrepareRow(domTable, tr);
      });
      if (domTable && tbody.rows.length) {
        const rowIndex = restoreIndex >= 0 ? Math.min(restoreIndex, tbody.rows.length - 1) : 0;
        const row = tbody.rows[rowIndex];
        if (focused && window.obDOMSetCurrentRow) {
          window.obDOMSetCurrentRow(domTable, row, false);
          var focusTarget = null;
          if (restoreField) {
            const controls = row.querySelectorAll('[name]');
            for (var controlIndex = 0; controlIndex < controls.length; controlIndex++) {
              const name = controls[controlIndex].getAttribute('name') || '';
              const rest = name.indexOf(namePrefix) === 0 ? name.slice(namePrefix.length) : '';
              const dot = rest.indexOf('.');
              if (dot >= 0 && rest.slice(dot + 1) === restoreField) { focusTarget = controls[controlIndex]; break; }
            }
          }
          if (focusTarget && !focusTarget.disabled && focusTarget.focus) focusTarget.focus();
          else if (row.focus) row.focus();
        } else {
          row.setAttribute('tabindex', '0');
        }
      }
      if (domTable && window.obDOMReindex) window.obDOMReindex(domTable);
      });
    });
  }

  // Экспортируем в window, чтобы grid-aware обёртка (план 48) могла
  // переопределить applyTableParts и обновлять SlickGrid после round-trip.
  // obFire ниже зовёт именно window.applyTableParts — так обёртка попадает
  // в цепочку, а не остаётся мёртвым кодом.
  window.applyTableParts = applyTableParts;

  // applyFormTables(vts) — перерисовка ValueTable (формовых атрибутов-таблиц) по
  // ответу сервера (formTables). Зеркалит applyTableParts, но для tbody
  // id=vt-body-<name>: имена инпутов vt.<name>.<idx>.<field>, типы берутся из
  // data-vt-fields ("name|TypeRef,..."). Нужна, чтобы VT обновлялась после
  // round-trip-события (ПриИзменении и т.п.) — раньше сервер слал formTables, но
  // клиент их игнорировал, и таблица «застывала».
  function applyFormTables(vts){
    if (!vts) return;
    Object.keys(vts).forEach(function(vtName){
      var tableBodies = obManagedTableBodies('vt-body-' + vtName, 'data-vt-fields');
      if (!tableBodies.length) return;
      tableBodies.forEach(function(tbody){
      var fieldsMeta = (tbody.getAttribute('data-vt-fields') || '').split(',').map(function(s){
        var idx = s.indexOf('|');
        if (idx < 0) return { name: s, type: 'string' };
        return { name: s.slice(0, idx), type: (s.slice(idx + 1) || 'string').toLowerCase() };
      });
      var rows = vts[vtName] || [];
      var readOnly = obManagedTableReadOnly(tbody);
      tbody.innerHTML = '';
      rows.forEach(function(row, idx){
        var tr = document.createElement('tr');
        tr.className = obFormRowClass(row);
        fieldsMeta.forEach(function(f){
          var td = document.createElement('td');
          td.className = obFormCellClass(row, f.name);
          var v = row[f.name];
          var inp = document.createElement('input');
          inp.name = 'vt.' + vtName + '.' + idx + '.' + f.name;
          if (f.type === 'number') {
            inp.type = 'number'; inp.step = 'any';
            inp.setAttribute('data-vt-num', f.name);
            inp.value = (v == null ? '' : v);
          } else if (f.type === 'bool') {
            inp.type = 'checkbox'; inp.value = 'true';
            if (String(v) === 'true') inp.checked = true;
            inp.disabled = readOnly;
          } else {
            inp.type = 'text';
            inp.value = (v == null ? '' : v);
          }
          inp.disabled = readOnly;
          td.appendChild(inp);
          tr.appendChild(td);
        });
        var tdDel = document.createElement('td');
        var btn = document.createElement('button');
        btn.type = 'button'; btn.className = 'del-btn'; btn.textContent = '×';
        btn.disabled = readOnly;
        if (!readOnly) btn.onclick = function(){ tr.remove(); };
        tdDel.appendChild(btn);
        tr.appendChild(tdDel);
        tbody.appendChild(tr);
      });
      });
    });
  }
  window.applyFormTables = applyFormTables;

  function beginFileRead(contentEl) {
    const previous = contentEl._obFileReadState;
    if (previous && typeof previous.settle === 'function') previous.settle();
    let resolvePending;
    let settled = false;
    const state = {
      token: previous ? previous.token + 1 : 1,
      loading: true,
      error: null,
      pending: new Promise(function(resolve){ resolvePending = resolve; }),
      settle: function(){
        if (settled) return;
        settled = true;
        resolvePending();
      }
    };
    contentEl._obFileReadState = state;
    return state;
  }

  function finishFileRead(contentEl, state, error) {
    if (contentEl._obFileReadState === state) {
      state.loading = false;
      state.error = error || null;
    }
    state.settle();
  }

  async function awaitCurrentFileReads(fileHelpers) {
    for (;;) {
      const states = fileHelpers.map(function(el){ return el._obFileReadState || null; });
      await Promise.all(states.map(function(state){ return state ? state.pending : Promise.resolve(); }));
      // A newer selection may have replaced a reader while obFire was waiting.
      // The superseded promise is settled immediately; loop and await only the
      // currently selected file instead of blocking on or sending stale data.
      if (fileHelpers.some(function(el, i){ return (el._obFileReadState || null) !== states[i]; })) continue;
      for (let i = 0; i < states.length; i++) {
        if (states[i] && states[i].error) {
          flash(states[i].error, 'err');
          return false;
        }
      }
      return true;
    }
  }

  // obFilePick — при выборе файла: имя в текстовое поле, содержимое в скрытый
  // textarea. Кодировка: UTF-8 → fallback Windows-1251 (TextDecoder).
  // В webview/Electron — file.path вместо содержимого. Состояние чтения живёт
  // на backing textarea: obFire ждёт актуальный Promise и не отправляет форму
  // после ошибки чтения.
  window.obFilePick = function(input, pathId, contentId) {
    const pathEl = document.getElementById(pathId);
    const contentEl = contentId ? document.getElementById(contentId) : null;
    if (!pathEl) return;
    const file = input.files && input.files[0];
    if (!file) {
      pathEl.value = '';
      if (contentEl) {
        const state = beginFileRead(contentEl);
        contentEl.value = '';
        delete contentEl.dataset.obFileContentReady;
        finishFileRead(contentEl, state, null);
      }
      return;
    }
    if (file.path) {
      pathEl.value = file.path;
      if (contentEl) {
        const state = beginFileRead(contentEl);
        contentEl.value = '';
        delete contentEl.dataset.obFileContentReady;
        finishFileRead(contentEl, state, null);
      }
      return;
    }
    pathEl.value = file.name;
    if (!contentEl) return;
    const state = beginFileRead(contentEl);
    contentEl.value = '';
    delete contentEl.dataset.obFileContentReady;
    try {
      if (typeof window.FileReader !== 'function') throw new Error('FileReader API недоступен');
      const reader = new window.FileReader();
      reader.onload = function() {
        try {
          const bytes = new Uint8Array(reader.result);
          let text;
          try {
            text = new TextDecoder('utf-8', {fatal: true}).decode(bytes);
          } catch(e) {
            text = new TextDecoder('windows-1251').decode(bytes);
          }
          if (contentEl._obFileReadState === state) {
            contentEl.value = text;
            contentEl.dataset.obFileContentReady = '1';
          }
          finishFileRead(contentEl, state, null);
        } catch (e) {
          finishFileRead(contentEl, state, 'Не удалось прочитать файл «' + file.name + '»: ' + (e && e.message ? e.message : e));
        }
      };
      reader.onerror = function() {
        const detail = reader.error && reader.error.message ? ': ' + reader.error.message : '';
        finishFileRead(contentEl, state, 'Не удалось прочитать файл «' + file.name + '»' + detail);
      };
      reader.onabort = function() {
        finishFileRead(contentEl, state, 'Чтение файла «' + file.name + '» отменено');
      };
      reader.readAsArrayBuffer(file);
    } catch (e) {
      finishFileRead(contentEl, state, 'Не удалось прочитать файл «' + file.name + '»: ' + (e && e.message ? e.message : e));
    }
  };

  // obFire(elementName, eventName[, extraParams]) — extraParams (объект)
  // добавляются к телу запроса. Используется подбором (план 46): фаза 2
  // шлёт {_pick_result}, команды ТЧ — {_tp, _tp_selected}.
  window.obFire = async function(elementName, eventName, extraParams){
   try {
    // Зафиксировать активную правку и синхронизировать ТЧ. При невалидной
    // ссылке или исключении editor-lock отправлять старое tp_json нельзя.
    if (window.obGridSync && window.obGridSync() === false) return;
    const form = document.getElementById('main-form');
    if (!form) return;
    const fileHelpers = Array.prototype.slice.call(form.querySelectorAll('[data-ob-file-content-for]'));
    if (!await awaitCurrentFileReads(fileHelpers)) return;
    // FileReader может работать долго; за это время активная grid-ячейка могла
    // измениться уже после первого snapshot. Непосредственно перед FormData
    // повторяем commit/sync и при любом veto оставляем событие неотправленным.
    if (window.obGridSync && window.obGridSync() === false) return;
    const body = new URLSearchParams();
    const fileHelperDisabled = fileHelpers.map(function(el){ return el.disabled; });
    let eventFD;
    try {
      fileHelpers.forEach(function(el){ el.disabled = true; });
      eventFD = new FormData(form);
    } finally {
      fileHelpers.forEach(function(el, i){ el.disabled = fileHelperDisabled[i]; });
    }
    eventFD.set(serviceField('_element'), elementName || '');
    eventFD.set(serviceField('_event'), eventName || '');
    eventFD.set(serviceField('_kind'), 'object');
    if (DOC_ID) eventFD.set(serviceField('_id'), DOC_ID);
    eventFD.forEach((v, k) => {
      if (typeof v !== 'string') { body.append(k, ''); return; }
      // If the rendered file helper has content, prefer it over the path.
      // The data attribute identifies the actual control without reserving
      // all legal parameter names beginning with _fc_.
      const fcEl = form.querySelector('[data-ob-file-content-for="' + (window.CSS && CSS.escape ? CSS.escape(k) : k) + '"]');
      if (fcEl && (fcEl.value || fcEl.dataset.obFileContentReady === '1')) { body.append(k, fcEl.value); }
      else { body.append(k, v); }
    });
    // Команда над ТЧ: подмешать индексы выделенных строк (_tp_selected) по
    // имени ТЧ из extraParams._tp.
    if (extraParams && extraParams._tp) {
      // Plan 48: check if SlickGrid exists for this TP
      var obg = (window._obGrids || {})[extraParams._tp];
      if (obg && !obg.readOnly) {
        // getSelectedRows бросает «Selection model is not set», если модель
        // выделения не установлена (плагин не завендорен). Командам подбора/
        // пересчёта/очистки выделение не нужно — гасим ошибку и шлём пусто.
        var sel = [];
        try { sel = obg.grid.getSelectedRows() || []; } catch (e) { sel = []; }
        // tp_json is serialized in canonical _ord order, while SlickGrid
        // selection indices follow the current visual sort. Translate selected
        // rows to the exact array indices sent to the server.
        var canonicalItems = obg.dataView.getItems().slice().sort(function(a, b) {
          return (a._ord || 0) - (b._ord || 0);
        });
        sel = sel.map(function(displayIndex) {
          var item = obg.dataView.getItem(displayIndex);
          if (!item) return -1;
          for (var i = 0; i < canonicalItems.length; i++) {
            if (canonicalItems[i] && canonicalItems[i].id === item.id) return i;
          }
          return -1;
        }).filter(function(index) { return index >= 0; });
        body.append(serviceField('_tp_selected'), sel.join(','));
      } else {
        // Legacy: read from DOM checkboxes
        const tbody = obManagedWritableTableBody('tp-body-' + extraParams._tp, 'data-tp-fields');
        if (tbody) {
          const sel = [];
          Array.prototype.forEach.call(tbody.rows, (tr, i) => {
            const cb = tr.querySelector('._tp-sel');
            if (cb && cb.checked) sel.push(i);
          });
          body.append(serviceField('_tp_selected'), sel.join(','));
        }
      }
    }
    if (extraParams) {
      Object.keys(extraParams).forEach(k => body.append(serviceField(k), extraParams[k]));
    }
    try {
      const res = await fetch(URL, {
        method: 'POST',
        body: body,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded; charset=utf-8' },
        credentials: 'same-origin'
      });
      const data = await res.json();
      // Подбор фазы 1: сервер вернул pickerData — открыть диалог, не трогая
      // ТЧ (её обновит фаза 2 после «Перенести»).
      if (data.pickerData) {
        (data.messages || []).forEach(m => flash(m, 'ok'));
        if (data.error) flash(data.error, 'err');
        openItemPicker(data.pickerData, elementName, extraParams || null);
        return;
      }
      if (Object.prototype.hasOwnProperty.call(data, 'conditionalCss')) applyFormConditionalCSS(data.conditionalCss);
      window.applyTableParts(data.tableparts);
      applyValues(data.values, data.refOptions);
      applyChoiceList(elementName, data.choiceList);
      applyFormTables(data.formTables);
      // Обработчик записал новую форму (Объект.Записать()): дальше она работает
      // с этой записью. Без подмены _id второе действие подряд ушло бы как
      // «новый документ» и создало дубль, а адрес страницы остался бы /new.
      if (data.savedId && !DOC_ID) {
        DOC_ID = String(data.savedId);
        var idInput = document.querySelector('#main-form [name="_id"]');
        if (idInput) idInput.value = DOC_ID;
        if (window.history && history.replaceState) {
          history.replaceState(null, '', location.pathname.replace(/\/new$/, '/' + DOC_ID));
        }
        window._obFormDirty = false;
      }
      // Обработчик, записавший объект, поднял его версию. Форма держит версию,
      // прочитанную при отрисовке, — без обновления следующая «Записать»
      // упирается в «объект изменён другим пользователем».
      if (data.version) {
        var verInput = form.querySelector('[name="_version"]');
        if (!verInput) {
          verInput = document.createElement('input');
          verInput.type = 'hidden';
          verInput.name = '_version';
          form.appendChild(verInput);
        }
        verInput.value = String(data.version);
      }
      (data.messages || []).forEach(m => flash(m, 'ok'));
      if (data.error) flash(data.error, 'err');
    } catch (e) {
      flash('Сетевая ошибка: ' + (e && e.message ? e.message : e), 'err');
    }
   } catch (e) {
      // Синхронные ошибки (obGridSync, сборка формы) больше не «глотаются»
      // как unhandled rejection — показываем баннер, чтобы причина была видна.
      flash('Ошибка формы: ' + (e && e.message ? e.message : e), 'err');
   }
  };

  // Отслеживание «грязной» формы — чтобы Esc/закрытие спрашивало подтверждение
  // только при наличии несохранённых изменений. Плюс пометка несохранённого
  // документа звёздочкой в заголовке вкладки браузера (аналог «*» в 1С) и
  // предупреждение при ЛЮБОМ уходе со страницы — крестик, клик по ссылке,
  // закрытие/обновление вкладки.
  // Сохранить реквизиты формы перед полной отправкой и вернуть после перезагрузки.
  obManagedReady(restoreFormAttrs);
  document.addEventListener('submit', function(e){
    const form = e.target;
    if (!form || !form.querySelectorAll) return;
    if (form.id === 'main-form') stashFormAttrs();

    const fileHelpers = Array.prototype.slice.call(form.querySelectorAll('[data-ob-file-content-for]'));
    if (!fileHelpers.length || obManagedFileSubmitReentry.has(form)) return;

    // В том числе implicit submit по Enter должен быть остановлен до первого
    // await: иначе браузер успеет отправить пустой backing textarea.
    e.preventDefault();
    e.stopPropagation();
    if (obManagedFileSubmitPending.has(form)) return;
    if (!obManagedSubmitAllowed(form)) return;
    obManagedFileSubmitPending.add(form);

    const submitter = e.submitter || null;
    Promise.resolve(awaitCurrentFileReads(fileHelpers)).then(function(ok){
      if (!ok) return;
      // Пока FileReader работал, пользователь мог закончить другую ячейку
      // SlickGrid. Повторно коммитим/sync-им grid state непосредственно перед
      // native submit; confirm второй раз не показываем.
      try {
        if (!obManagedGridSubmitAllowed(form)) return;
      } catch (err) {
        flash('Не удалось синхронизировать таблицу: ' + (err && err.message ? err.message : err), 'err');
        return;
      }

      const proto = window.HTMLFormElement && window.HTMLFormElement.prototype;
      const requestSubmit = proto && typeof proto.requestSubmit === 'function'
        ? proto.requestSubmit
        : (typeof form.requestSubmit === 'function' ? form.requestSubmit : null);
      const nativeSubmit = proto && typeof proto.submit === 'function'
        ? proto.submit
        : (typeof form.submit === 'function' ? form.submit : null);

      obManagedFileSubmitReentry.add(form);
      try {
        if (requestSubmit) {
          // submitter мог исчезнуть из DOM за время чтения; тогда безопаснее
          // повторить implicit submit без него, чем получить NotFoundError.
          if (submitter && (!('form' in submitter) || submitter.form === form) && !submitter.disabled) {
            requestSubmit.call(form, submitter);
          } else {
            requestSubmit.call(form);
          }
          return;
        }
        if (!nativeSubmit) throw new Error('браузер не поддерживает отправку формы');

        // Старый браузер без requestSubmit(): validation/confirm уже выполнены
        // выше. Временное поле сохраняет name/value нажатой submit-кнопки.
        let submitterInput = null;
        if (submitter && submitter.name && !submitter.disabled) {
          submitterInput = document.createElement('input');
          submitterInput.type = 'hidden';
          submitterInput.name = submitter.name;
          submitterInput.value = submitter.value || '';
          form.appendChild(submitterInput);
        }
        try {
          if (form.id === 'main-form') window._obFormDirty = false;
          nativeSubmit.call(form);
        } finally {
          if (submitterInput) submitterInput.remove();
        }
      } catch (err) {
        flash('Не удалось отправить форму: ' + (err && err.message ? err.message : err), 'err');
      } finally {
        obManagedFileSubmitReentry.delete(form);
      }
    }).catch(function(err){
      flash('Не удалось дождаться чтения файла: ' + (err && err.message ? err.message : err), 'err');
    }).finally(function(){
      obManagedFileSubmitPending.delete(form);
    });
  }, true);

  window._obFormDirty = false;
  var _obBaseTitle = document.title;
  function _obMarkDirty(){
    window._obFormDirty = true;
    if (document.title.charAt(0) !== '●') document.title = '● ' + _obBaseTitle;
  }
  document.addEventListener('input',  function(e){ if (e.target && e.target.closest && e.target.closest('#main-form')) _obMarkDirty(); }, true);
  document.addEventListener('change', function(e){ if (e.target && e.target.closest && e.target.closest('#main-form')) _obMarkDirty(); }, true);
  // «Грязный» флаг сбрасывает финальный submit-handler только после всех
  // проверок. Сбрасывать его здесь нельзя: obGridSync ниже ещё может запретить
  // отправку из-за незавершённой/невалидной правки ячейки.
  window.addEventListener('beforeunload', function(e){
    if (window._obFormDirty) { e.preventDefault(); e.returnValue = ''; return ''; }
  });

  // Esc — отмена незаконченного ввода / закрытие формы (как в 1С). Порядок:
  //   1) открыт модальный диалог (подбор/выбор ссылки) → закрыть его;
  //   2) открыт выпадающий список ячейки-ссылки → закрыть только список;
  //   3) активен редактор ячейки грида → отменить правку ячейки (форму НЕ
  //      закрываем);
  //   4) фокус в поле ввода → снять фокус (отменить ввод);
  //   5) иначе → закрыть форму (с подтверждением, если были изменения).
  //
  // ВАЖНО: слушатель в ФАЗЕ ПЕРЕХВАТА (capture=true). В фазе всплытия SlickGrid
  // успевал отменить правку РАНЬШЕ нас, editor-lock становился неактивным, и мы
  // ошибочно закрывали документ прямо из редактирования ячейки.
  document.addEventListener('keydown', function(e){
    if (e.key !== 'Escape' && e.keyCode !== 27) return;
    var modal = document.getElementById('_item-picker-modal') || document.getElementById('_ref-picker-modal');
    if (modal) { modal.remove(); e.preventDefault(); e.stopPropagation(); return; }
    // Выпадающий список ячейки-ссылки закрываем ДО проверки editor-lock: этот
    // слушатель в фазе перехвата, и без отдельной ветки Esc из подбора отменял
    // бы всю правку ячейки, а не только список.
    if (window._obRefDropdown && window._obRefDropdown.close) {
      window._obRefDropdown.close(); e.preventDefault(); e.stopPropagation(); return;
    }
    var grids = window._obGrids || {};
    for (var tp in grids) {
      var lock = grids[tp].grid && grids[tp].grid.getEditorLock && grids[tp].grid.getEditorLock();
      if (lock && lock.isActive()) { lock.cancelCurrentEdit(); e.preventDefault(); e.stopPropagation(); return; }
    }
    var ae = document.activeElement;
    if (ae && /^(INPUT|SELECT|TEXTAREA)$/.test(ae.tagName) && !ae.readOnly && ae.type !== 'submit' && ae.type !== 'button') {
      ae.blur(); e.preventDefault(); e.stopPropagation(); return;
    }
    var cancel = document.querySelector('a.btn-cancel');
    if (cancel) {
      if (window._obFormDirty && !confirm('Данные были изменены и не записаны. Закрыть форму?')) {
        e.preventDefault(); e.stopPropagation(); return;
      }
      e.preventDefault(); e.stopPropagation(); cancel.click();
    }
  }, true);
})();

// addVtRow — JS для добавления строки в ValueTable (формовый атрибут-таблица).
function addVtRow(vtName, fields, tbodyOverride) {
  var tbody = tbodyOverride || document.getElementById("vt-body-" + vtName);
  if (!tbody || tbody.getAttribute('data-ob-table-readonly') === '1') return;
  var idx = tbody.rows.length;
  var tr = document.createElement("tr");
  var fieldTypes = (tbody.getAttribute("data-vt-fields") || "").split(",");
  fields.forEach(function(fn, i) {
    var td = document.createElement("td");
    var inp = document.createElement("input");
    inp.name = "vt." + vtName + "." + idx + "." + fn;
    var ft = (fieldTypes[i] || "").split("|")[1] || "";
    if (ft === "number") {
      inp.type = "number"; inp.step = "any";
      inp.setAttribute("data-vt-num", fn);
    } else if (ft === "bool") {
      inp.type = "checkbox"; inp.value = "true";
    } else {
      inp.type = "text";
    }
    td.appendChild(inp);
    tr.appendChild(td);
  });
  var tdDel = document.createElement("td");
  var btn = document.createElement("button");
  btn.type = "button"; btn.className = "del-btn"; btn.textContent = "×";
  btn.setAttribute("data-ob-remove-row", "");
  tdDel.appendChild(btn);
  tr.appendChild(tdDel);
  tbody.appendChild(tr);
}

function obManagedClosestSelect(btn) {
  var target = btn.getAttribute('data-ob-ref-picker') || btn.getAttribute('data-ob-ref-current') || '';
  if (target && target !== 'closest') return document.getElementById(target);
  var root = btn.parentElement || (btn.closest && btn.closest('td')) || document;
  return root && root.querySelector ? root.querySelector('select') : null;
}

function obManagedParseFieldMeta(raw) {
  return String(raw || '').split(',').filter(Boolean).map(function (s) {
    var idx = s.indexOf('|');
    if (idx < 0) return { name: s, type: 'string' };
    var rest = s.slice(idx + 1);
    var refIdx = rest.indexOf(':');
    return { name: s.slice(0, idx), type: refIdx >= 0 ? rest.slice(0, refIdx) : rest };
  }).filter(function (f) { return f.name !== ''; });
}

function obManagedAddTpRow(btn) {
  var tpName = btn.getAttribute('data-ob-add-tp') || '';
  var tbody = obManagedWritableTableBody('tp-body-' + tpName, 'data-tp-fields');
  if (!tpName || !tbody || typeof addTpRow !== 'function') return;
  var meta = obManagedParseFieldMeta(tbody.getAttribute('data-tp-fields') || '');
  var fields = meta.map(function (f) { return f.name; });
  var nums = meta.filter(function (f) { return f.type === 'number'; }).map(function (f) { return f.name; });
  addTpRow(tpName, fields, nums, tbody.rows.length, tbody);
  var table = tbody.closest && tbody.closest('table[data-ob-dom-table]');
  if (table && window.obDOMNotifyMutation) window.obDOMNotifyMutation(table, 'add');
}

function obManagedAddVtRow(btn) {
  var vtName = btn.getAttribute('data-ob-add-vt') || '';
  var tbody = obManagedWritableTableBody('vt-body-' + vtName, 'data-vt-fields');
  if (!vtName || !tbody || typeof addVtRow !== 'function') return;
  var fields = obManagedParseFieldMeta(tbody.getAttribute('data-vt-fields') || '').map(function (f) { return f.name; });
  addVtRow(vtName, fields, tbody);
}

function obManagedSubmitAllowed(form) {
  var msg = form.getAttribute('data-ob-confirm');
  if (msg && !confirm(msg)) return false;
  return obManagedGridSubmitAllowed(form);
}

function obManagedGridSubmitAllowed(form) {
  if (form.hasAttribute('data-ob-grid-sync') && window.obGridSync) {
    // Незавершённая правка ячейки не даёт записать документ: причину показал
    // обработчик onValidationError, пользователю есть что исправить (или Esc).
    if (window.obGridSync() === false) return false;
  }
  return true;
}

function obManagedInitDelegates() {
  document.addEventListener('click', function (e) {
    // data-ob-toggle-next НЕ обрабатываем здесь: managed-форма всегда грузит и
    // ui.js (из шаблона "head"), где этот же делегат уже висит на document.
    // Дублирование переключало бы display дважды (none→block→none) за один клик
    // и dropdown «Печать ▾»/«Ввести на основании» не открывался бы — issue #309.
    var btn = e.target && e.target.closest ? e.target.closest('[data-ob-ref-picker],[data-ob-ref-current],[data-ob-file-trigger],[data-ob-fire-click],[data-ob-grid-add],[data-ob-grid-del],[data-ob-add-tp],[data-ob-add-vt],[data-ob-remove-row],[data-ob-ref-cancel]') : null;
    if (!btn) return;
    if (btn.disabled || btn.getAttribute('aria-disabled') === 'true') return;

    if (btn.hasAttribute('data-ob-ref-cancel')) {
      e.preventDefault();
      try { parent.postMessage({ source: 'obRefCancel' }, '*'); } catch (_) {}
      return;
    }
    if (btn.hasAttribute('data-ob-ref-picker')) {
      e.preventDefault();
      var pickSel = obManagedClosestSelect(btn);
      if (pickSel && typeof openRefPicker === 'function') openRefPicker(pickSel);
      return;
    }
    if (btn.hasAttribute('data-ob-ref-current')) {
      e.preventDefault();
      var curSel = obManagedClosestSelect(btn);
      if (curSel && typeof openRefCurrent === 'function') openRefCurrent(curSel);
      return;
    }
    if (btn.hasAttribute('data-ob-file-trigger')) {
      e.preventDefault();
      var file = document.getElementById(btn.getAttribute('data-ob-file-trigger') || '');
      if (file) file.click();
      return;
    }
    if (btn.hasAttribute('data-ob-fire-click')) {
      e.preventDefault();
      var params = {};
      var tp = btn.getAttribute('data-ob-fire-tp') || '';
      if (tp) params._tp = tp;
      if (window.obFire) window.obFire(btn.getAttribute('data-ob-fire-click') || '', 'Нажатие', params);
      return;
    }
    if (btn.hasAttribute('data-ob-grid-add')) {
      e.preventDefault();
      if (window.obGridAddRow) window.obGridAddRow(btn.getAttribute('data-ob-grid-add') || '');
      return;
    }
    if (btn.hasAttribute('data-ob-grid-del')) {
      e.preventDefault();
      if (window.obGridDelRow) window.obGridDelRow(btn.getAttribute('data-ob-grid-del') || '');
      return;
    }
    if (btn.hasAttribute('data-ob-add-tp')) {
      e.preventDefault();
      obManagedAddTpRow(btn);
      return;
    }
    if (btn.hasAttribute('data-ob-add-vt')) {
      e.preventDefault();
      obManagedAddVtRow(btn);
      return;
    }
    if (btn.hasAttribute('data-ob-remove-row')) {
      e.preventDefault();
      var tr = btn.closest && btn.closest('tr');
      if (tr) {
        var table = tr.closest && tr.closest('table[data-ob-dom-table]');
        var body = tr.parentElement;
        var index = tr.sectionRowIndex;
        tr.remove();
        if (table && body && window.obDOMFinishMutation) {
          var next = body.rows.length ? body.rows[Math.min(Math.max(index, 0), body.rows.length - 1)] : null;
          window.obDOMFinishMutation(table, next, false);
          if (window.obDOMNotifyMutation) window.obDOMNotifyMutation(table, 'delete');
        }
      }
    }
  });

  function obManagedNormalizeHotkey(value) {
    if (typeof window.obNormalizeFormHotkey === 'function') return window.obNormalizeFormHotkey(value);
    return '';
  }

  function obManagedEventHotkey(e) {
    if (!e || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return '';
    return obManagedNormalizeHotkey(e.key || e.code || '');
  }

  document.addEventListener('keydown', function (e) {
    var hotkey = obManagedEventHotkey(e);
    if (!hotkey) return;
    if ((typeof obHasBlockingModal === 'function' && obHasBlockingModal()) ||
        document.getElementById('_ref-picker-modal') || document.getElementById('_item-picker-modal') ||
        document.getElementById('_ref-create-modal')) return;
    var form = document.getElementById('main-form');
    if (!form || !document.contains(form)) return;
    var target = e.target;
    if (target && target !== document.body && !form.contains(target)) return;
    var btn = typeof window.obResolveActionableFormHotkey === 'function'
      ? window.obResolveActionableFormHotkey(hotkey) : null;
    if (!btn) return;
    e.preventDefault();
    btn.click();
  });

  document.addEventListener('change', function (e) {
    var el = e.target;
    if (!el || !el.getAttribute) return;
    if (el.hasAttribute('data-ob-file-pick-path') && window.obFilePick) {
      window.obFilePick(el, el.getAttribute('data-ob-file-pick-path') || '', el.getAttribute('data-ob-file-pick-content') || '');
      return;
    }
    var fire = el.getAttribute('data-ob-fire-change');
    if (fire && window.obFire) window.obFire(fire, 'ПриИзменении');
  });

  document.addEventListener('input', function (e) {
    var el = e.target;
    if (el && el.hasAttribute && el.hasAttribute('data-ob-recalc-tp-row') && typeof recalcTpRow === 'function') recalcTpRow(el);
  });

  document.addEventListener('focusin', function (e) {
    var el = e.target;
    var name = el && el.getAttribute ? el.getAttribute('data-ob-list-choice') : '';
    if (name && window.obStartListChoice) window.obStartListChoice(name);
  });

  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || !form.getAttribute) return;
    // Первый submit с файловым helper уже синхронно остановил capture-handler;
    // повторный requestSubmit проходит сюда после успешного чтения и не должен
    // второй раз показывать confirm или синхронизировать грид.
    if (!obManagedFileSubmitReentry.has(form) && !obManagedSubmitAllowed(form)) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    // Все синхронные veto пройдены: штатная навигация после submit не должна
    // показывать предупреждение о несохранённых данных.
    if (form.id === 'main-form' && !e.defaultPrevented) window._obFormDirty = false;
  });
}

obManagedReady(obManagedInitDelegates);

// SlickGrid initializer for managed-form table parts (plan 48).
// Grids are stored in window._obGrids = {tpName: {grid, dataView, columns}}.
(function(){
  window._obGrids = window._obGrids || {};
  window._obGridViews = window._obGridViews || [];

  // resizeGrid — пересчитывает геометрию грида и растягивает колонки на всю
  // ширину контейнера. Критично для ТЧ на вкладках/в свёрнутых группах: при
  // инициализации в скрытом (display:none) контейнере SlickGrid меряет ширину
  // 0 и autosizeColumns схлопывает колонки в узкую полоску. Поэтому ресайзим
  // только видимый грид (offsetParent != null) — и повторяем при показе вкладки.
  function resizeGrid(g) {
    if (!g || !g.div || g.div.offsetParent === null) return;
    g.grid.resizeCanvas();
    g.grid.autosizeColumns();
    updateTotals(g); // footer-ячейки пересоздаются при re-render — обновляем суммы
  }

  // updateTotals — строка итогов (footer row SlickGrid). Для каждой числовой
  // колонки считает сумму по всем строкам модели и выводит в footer-ячейку,
  // выровненную по колонке. В первой колонке — подпись «Итого».
  function updateTotals(g) {
    // Полностью defensive: итоги — вторичны и НИКОГДА не должны ломать
    // перерисовку грида (этот код вызывается из подписчиков onRowCountChanged).
    try {
      if (!g || !g.grid || typeof g.grid.getOptions !== "function" || !g.grid.getOptions().showFooterRow) return;
      if (typeof g.grid.getFooterRowColumn !== "function") return;
      var items = g.dataView.getItems();
      var cols = g.columnsMeta || [];
      for (var i = 0; i < cols.length; i++) {
        var c = cols[i];
        var node = g.grid.getFooterRowColumn(c.id);
        if (!node) continue;
        if (c.type === "number") {
          var sum = 0;
          for (var r = 0; r < items.length; r++) {
            var n = Number(String(items[r][c.id] == null ? "" : items[r][c.id]).replace(",", "."));
            if (!isNaN(n)) sum += n;
          }
          node.innerHTML = '<span style="display:block;text-align:right;font-weight:600;font-variant-numeric:tabular-nums">' +
            sum.toLocaleString("ru-RU", {minimumFractionDigits: 0, maximumFractionDigits: 2}) + "</span>";
        } else {
          node.innerHTML = (i === 0) ? '<span style="color:#64748b">Итого</span>' : "";
        }
      }
    } catch (e) { if (window.console) console.warn("updateTotals:", e); }
  }
  // _obResizeGrids — пройтись по всем гридам и пересчитать видимые. Вызывается
  // при переключении вкладок managed-формы и при ресайзе окна.
  window._obResizeGrids = function() {
    var views = window._obGridViews || [];
    if (views.length) {
      for (var i = 0; i < views.length; i++) resizeGrid(views[i]);
      return;
    }
    // Compatibility for callers/tests which seed only the historical map.
    var grids = window._obGrids || {};
    for (var tp in grids) resizeGrid(grids[tp]);
  };

  // Serialize ref value: extract id from {id,_label} object or return raw value
  function refId(v) {
    if (v && typeof v === "object") {
      if (v.id !== undefined) return v.id;
      if (v.UUID !== undefined) return v.UUID; // сериализованный *interpreter.Ref
    }
    return (v == null) ? "" : String(v);
  }

  // Редактор ячейки-ссылки: ввод по строке + форма выбора (план 48, фаза 4).
  //
  // Клавиатура — как в 1С: печатаем часть наименования → список фильтруется,
  // ↑/↓ двигают подсветку, Enter подставляет подсвеченный вариант, Tab
  // подставляет и уходит в следующую колонку, F4 / Alt+↓ открывают форму
  // выбора, Esc закрывает список (правку ячейки не отменяет).
  //
  // ВАЖНО, почему это не «улучшение», а починка: выбор пункта раньше висел
  // ТОЛЬКО на mousedown, а isValueChanged() смотрел лишь на selectedId.
  // Набранный текст selectedId не менял → SlickGrid считал ячейку неизменённой
  // и не звал ни validate(), ни serializeValue() (см. commitCurrentEdit в
  // slick.grid.js), то есть введённое молча пропадало. Набрать документ с
  // клавиатуры было невозможно в принципе.
  //
  // Список = предзагруженные опции (сервер кладёт в data-sg-ref первые 50)
  // ПЛЮС результаты серверного поиска /ui/_ref-options/<entity>?q= — без него
  // позицию за пределами предзагруженных в ячейке было не найти вообще.
  function ObRefEditor(refField, refOptsList, args) {
    var wrapper, input, dropBtn, list;
    var isOpen = false, selectedId = '', defaultValue = '';
    var refEntity = (args.column && args.column.refEntity) || '';
    var serverRows = [];   // последний ответ серверного поиска
    var shown = [];        // что сейчас отрисовано в списке (для ↑/↓ и Enter)
    var activeIdx = -1;    // подсвеченный пункт списка, -1 — нет подсветки
    var searchTimer = null, searchSeq = 0;

    function label(id) {
      for (var k = 0; k < refOptsList.length; k++) {
        if (String(refOptsList[k].id) === String(id)) return refOptsList[k]._label;
      }
      return '';
    }

    // remember — кладёт найденную сервером позицию в refOptsList. Тот же массив
    // держит в замыкании ФОРМАТТЕР колонки, поэтому без этого выбранный «не из
    // первых 50» элемент рисовался бы в ячейке голым UUID.
    function remember(opt) {
      if (!opt || opt.id === undefined || opt.id === null || opt.id === '') return;
      for (var k = 0; k < refOptsList.length; k++) {
        if (String(refOptsList[k].id) === String(opt.id)) return;
      }
      refOptsList.push({id: opt.id, _label: opt._label});
    }

    // candidates — предзагруженные опции + серверные результаты, отфильтрованные
    // подстрокой (регистронезависимо), без дублей по id.
    function candidates(filter) {
      var f = String(filter == null ? '' : filter).trim().toLowerCase();
      var out = [], seen = {};
      function push(o) {
        if (!o || o.id === undefined || o.id === null || o.id === '') return;
        var key = String(o.id);
        if (seen[key]) return;
        var lbl = String(o._label == null ? '' : o._label);
        if (f && lbl.toLowerCase().indexOf(f) < 0) return;
        seen[key] = true;
        out.push({id: o.id, _label: lbl});
      }
      for (var i = 0; i < refOptsList.length; i++) push(refOptsList[i]);
      for (var j = 0; j < serverRows.length; j++) push(serverRows[j]);
      return out;
    }

    function paintActive() {
      var nodes = list.querySelectorAll('[data-idx]');
      for (var k = 0; k < nodes.length; k++) {
        nodes[k].style.background = (k === activeIdx) ? '#eef2ff' : '';
      }
      if (activeIdx >= 0 && nodes[activeIdx] && nodes[activeIdx].scrollIntoView) {
        nodes[activeIdx].scrollIntoView({block: 'nearest'});
      }
    }

    function renderList() {
      list.innerHTML = '';
      if (!shown.length) {
        var empty = document.createElement('div');
        empty.textContent = 'Ничего не найдено (F4 — форма выбора)';
        empty.style.cssText = 'padding:8px 10px;color:#94a3b8;font-style:italic';
        list.appendChild(empty);
        return;
      }
      for (var k = 0; k < shown.length; k++) {
        var item = document.createElement('div');
        item.textContent = shown[k]._label;
        item.setAttribute('data-id', shown[k].id);
        item.setAttribute('data-idx', k);
        item.style.cssText = 'padding:6px 10px;cursor:pointer;border-bottom:1px solid #f1f5f9';
        (function(o, idx) {
          item.addEventListener('mouseenter', function() { activeIdx = idx; paintActive(); });
          // mousedown, а не click: blur поля ввода закрыл бы список раньше click.
          item.addEventListener('mousedown', function(e) {
            e.preventDefault();
            pick(o);
            args.commitChanges();
          });
        })(shown[k], k);
        list.appendChild(item);
      }
      paintActive();
    }

    // keepId — сохранить подсветку на конкретной позиции. Нужен, когда список
    // перестраивается не по действию пользователя: ответ серверного поиска
    // приходит асинхронно и иначе сбрасывал бы на первую строку выбор, только
    // что сделанный стрелками.
    function buildList(filter, keepId) {
      shown = candidates(filter);
      activeIdx = -1;
      if (keepId) {
        for (var k = 0; k < shown.length; k++) {
          if (String(shown[k].id) === String(keepId)) { activeIdx = k; break; }
        }
      }
      // Когда что-то набрано — подсвечиваем первый вариант: Enter сразу его
      // подставит (ввод по строке). При пустом фильтре подсветки нет, и Enter
      // уходит в SlickGrid — просто коммитит ячейку.
      if (activeIdx < 0) activeIdx = (String(filter == null ? '' : filter).trim() !== '' && shown.length) ? 0 : -1;
      renderList();
    }

    function positionList() {
      var rect = input.getBoundingClientRect();
      // position:fixed — getBoundingClientRect отсчитывается от окна; при
      // absolute список уезжал мимо ячейки на прокрученной форме.
      list.style.left = rect.left + 'px';
      list.style.top = rect.bottom + 'px';
      list.style.width = Math.max(rect.width, 220) + 'px';
    }

    function openList() {
      if (isOpen) return;
      isOpen = true;
      buildList(input.value);
      positionList();
      document.body.appendChild(list);
      // Глобальному обработчику Esc (он в фазе перехвата и иначе отменил бы всю
      // правку ячейки) нужно знать, что открыт именно наш выпадающий список.
      window._obRefDropdown = {close: closeList};
    }

    function closeList() {
      if (!isOpen) return;
      isOpen = false;
      activeIdx = -1;
      if (list && list.parentElement) list.remove();
      if (window._obRefDropdown && window._obRefDropdown.close === closeList) window._obRefDropdown = null;
    }

    // Серверный поиск. Эндпойнт уважает права на чтение, RLS и маскирование
    // полей, поэтому в ячейке видно ровно то же, что в форме выбора.
    function searchServer(q) {
      if (!refEntity || !window.fetch) return;
      var seq = ++searchSeq;
      var url = '/ui/_ref-options/' + encodeURIComponent(refEntity) +
                '?limit=50&q=' + encodeURIComponent(q || '');
      fetch(url, {credentials: 'same-origin', headers: {'Accept': 'application/json'}})
        .then(function(resp) { if (!resp.ok) throw new Error('HTTP ' + resp.status); return resp.json(); })
        .then(function(data) {
          if (seq !== searchSeq) return; // ответ устарел (или редактор уже закрыт)
          var keep = (activeIdx >= 0 && shown[activeIdx]) ? shown[activeIdx].id : '';
          serverRows = ((data && data.items) || []).map(function(row) {
            return {id: row && row.id != null ? String(row.id) : '', _label: String((row && row._label) || '')};
          }).filter(function(o) { return o.id !== ''; });
          if (isOpen) buildList(input.value, keep);
        })
        .catch(function() { /* сеть/ошибка — остаются предзагруженные опции */ });
    }

    function scheduleSearch(q) {
      if (searchTimer) clearTimeout(searchTimer);
      searchTimer = setTimeout(function() { searchServer(q); }, 180);
    }

    function pick(opt) {
      selectedId = opt.id;
      input.value = opt._label;
      remember(opt);
      closeList();
    }

    // resolveTyped — «ввод по строке»: превращает набранный текст в ссылку.
    // Возвращает true, если значение определено (в том числе очищено).
    function resolveTyped() {
      var text = String(input.value == null ? '' : input.value).trim();
      if (text === '') { selectedId = ''; return true; }
      if (text === String(label(selectedId) || '').trim()) return true; // поверх не набирали
      var low = text.toLowerCase();
      var found = candidates(text);
      for (var k = 0; k < found.length; k++) { // точное совпадение подписи важнее частичного
        if (String(found[k]._label).trim().toLowerCase() === low) { pick(found[k]); return true; }
      }
      if (found.length === 1) { pick(found[0]); return true; }
      if (activeIdx >= 0 && shown[activeIdx]) { pick(shown[activeIdx]); return true; } // Tab с открытым списком
      return false;
    }

    // openPicker — общая форма выбора (ui.js). Ей нужен <select> как носитель
    // значения: отдаём временный и слушаем его change, который шлёт selectItem.
    function openPicker() {
      if (typeof window.openRefPicker !== 'function') return;
      var selEl = document.createElement('select');
      selEl.setAttribute('data-ref-entity', refEntity);
      var opts = candidates('');
      for (var k = 0; k < opts.length; k++) {
        var o = document.createElement('option');
        o.value = opts[k].id;
        o.textContent = opts[k]._label;
        selEl.appendChild(o);
      }
      selEl.value = selectedId;
      selEl.addEventListener('change', function() {
        var id = selEl.value;
        if (!id) return;
        var chosen = selEl.options[selEl.selectedIndex];
        pick({id: id, _label: chosen ? chosen.textContent : ''});
        args.commitChanges();
      });
      closeList();
      window.openRefPicker(selEl);
    }

    function onKeyDown(e) {
      var key = e.key || '';
      if (key === 'ArrowDown' && e.altKey) { e.preventDefault(); e.stopPropagation(); openPicker(); return; }
      if (key === 'ArrowDown' || key === 'ArrowUp') {
        // stopPropagation обязателен: иначе SlickGrid уведёт курсор на соседнюю
        // строку прямо из открытого списка.
        e.preventDefault();
        e.stopPropagation();
        if (!isOpen) { openList(); if (activeIdx < 0 && shown.length) { activeIdx = 0; paintActive(); } return; }
        if (!shown.length) return;
        var next = activeIdx + (key === 'ArrowDown' ? 1 : -1);
        if (next < 0) next = shown.length - 1;
        if (next >= shown.length) next = 0;
        activeIdx = next;
        paintActive();
        return;
      }
      if (key === 'Enter') {
        if (isOpen && activeIdx >= 0 && shown[activeIdx]) {
          // Подставляем значение и ОСТАЁМСЯ в ячейке (ввод по строке в 1С);
          // следующий Enter/Tab уже коммитит правку средствами SlickGrid.
          e.preventDefault();
          e.stopPropagation();
          pick(shown[activeIdx]);
          input.select();
        }
        return; // список закрыт — Enter уходит в грид и коммитит ячейку
      }
      if (key === 'Escape' || e.keyCode === 27) {
        if (isOpen) { e.preventDefault(); e.stopPropagation(); closeList(); }
        return; // список закрыт — Esc отменяет правку ячейки (как раньше)
      }
      if (key === 'F4') { e.preventDefault(); e.stopPropagation(); openPicker(); }
    }

    this.init = function() {
      wrapper = document.createElement('div');
      wrapper.style.cssText = 'display:flex;align-items:center;width:100%;height:100%;gap:2px';

      input = document.createElement('input');
      input.type = 'text';
      input.autocomplete = 'off';
      input.style.cssText = 'flex:1;border:none;outline:none;padding:2px 4px;font-size:13px;min-width:0';
      var cur = args.item[args.column.field];
      defaultValue = cur;
      selectedId = refId(cur);
      input.value = label(selectedId) || String(selectedId);

      dropBtn = document.createElement('button');
      dropBtn.type = 'button';
      dropBtn.textContent = '…';
      dropBtn.title = 'Выбрать из списка (F4)';
      dropBtn.style.cssText = 'border:none;background:none;cursor:pointer;font-size:12px;padding:0 4px;color:#2563eb;flex-shrink:0';

      wrapper.appendChild(input);
      wrapper.appendChild(dropBtn);
      args.container.appendChild(wrapper);

      list = document.createElement('div');
      list.setAttribute('data-ob-ref-list', refField);
      list.style.cssText = 'position:fixed;z-index:9999;background:#fff;border:1px solid #e2e8f0;border-radius:6px;box-shadow:0 4px 12px rgba(0,0,0,.12);max-height:200px;overflow-y:auto;min-width:160px;font-size:13px';

      input.addEventListener('keydown', onKeyDown);
      input.addEventListener('focus', openList);
      input.addEventListener('input', function() {
        if (isOpen) buildList(input.value); else openList();
        scheduleSearch(input.value);
      });
      input.addEventListener('blur', function() { setTimeout(closeList, 150); });

      dropBtn.addEventListener('mousedown', function(e) { e.preventDefault(); }); // не терять фокус до click
      dropBtn.addEventListener('click', function(e) {
        e.preventDefault();
        e.stopPropagation();
        openPicker();
      });

      input.focus();
      input.select();
    };

    this.destroy = function() {
      // Гасим таймер и ответы «в полёте»: иначе поздний ответ поиска дорисовал бы
      // список уже уничтоженного редактора.
      if (searchTimer) { clearTimeout(searchTimer); searchTimer = null; }
      searchSeq++;
      closeList();
      isOpen = false;
      if (list && list.parentElement) list.remove();
      if (wrapper && wrapper.parentElement) wrapper.remove();
    };
    this.focus = function() { if (input) input.focus(); };
    this.getValue = function() { return selectedId; };
    this.setValue = function(val) { selectedId = refId(val); input.value = label(selectedId); };
    this.loadValue = function(item) {
      var v = item[args.column.field];
      defaultValue = v;
      selectedId = refId(v);
      input.value = label(selectedId);
    };
    this.serializeValue = function() { resolveTyped(); return selectedId; };
    this.applyValue = function(item, state) { item[args.column.field] = state; };
    // isValueChanged должен быть true и когда изменился ТОЛЬКО набранный текст:
    // при false SlickGrid не зовёт validate()/serializeValue() и правка теряется.
    this.isValueChanged = function() {
      var text = String(input.value == null ? '' : input.value).trim();
      if (text !== String(label(selectedId) || '').trim()) return true;
      return String(selectedId) !== String(refId(defaultValue));
    };
    this.validate = function() {
      if (resolveTyped()) return {valid: true, msg: null};
      return {valid: false, msg: 'Не найдено: «' + String(input.value).trim() + '». ↑/↓ и Enter — выбрать, F4 — форма выбора'};
    };
    this.init();
  }

  // Custom number editor with locale-aware parsing (plan 48, phase 3).
  function ObNumberEditor(args) {
    var input, defaultValue;
    this.init = function() {
      input = document.createElement('input');
      input.type = 'text';
      input.className = 'editor-text';
      input.style.cssText = 'width:100%;height:100%;border:none;outline:none;padding:2px 4px;text-align:right;font-variant-numeric:tabular-nums;font-size:13px';
      args.container.appendChild(input);
      input.focus();
      var val = args.item[args.column.field];
      defaultValue = val;
      if (val != null && val !== '') input.value = String(val).replace('.', ',');
      input.select();
    };
    this.destroy = function() { input.remove(); };
    this.focus = function() { input.focus(); };
    this.getValue = function() { return input.value; };
    this.setValue = function(val) { input.value = val; };
    this.loadValue = function(item) {
      var v = item[args.column.field];
      defaultValue = v;
      input.value = (v != null && v !== '') ? String(v).replace('.', ',') : '';
    };
    this.serializeValue = function() {
      var v = input.value.replace(/\s/g, '').replace(',', '.');
      return v === '' ? '' : v;
    };
    this.applyValue = function(item, state) { item[args.column.field] = state; };
    this.isValueChanged = function() {
      return input.value !== ((defaultValue != null) ? String(defaultValue).replace('.', ',') : '');
    };
    this.validate = function() {
      var v = input.value.replace(/\s/g, '').replace(',', '.');
      if (v !== '' && isNaN(Number(v))) return {valid: false, msg: 'Введите число'};
      return {valid: true, msg: null};
    };
    this.init();
  }

  // Build SlickGrid columns from metadata with editors (plan 48, phase 3).
  function buildColumns(colsMeta, refOpts, enumLabels) {
    var columns = [];
    for (var i = 0; i < colsMeta.length; i++) {
      var c = colsMeta[i];
      var col = {id: c.id, name: c.name, field: c.id, width: 120, resizable: true, sortable: true};
      if (c.type === "number") {
        col.cssClass = "ob-num";
        col.editor = ObNumberEditor;
        // Подсветка значений: отрицательные — красным (недостачи, возвраты,
        // отклонения); колонка «дефицит» при положительном значении — оранжевым.
        var warnPos = /дефицит/i.test(c.id || "");
        col.formatter = (function(warn){ return function(row, cell, value) {
          if (value == null || value === "") return "";
          var n = Number(String(value).replace(',', '.'));
          if (isNaN(n)) return "<span>" + value + "</span>";
          var s = n.toLocaleString("ru-RU", {minimumFractionDigits:0, maximumFractionDigits:2});
          if (n < 0) return "<span style='color:#dc2626;font-weight:600'>" + s + "</span>";
          if (warn && n > 0) return "<span style='color:#ea580c;font-weight:600'>" + s + "</span>";
          return "<span>" + s + "</span>";
        }; })(warnPos);
      } else if (c.ref) {
        col.cssClass = "ob-ref";
        // refEntity нужен редактору для серверного поиска и форме выбора: без
        // него в ячейке были видны только предзагруженные опции, а модалка
        // подбора уходила в локальный фильтр вместо /ui/_ref-options.
        col.refEntity = c.ref;
        col.editor = (function(refField, refOptsList) {
          return ObRefEditor.bind(null, refField, refOptsList);
        })(c.id, refOpts[c.id] || []);
        col.formatter = (function(refField) {
          return function(row, cell, value, colDef, dataCtx) {
            if (!value) return "";
            // Ссылка может прийти объектом: {id,_label} (клиентский формат) или
            // {UUID,Name} (сериализованный *interpreter.Ref, если просочился мимо
            // serializeValue). Извлекаем подпись/идентификатор — иначе String(obj)
            // дал бы «[object Object]».
            if (typeof value === "object") {
              if (value._label) return "<span>" + value._label + "</span>";
              if (value.Name)   return "<span>" + value.Name + "</span>";
              value = (value.id !== undefined) ? value.id : (value.UUID !== undefined ? value.UUID : "");
            }
            var opts = (refOpts && refOpts[refField]) || [];
            for (var k = 0; k < opts.length; k++) {
              if (String(opts[k].id) === String(value)) return "<span>" + opts[k]._label + "</span>";
            }
            return "<span>" + String(value) + "</span>";
          };
        })(c.id);
      } else if (c.enum) {
        col.cssClass = "ob-enum";
        col.editor = Slick.Editors.Text;
        col.formatter = (function(enumField) {
          return function(row, cell, value) {
            if (value == null || value === "") return "";
            var labels = (enumLabels && enumLabels[enumField]) || {};
            var lbl = labels[value];
            return "<span>" + (lbl != null ? lbl : String(value)) + "</span>";
          };
        })(c.id);
      } else if (c.type === "bool") {
        col.cssClass = "ob-bool";
        col.editor = Slick.Editors.Checkbox;
        col.formatter = function(row, cell, value) {
          var on = (value === true || value === "true" || value === 1 || value === "1");
          return on ? '<span style="color:#16a34a;font-weight:700">✓</span>'
                    : '<span style="color:#cbd5e1">—</span>';
        };
      } else {
        col.editor = Slick.Editors.Text;
      }
      columns.push(col);
    }
    return columns;
  }

  // Serialize ref value
  function refId(v) {
    if (v && typeof v === "object") {
      if (v.id !== undefined) return v.id;
      if (v.UUID !== undefined) return v.UUID; // сериализованный *interpreter.Ref
    }
    return (v == null) ? "" : String(v);
  }

  function copyFormGridStyleKeys(source, item) {
    item._obRowClass = source && source._form_row_class ? String(source._form_row_class) : "";
    item._obCellClasses = source && source._form_cell_classes ? source._form_cell_classes : {};
    return item;
  }

  function formGridItemMetadata(row) {
    var item = this.getItem(row);
    if (!item) return null;
    var meta = null;
    if (item._obRowClass) {
      meta = meta || {};
      meta.cssClasses = item._obRowClass;
    }
    var cc = item._obCellClasses || {};
    var columns = {};
    Object.keys(cc).forEach(function(field) {
      if (cc[field]) columns[field] = {cssClass: String(cc[field])};
    });
    if (Object.keys(columns).length) {
      meta = meta || {};
      meta.columns = columns;
    }
    return meta;
  }

  // Serialize all grid data into hidden inputs (for form submit / obFire)
  // obGridSync — переносит строки гридов в скрытые tp_json перед отправкой формы.
  // Возвращает false, если незаконченную правку ячейки не удалось закоммитить
  // (например, в колонке-ссылке набрано ненайденное) — записывать в таком виде
  // нельзя: на форме пользователь видит одно, в tp_json ушло бы другое.
  window.obGridSync = function() {
    var grids = window._obGrids || {};
    var ok = true;
    for (var tpName in grids) {
      var g = grids[tpName];
      // Открытый редактор ячейки: без коммита значение, набранное прямо перед
      // «Записать», в dataView не попадает и молча теряется при сохранении.
      // Тот же приём применяется перед отправкой события формы (см. obFire).
      try {
        var lock = g.grid && g.grid.getEditorLock && g.grid.getEditorLock();
        if (lock && lock.isActive() && !lock.commitCurrentEdit()) ok = false;
      } catch (e) {
        // Исключение editor-lock не означает успех: dataView может содержать
        // старое значение. Останавливаем submit/obFire вместо тихой потери.
        ok = false;
        if (window.obFlash) window.obFlash('Не удалось завершить правку ячейки: ' + (e && e.message ? e.message : e), 'err');
      }
      // Сериализуем в исходном порядке (_ord), а не в порядке текущей
      // сортировки отображения — чтобы сортировка «для просмотра» не меняла
      // порядок строк в сохраняемом документе.
      var items = g.dataView.getItems().slice().sort(function(a, b) {
        return (a._ord || 0) - (b._ord || 0);
      });
      var rows = items.map(function(item) {
        var row = {};
        var cols = g.columnsMeta || [];
        for (var i = 0; i < cols.length; i++) {
          row[cols[i].id] = refId(item[cols[i].id]);
        }
        return row;
      });
      window.obManagedSetTablePartJSON(tpName, rows);
    }
    return ok;
  };

  function gridCellEventParams(tpName, args, columns, dataView) {
    var rowIndex = -1;
    if (args && args.item && dataView && typeof dataView.getItems === "function") {
      var items = dataView.getItems().slice().sort(function(a, b) {
        return (a._ord || 0) - (b._ord || 0);
      });
      for (var i = 0; i < items.length; i++) {
        if (items[i] && items[i].id === args.item.id) { rowIndex = i; break; }
      }
    }
    if (rowIndex < 0 && args && typeof args.row === "number") rowIndex = args.row;
    var cellIndex = (args && typeof args.cell === "number") ? args.cell : -1;
    var colName = "";
    if (cellIndex >= 0 && columns && columns[cellIndex]) colName = columns[cellIndex].field || columns[cellIndex].id || "";
    return {
      _tp: tpName,
      _tp_row: rowIndex >= 0 ? String(rowIndex) : "",
      _tp_row_number: rowIndex >= 0 ? String(rowIndex + 1) : "",
      _tp_col: colName,
      _tp_col_index: cellIndex >= 0 ? String(cellIndex) : ""
    };
  }

  // Add empty row to grid
  // obFireRowEvent — серверное событие строки ТЧ (ПриДобавленииСтроки/
  // ПриУдаленииСтроки). Дёргается после добавления/удаления строки, но только
  // если у элемента ТЧ объявлен обработчик (флаг data-sg-rowadd/data-sg-rowdel),
  // — иначе впустую гоняли бы сеть. Путь тот же, что у ПриИзменении: obFire
  // синхронизирует ТЧ (obGridSync) и применяет values/tableparts из ответа.
  window.obFireRowEvent = function(tpName, attr, eventName) {
    // A form may show the same TP in a readonly summary and an editable grid.
    // Resolve the exact canonical writable host instead of the first duplicate
    // id in DOM order.
    var state = (window._obGrids || {})[tpName];
    var div = state && !state.readOnly ? state.div : null;
    if (!div || div.getAttribute(attr) !== "1") return;
    var elName = div.getAttribute("data-sg-el") || tpName;
    if (window.obFire) window.obFire(elName, eventName, {_tp: tpName});
  };

  window.obGridAddRow = function(tpName) {
    var g = (window._obGrids || {})[tpName];
    if (!g || g.readOnly || (g.div && g.div.getAttribute("data-sg-ro") === "1") || !commitGridEdit(g)) return;
    rememberActiveGrid(tpName);
    var nextId = 0, nextOrd = 0;
    g.dataView.getItems().forEach(function(it) {
      if (it.id >= nextId) nextId = it.id + 1;
      if ((it._ord || 0) >= nextOrd) nextOrd = (it._ord || 0) + 1;
    });
    var item = {id: nextId, _ord: nextOrd};
    var cols = g.columnsMeta || [];
    for (var i = 0; i < cols.length; i++) item[cols[i].id] = "";
    g.dataView.addItem(item);
    window._obFormDirty = true;
    g.grid.invalidate();
    // scrollRowIntoView ждёт ИНДЕКС отображаемой строки, не id записи —
    // после удалений они расходятся. Берём индекс из getRowById.
    var rowIdx = g.dataView.getRowById(nextId);
    if (rowIdx !== undefined && g.columns.length > 0) {
      g.grid.scrollRowIntoView(rowIdx);
      g.grid.setActiveCell(rowIdx, 0);
      g.grid.editActiveCell();
    }
    obFireRowEvent(tpName, "data-sg-rowadd", "ПриДобавленииСтроки");
  };

  function gridRowsForDelete(g) {
    var rows = [];
    var selectionModel = null;
    try {
      selectionModel = g.grid.getSelectionModel && g.grid.getSelectionModel();
      if (selectionModel && g.grid.getSelectedRows) rows = g.grid.getSelectedRows() || [];
    } catch (e) { rows = []; }
    if (!rows.length) {
      var ac = g.grid.getActiveCell();
      if (ac) rows = [ac.row];
    }
    var unique = [];
    var seen = {};
    for (var i = 0; i < rows.length; i++) {
      var row = Number(rows[i]);
      if (!Number.isInteger(row) || row < 0 || seen[row]) continue;
      seen[row] = true;
      unique.push(row);
    }
    return unique;
  }

  // В поставке нет RowSelectionModel: обычный SlickGrid честно удаляет только
  // активную строку. Если приложение позднее установит selection model,
  // используем все реально выбранные строки и сохраняем multi-select семантику.
  window.obGridDelRow = function(tpName) {
    var g = (window._obGrids || {})[tpName];
    if (!g || g.readOnly || (g.div && g.div.getAttribute("data-sg-ro") === "1") || !commitGridEdit(g)) return false;
    rememberActiveGrid(tpName);
    var rows = gridRowsForDelete(g);
    if (!rows.length) return false;
    var toRemove = [];
    var ids = Object.create(null);
    for (var i = 0; i < rows.length; i++) {
      var item = g.dataView.getItem(rows[i]);
      if (item && !ids[item.id]) {
        ids[item.id] = true;
        toRemove.push(item);
      }
    }
    if (!toRemove.length) return false;
    for (var j = 0; j < toRemove.length; j++) g.dataView.deleteItem(toRemove[j].id);
    window._obFormDirty = true;
    try { g.grid.invalidate(); } catch (e) {
      if (window.console) window.console.error("SlickGrid delete refresh error [" + tpName + "]:", e);
    }
    try {
      if (g.grid.getSelectionModel && g.grid.getSelectionModel() && g.grid.setSelectedRows) g.grid.setSelectedRows([]);
    } catch (e) {}
    try { obFireRowEvent(tpName, "data-sg-rowdel", "ПриУдаленииСтроки"); } catch (e) {
      if (window.console) window.console.error("SlickGrid row-delete event error [" + tpName + "]:", e);
    }
    return true;
  };

  // reindexOrd — переписывает _ord подряд (0..n-1) по текущему порядку массива.
  // Порядок строк документа значим и хранится именно в _ord (см. obGridSync),
  // поэтому после вставки/перемещения его надо нормализовать.
  function reindexOrd(items) {
    for (var i = 0; i < items.length; i++) items[i]._ord = i;
    return items;
  }

  function byOrd(g) {
    return g.dataView.getItems().slice().sort(function(a, b) { return (a._ord || 0) - (b._ord || 0); });
  }

  // commitGridEdit — закрыть открытый редактор перед структурной операцией.
  // false — правку не приняли (например, в ячейке-ссылке набрано ненайденное);
  // добавлять/двигать строки в этот момент нельзя.
  function commitGridEdit(g) {
    var lock = g.grid && g.grid.getEditorLock && g.grid.getEditorLock();
    if (lock && lock.isActive()) return lock.commitCurrentEdit();
    return true;
  }

  // obGridCopyRow — копия текущей строки сразу под ней (F9, как в 1С).
  window.obGridCopyRow = function(tpName) {
    var g = (window._obGrids || {})[tpName];
    if (!g || g.readOnly || !commitGridEdit(g)) return;
    rememberActiveGrid(tpName);
    var ac = g.grid.getActiveCell();
    if (!ac) return;
    var src = g.dataView.getItem(ac.row);
    if (!src) return;
    var nextId = 0;
    g.dataView.getItems().forEach(function(it) { if (it.id >= nextId) nextId = it.id + 1; });
    var copy = {id: nextId, _ord: (src._ord || 0) + 0.5};
    var cols = g.columnsMeta || [];
    for (var i = 0; i < cols.length; i++) copy[cols[i].id] = src[cols[i].id];
    copy._obRowClass = src._obRowClass || "";
    copy._obCellClasses = Object.assign({}, src._obCellClasses || {});
    g.dataView.addItem(copy);
    g.dataView.setItems(reindexOrd(byOrd(g)));
    window._obFormDirty = true;
    g.grid.invalidate();
    var rowIdx = g.dataView.getRowById(nextId);
    if (rowIdx !== undefined) {
      g.grid.scrollRowIntoView(rowIdx);
      g.grid.setActiveCell(rowIdx, 0);
      g.grid.editActiveCell();
    }
    updateTotals(g);
    obFireRowEvent(tpName, "data-sg-rowadd", "ПриДобавленииСтроки");
  };

  // obGridMoveRow — переместить текущую строку на delta позиций (Ctrl+↑/↓).
  // Работаем в порядке _ord, а не отображения: под клиентской сортировкой
  // «переместить вверх» иначе давало бы непредсказуемый результат. После
  // перемещения список показывается в порядке документа.
  window.obGridMoveRow = function(tpName, delta) {
    var g = (window._obGrids || {})[tpName];
    if (!g || g.readOnly || !commitGridEdit(g)) return;
    rememberActiveGrid(tpName);
    var ac = g.grid.getActiveCell();
    if (!ac) return;
    var cur = g.dataView.getItem(ac.row);
    if (!cur) return;
    var items = byOrd(g);
    var pos = -1;
    for (var i = 0; i < items.length; i++) { if (items[i].id === cur.id) { pos = i; break; } }
    var to = pos + delta;
    if (pos < 0 || to < 0 || to >= items.length) return;
    items.splice(to, 0, items.splice(pos, 1)[0]);
    g.dataView.setItems(reindexOrd(items));
    window._obFormDirty = true;
    g.grid.invalidate();
    var rowIdx = g.dataView.getRowById(cur.id);
    if (rowIdx !== undefined) {
      g.grid.scrollRowIntoView(rowIdx);
      g.grid.setActiveCell(rowIdx, ac.cell);
    }
    updateTotals(g);
  };

  function gridNameFromTarget(el) {
    var host = el && el.closest ? el.closest(".ob-grid[data-sg-tp]") : null;
    return host ? (host.getAttribute("data-sg-tp") || "") : "";
  }

  function rememberActiveGrid(tpName) {
    if (tpName && (window._obGrids || {})[tpName]) {
      window._obActiveGridName = tpName;
      window._obActiveDOMTable = null;
    }
  }

  function managedElementVisible(el) {
    if (!el) return false;
    if (typeof window.obElementVisible === "function") return window.obElementVisible(el);
    for (var cur = el; cur && cur.nodeType === 1; cur = cur.parentElement) {
      var inlineStyle = cur.style || {};
      if (cur.hidden || (cur.getAttribute && cur.getAttribute("aria-hidden") === "true") || inlineStyle.display === "none" || inlineStyle.visibility === "hidden") return false;
      if (window.getComputedStyle) {
        var style = window.getComputedStyle(cur);
        if (style && (style.display === "none" || style.visibility === "hidden")) return false;
      }
    }
    return true;
  }

  // activeGridName — грид, к которому относится нажатие. Фокус внутри грида
  // имеет приоритет; после ухода на служебный focus-sink используем ПОСЛЕДНИЙ
  // грид, с которым реально взаимодействовал пользователь. Перебирать первый
  // грид с activeCell нельзя: на форме их может быть несколько, и activeCell
  // сохраняется у каждого.
  function activeGridName(target) {
    var grids = window._obGrids || {};
    var source = target || document.activeElement;
    var directHost = source && source.closest ? source.closest(".ob-grid[data-sg-tp]") : null;
    var direct = directHost ? (directHost.getAttribute("data-sg-tp") || "") : "";
    // Прямой контекст всегда окончательный. Неизвестный/ещё не
    // зарегистрированный host не имеет права откатываться к старому гриду.
    if (directHost) {
      window._obActiveDOMTable = null;
      if (!direct || !grids[direct] || grids[direct].div !== directHost ||
          !document.contains(directHost) || !managedElementVisible(directHost)) {
        if (direct && window._obActiveGridName === direct) window._obActiveGridName = "";
        return "";
      }
      rememberActiveGrid(direct);
      return direct;
    }
    // A concrete no-grid table target is authoritative too: never fall back
    // to a SlickGrid that happened to be active before focus moved here.
    if (source && source.closest && source.closest('table[data-ob-dom-table]')) {
      window._obActiveGridName = "";
      return "";
    }
    var remembered = window._obActiveGridName || "";
    var g = remembered && grids[remembered];
    if (g && g.div && document.contains(g.div) && managedElementVisible(g.div)) return remembered;
    if (remembered) window._obActiveGridName = "";
    return "";
  }

  function gridInteractiveTarget(el) {
    if (!el || !el.closest) return false;
    return !!el.closest('a[href],button,input,textarea,select,option,summary,[contenteditable]:not([contenteditable="false"]),[role="button"],[role="link"],[role="menuitem"]');
  }

  function managedHasBlockingModal() {
    if (typeof obHasBlockingModal === 'function') return obHasBlockingModal();
    return !!(document.getElementById('_ref-picker-modal') ||
      document.getElementById('_item-picker-modal') ||
      document.getElementById('_ref-create-modal'));
  }

  function hasActionableFormHotkey(key) {
    return typeof window.obResolveActionableFormHotkey === 'function' &&
      !!window.obResolveActionableFormHotkey(key);
  }

  // Клавиши работы со строками — как в 1С. Delete живёт на самом гриде
  // (grid.onKeyDown), потому что зависит от editor-lock; остальные ловим на
  // документе: Ins должен работать и когда фокус ушёл с грида.
  //
  // ВАЖНО: слушатель в ФАЗЕ ПЕРЕХВАТА. Свой обработчик SlickGrid вешает на
  // канву грида, то есть ГЛУБЖЕ документа, и в фазе всплытия успевает разобрать
  // клавишу первым: Ctrl+↓ у него — «перейти к последней строке», и перемещение
  // строки не срабатывало вовсе. Отсюда же stopPropagation — чтобы после нашей
  // обработки грид не выполнил ещё и свою.
  if (!window._obGridKeysHook) {
    window._obGridKeysHook = true;
    document.addEventListener("keydown", function(e) {
      if (e.defaultPrevented || e.altKey || e.metaKey || e.shiftKey) return;
      if (managedHasBlockingModal()) return;
      var direct = gridNameFromTarget(e.target);
      // Resolve concrete table ownership before interactive-target guards.
      // A readonly editor still has to retire a stale marker from another TP.
      var tp = activeGridName(e.target);
      // Редактор ячейки находится внутри .ob-grid: там структурная клавиша
      // сначала коммитит значение. Обычное поле формы вне грида нельзя
      // перехватывать из-за когда-то активной табличной части.
      if (!direct && gridInteractiveTarget(e.target)) return;
      if (direct && e.target && e.target.closest && e.target.closest('a[href],button,summary,[contenteditable]:not([contenteditable="false"])')) return;
      if (!tp) return;
      var active = (window._obGrids || {})[tp];
      if (!active || active.readOnly) return;
      function take() { e.preventDefault(); e.stopPropagation(); }
      if (e.key === "Insert" && !e.ctrlKey) {
        take();
        if (window.obGridAddRow) window.obGridAddRow(tp);
        return;
      }
      // Явный hotkey кнопки формы важнее встроенного значения клавиши.
      if (e.key === "F9" && !e.ctrlKey && !hasActionableFormHotkey("F9")) {
        take();
        if (window.obGridCopyRow) window.obGridCopyRow(tp);
        return;
      }
      if (e.ctrlKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
        take();
        if (window.obGridMoveRow) window.obGridMoveRow(tp, e.key === "ArrowDown" ? 1 : -1);
      }
    }, true);
  }

  // SlickGrid-aware applyTableParts. Оборачивает window.applyTableParts (DOM-
  // версию из obFire-IIFE): для ТЧ с гридом обновляет модель грида, для
  // остальных вызывает origApplyTP. Активную ячейку сохраняем, чтобы серверный
  // пересчёт сумм (Р2) не сбивал курсор при быстром вводе с клавиатуры.
  var origApplyTP = window.applyTableParts;
  window.applyTableParts = function(tps) {
    if (!tps) return;
    var grids = window._obGrids || {};
    var views = window._obGridViews || [];
    Object.keys(tps).forEach(function(tpName) {
      var matches = views.filter(function(view) { return view.tpName === tpName; });
      if (!matches.length && grids[tpName]) matches = [grids[tpName]];
      matches.forEach(function(g) {
        var active = g.grid.getActiveCell();
        var rows = tps[tpName] || [];
        var cols = g.columnsMeta || [];
        var items = rows.map(function(r, idx) {
          var item = {id: idx, _ord: idx};
          // == null (не || "") — иначе число 0 / false терялись бы как пустая строка.
          for (var i = 0; i < cols.length; i++) item[cols[i].id] = (r[cols[i].id] == null ? "" : r[cols[i].id]);
          copyFormGridStyleKeys(r, item);
          return item;
        });
        g.dataView.setItems(items);
        g.grid.invalidate();
        if (active && active.row < items.length) {
          g.grid.setActiveCell(active.row, active.cell);
        }
        updateTotals(g);
      });
    });
    if (origApplyTP) origApplyTP(tps);
  };

  // setupGrid инициализирует один грид. Вынесено из цикла в отдельную функцию,
  // чтобы каждый грид замыкал свои grid/dataView/tpName (иначе при нескольких
  // ТЧ на форме все подписки замыкали бы последний грид — классический баг var
  // в цикле).
  function setupGrid(div) {
    if (div._obGridState) return;
    var tpName = div.getAttribute("data-sg-tp");

    // ВАЖНО: jsJSON от nil-слайса даёт литерал "null", а не "[]". Без защиты
    // от null для пустой табличной части (новый документ) JSON.parse("null")
    // вернёт null и rowsRaw.map бросит TypeError ДО создания грида — грид не
    // создавался и не регистрировался, из-за чего add/удаление/подбор тихо не
    // работали именно в новых документах.
    var colsRaw = JSON.parse(div.getAttribute("data-sg-cols") || "[]") || [];
    var refOpts = JSON.parse(div.getAttribute("data-sg-ref") || "null") || {};
    var enumLabels = JSON.parse(div.getAttribute("data-sg-enum") || "null") || {};
    var rowsRaw = JSON.parse(div.getAttribute("data-sg-rows") || "[]") || [];

    var columns = buildColumns(colsRaw, refOpts, enumLabels);
    // _ord — исходный порядок строки. Клиентская сортировка меняет ПОРЯДОК
    // ОТОБРАЖЕНИЯ (dataView.sort), но при сохранении (obGridSync) строки
    // сериализуются по _ord — чтобы сортировка «для просмотра» не переставляла
    // строки документа (у табличной части порядок значим).
    var items = rowsRaw.map(function(r, idx) {
      var item = {id: idx, _ord: idx};
      // == null (не || "") — иначе сохранённое числовое 0 грузилось бы пустым.
      for (var i = 0; i < colsRaw.length; i++) item[colsRaw[i].id] = (r[colsRaw[i].id] == null ? "" : r[colsRaw[i].id]);
      copyFormGridStyleKeys(r, item);
      return item;
    });

    var dataView = new Slick.Data.DataView();
    dataView.getItemMetadata = formGridItemMetadata;
    dataView.setItems(items);

    var readOnly = div.getAttribute("data-sg-ro") === "1";
    var options = {
      enableCellNavigation: true,
      enableColumnReorder: false,
      editable: !readOnly,
      // autoEdit:false — как в 1С: клик выделяет ячейку, в редактирование входим
      // по Enter / двойному клику / началу ввода (а не сразу по одиночному клику).
      autoEdit: false,
      autoHeight: false,
      rowHeight: 28,
      headerRowHeight: 30,
      syncColumnCellResize: true,
      enableTextSelectionOnCells: true,
      enableAddRow: false,
      // RowSelectionModel не поставляется с vendored SlickGrid. Не обещаем
      // множественное выделение, пока реальная selection model не установлена.
      multiSelect: false,
      // ВАЖНО: footer-строке нужны ОБЕ опции — createFooterRow создаёт DOM,
      // showFooterRow показывает его. Только showFooterRow без createFooterRow
      // роняет рендер (обращение к несуществующему _footerRowScroller[0]).
      createFooterRow: true,
      showFooterRow: true,
      footerRowHeight: 28
    };

    var grid = new Slick.Grid(div, dataView, columns, options);

    // Каждый DOM-host инициализируется отдельно: readonly summary должна
    // оставаться видимой рядом с writable placement той же табличной части.
    // Канонический реестр, однако, всегда указывает на writable grid независимо
    // от DOM-порядка; только он участвует в изменениях и сериализации.
    var gridState = {
      grid: grid, dataView: dataView, columns: columns,
      columnsMeta: colsRaw, refOpts: refOpts, div: div, readOnly: readOnly,
      tpName: tpName
    };
    div._obGridState = gridState;
    window._obGridViews.push(gridState);
    var registered = window._obGrids[tpName];
    if (!registered || (!readOnly && registered.readOnly)) {
      window._obGrids[tpName] = gridState;
    }
    // activeCell остаётся у нескольких SlickGrid одновременно. Запоминаем
    // именно пользовательский контакт с конкретным DOM-хостом.
    if (!readOnly) {
      div.addEventListener("mousedown", function() { rememberActiveGrid(tpName); }, true);
      div.addEventListener("focusin", function() { rememberActiveGrid(tpName); });
    }

   try {
    dataView.onRowCountChanged.subscribe(function() { grid.updateRowCount(); grid.render(); updateTotals(gridState); });
    dataView.onRowsChanged.subscribe(function(e, args) { grid.invalidateRows(args.rows); grid.render(); });

    // Сортировка по клику на заголовок (колонки sortable). Порядок ОТОБРАЖЕНИЯ;
    // на сохранение не влияет (см. _ord и obGridSync). Числа сортируются как
    // числа, ссылки — по наименованию (_label), остальное — по строке.
    grid.onSort.subscribe(function(e, args) {
      var field = args.sortCol.field;
      var sign = args.sortAsc ? 1 : -1;
      var meta = null;
      for (var i = 0; i < colsRaw.length; i++) { if (colsRaw[i].id === field) { meta = colsRaw[i]; break; } }
      var isNum = meta && meta.type === "number";
      var isRef = meta && meta.ref;
      function keyOf(it) {
        var v = it[field];
        if (isNum) { var n = Number(String(v == null ? "" : v).replace(",", ".")); return isNaN(n) ? -Infinity : n; }
        if (isRef) {
          var id = (v && typeof v === "object") ? (v.id || "") : (v == null ? "" : v);
          var opts = refOpts[field] || [];
          for (var k = 0; k < opts.length; k++) { if (String(opts[k].id) === String(id)) return String(opts[k]._label).toLowerCase(); }
          return String(id).toLowerCase();
        }
        return String(v == null ? "" : v).toLowerCase();
      }
      dataView.sort(function(a, b) { var ka = keyOf(a), kb = keyOf(b); return ka > kb ? sign : (ka < kb ? -sign : 0); });
      grid.invalidate(); grid.render();
    });

    // Клиентский авторасчёт Сумма = Количество × Цена — ТОЛЬКО при явном opt-in
    // (data-sg-autosum ← auto_sum: true у ТЧ в форме). Без флага обычная ТЧ с
    // колонками Цена/Количество/Сумма больше НЕ связывается автоматически (#215.1).
    // Колонки определяем ПО ИМЕНИ (а не «ровно 3 числовые»), чтобы работало и
    // когда есть доп. числовые колонки (НДС и т.п.). Это мгновенная подсказка;
    // точный пересчёт (НДС, итоги — decimal) делает сервер при записи/проведении.
    function num(v) { var n = Number(String(v == null ? "" : v).replace(/\s/g, "").replace(",", ".")); return isNaN(n) ? 0 : n; }
    function findColId(variants) {
      for (var i = 0; i < colsRaw.length; i++) {
        var nm = String(colsRaw[i].name || colsRaw[i].id).toLowerCase();
        for (var j = 0; j < variants.length; j++) { if (nm === variants[j]) return colsRaw[i].id; }
      }
      return null;
    }
    var autoSum = div.getAttribute("data-sg-autosum") === "1";
    var colQty = autoSum ? findColId(["количество", "кол-во", "колво", "кол", "quantity", "qty"]) : null;
    var colPrice = autoSum ? findColId(["цена", "price"]) : null;
    var colSum = autoSum ? findColId(["сумма", "amount", "sum"]) : null;
    grid.onCellChange.subscribe(function(e, args) {
      window._obFormDirty = true;
      if (colQty && colPrice && colSum && args && args.item && args.cell != null) {
        var changed = columns[args.cell] && columns[args.cell].field;
        // Пересчитываем сумму при правке количества/цены; саму сумму не трогаем,
        // если её правят вручную.
        if (changed === colQty || changed === colPrice) {
          args.item[colSum] = num(args.item[colQty]) * num(args.item[colPrice]);
          grid.invalidateRow(args.row); grid.render();
        }
      }
      updateTotals(gridState);
    });

    // Ячейка не прошла проверку (например, в колонке-ссылке набрано то, чего нет
    // в справочнике). SlickGrid сам только подсвечивает ячейку классом invalid и
    // держит курсор в ней — без текста причины это выглядит как «залипание».
    grid.onValidationError.subscribe(function(e, args) {
      var msg = args && args.validationResults && args.validationResults.msg;
      if (msg && window.obFlash) window.obFlash(msg, "err");
    });

    // Delete uses the same mutation path as the toolbar. This keeps the
    // no-SelectionModel fallback and row event ordering identical.
    grid.onKeyDown.subscribe(function(e) {
      var modalOpen = managedHasBlockingModal();
      if (!readOnly && !modalOpen && !e.defaultPrevented && !e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey &&
          e.key === 'Delete' && !grid.getEditorLock().isActive()) {
        if (window.obGridDelRow(tpName)) {
          e.preventDefault();
          e.stopImmediatePropagation();
        }
      }
    });

    // План 48 (Р2): серверный пересчёт зависимых колонок (Сумма = Кол × Цена)
    // через round-trip. Дёргаем obFire('ПриИзменении') только если у элемента
    // ТЧ есть такой обработчик (data-sg-recalc) — иначе впустую гоняли бы сеть
    // на каждый ввод. Debounce 250 мс коалесцирует быстрые правки (вопрос O3).
    // Деньги считаются на сервере (decimal), клиент лишь отображает результат.
    if (div.getAttribute("data-sg-recalc") === "1") {
      var elName = div.getAttribute("data-sg-el") || tpName;
      var recalcTimer = null;
      grid.onCellChange.subscribe(function(e, args) {
        var params = gridCellEventParams(tpName, args, columns, dataView);
        if (recalcTimer) clearTimeout(recalcTimer);
        recalcTimer = setTimeout(function() {
          if (window.obFire) window.obFire(elName, "ПриИзменении", params);
        }, 250);
      });
    }

   } catch (err) {
     // Подписки/настройка дали сбой. Грид уже зарегистрирован выше, поэтому
     // базовые операции работают. Показываем причину, чтобы не гадать вслепую.
     if (window.console) console.error("SlickGrid setup error [" + tpName + "]:", err);
     if (window.obFlash) window.obFlash("Грид «" + tpName + "»: " + (err && err.message ? err.message : err), "err");
   }

    // Растягиваем колонки на ширину контейнера, если грид уже виден. Для ТЧ на
    // скрытой вкладке ресайз отложится до её показа (см. хук на .managed-tab-btn).
    resizeGrid(gridState);
  }

  // Initialize all grids
  function initGrids() {
    var divs = document.querySelectorAll(".ob-grid[data-sg-tp]");
    for (var d = 0; d < divs.length; d++) setupGrid(divs[d]);
  }

  // При переключении вкладки managed-формы её содержимое становится видимым —
  // пересчитываем гриды (inline-onclick кнопки уже переключил display, наш
  // делегированный слушатель на document отработает после него в фазе всплытия).
  if (!window._obTabHook) {
    window._obTabHook = true;
    document.addEventListener("click", function(e) {
      var btn = e.target && e.target.closest ? e.target.closest(".managed-tab-btn") : null;
      if (btn) setTimeout(window._obResizeGrids, 0);
    });
    var _rt = null;
    window.addEventListener("resize", function() {
      if (_rt) clearTimeout(_rt);
      _rt = setTimeout(window._obResizeGrids, 100);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initGrids);
  } else {
    initGrids();
  }
})();

// Авто-вызов ПриОткрытииФормы при загрузке страницы. Без этого
// серверный handler на event="ПриОткрытии" не запускается сам по себе.
obManagedReady(function () {
  var cfg = obManagedConfig();
  if (!cfg.autoOpen) return;
  setTimeout(function () { if (window.obFire) obFire('', 'ПриОткрытии'); }, 0);
});

function obManagedTabKey(tabs) {
  return 'obtab:' + location.pathname + ':' + (tabs.getAttribute('data-tabs') || '');
}

function obManagedSwitchTab(btn) {
  var tabs = btn && btn.closest ? btn.closest('.managed-tabs') : null;
  if (!tabs) return;
  var idx = btn.getAttribute('data-tab-idx') || '0';
  tabs.querySelectorAll('.managed-tab-btn').forEach(function (b) { b.classList.remove('active'); });
  btn.classList.add('active');
  tabs.querySelectorAll('.managed-tab-content').forEach(function (c) { c.style.display = 'none'; });
  var content = tabs.querySelector('.managed-tab-content[data-tab-content="' + idx + '"]');
  if (content) content.style.display = 'block';
  try { sessionStorage.setItem(obManagedTabKey(tabs), idx); } catch (_) {}
  if (window._obResizeGrids) setTimeout(window._obResizeGrids, 0);
}

obManagedReady(function () {
  document.addEventListener('click', function (e) {
    var btn = e.target && e.target.closest ? e.target.closest('.managed-tab-btn') : null;
    if (!btn) return;
    obManagedSwitchTab(btn);
  });
  var groups = document.querySelectorAll('.managed-tabs');
  for (var i = 0; i < groups.length; i++) {
    var tabs = groups[i];
    var idx;
    try { idx = sessionStorage.getItem(obManagedTabKey(tabs)); } catch (_) { idx = null; }
    if (idx == null || idx === '0') continue;
    var btn = tabs.querySelector('.managed-tab-btn[data-tab-idx="' + idx + '"]');
    if (btn) obManagedSwitchTab(btn);
  }
});

// ─── ПолеКода: редактор с подсветкой ────────────────────────────────────────
//
// Монтируется на .code-editor рядом со скрываемой textarea.code-field.
// textarea остаётся источником истины для формы: редактор пишет в неё при
// каждом изменении, поэтому обычный submit работает и без всякой синхронизации
// по кнопке. Без JS (или если Monaco не загрузился) textarea просто остаётся
// видимой и редактируемой — прогрессивное улучшение, как у richtext.
obManagedReady(function () {
  var holders = document.querySelectorAll('.code-editor');
  if (!holders.length) return;
  if (typeof require === 'undefined' || window._monacoLoadErr) return; // textarea уже рабочая

  require.config({ paths: { vs: '/vendor/monaco/vs' } });
  require(['vs/editor/editor.main'], function () {
    for (var i = 0; i < holders.length; i++) {
      (function (holder) {
        if (holder.getAttribute('data-code-ready') === '1') return;
        var ta = holder.previousElementSibling;
        if (!ta || ta.tagName !== 'TEXTAREA' || !ta.classList.contains('code-field')) return;
        holder.setAttribute('data-code-ready', '1');

        var lang = holder.getAttribute('data-code-language') || 'plaintext';
        var ed = monaco.editor.create(holder, {
          value: ta.value,
          language: lang,
          automaticLayout: true,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          fontSize: 13,
          tabSize: 4,
          renderWhitespace: 'selection'
        });
        ta.style.display = 'none';

        // Ответ обработчика формы может обновить значение поля. textarea уже
        // получила бы новый текст, но Monaco продолжал показывать старый и при
        // следующем вводе затирал ответ сервера. Держим оба представления
        // синхронными и не выдаём программное обновление за правку пользователя.
        var syncing = false;
        ta._obSetCodeValue = function (value) {
          value = String(value == null ? '' : value);
          ta.value = value;
          if (ed.getValue() === value) return;
          syncing = true;
          try {
            ed.setValue(value);
          } finally {
            syncing = false;
          }
        };
        ed.onDidChangeModelContent(function () {
          ta.value = ed.getValue();
          if (!syncing) ta.dispatchEvent(new Event('input', { bubbles: true }));
        });
        ed.onDidBlurEditorWidget(function () {
          if (!syncing) ta.dispatchEvent(new Event('change', { bubbles: true }));
        });
      })(holders[i]);
    }
  });
});
