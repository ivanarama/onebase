// Вкладочная оболочка (issue #129/#130): когда страница открыта во фрейме
// оболочки /ui/app, прячем хром (топбар/подсистемы) — навигация идёт из оболочки.
window.__obEmbedded = window.self !== window.top;
if (window.__obEmbedded) {
  document.documentElement.className += ' ob-embedded';
  // #481: заголовок вкладки = представление записи. Сервер рендерит на карточке
  // существующей записи <meta name="ob-tab-title">; сообщаем его оболочке
  // (приёмник obSetTitle в tabs.go). ui.js подключён в <head>, поэтому читаем
  // мету после готовности DOM.
  (function () {
    function sendTabTitle() {
      try {
        var mt = document.querySelector('meta[name="ob-tab-title"]');
        var tt = mt && mt.getAttribute('content');
        if (tt && window.parent && window.parent !== window) {
          window.parent.postMessage({ source: 'obSetTitle', title: tt }, window.location.origin);
        }
      } catch (_) {}
    }
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', sendTabTitle);
    else sendTabTitle();
  })();
  // Фаза 2: открытие записи/новой формы/отчёта внутри вкладки — это новая
  // вкладка рядом, а не замена текущей (пагинация/сортировка/фильтры остаются
  // в той же вкладке — у них тот же путь списка, без id-сегмента).
  var obOpenableForm = function (href) {
    if (!/^\/ui\//.test(href)) return false;
    if (/^\/ui\/(admin|about|logout|login|logo|debug|app|_(?!ref-open|ref-create))/.test(href)) return false;
    if (href.indexOf('_popup=1') >= 0) return false;
    if (/^\/ui\/(report|processor)\/[^\/?#]+/.test(href)) return true;
    if (/^\/ui\/[^\/?#]+\/[^\/?#]+\/[^\/?#]+/.test(href)) return true;
    return false;
  };
  window.obOpenInShell = function (href, title, allowDup) {
    if (!obOpenableForm(href)) return false;
    var shell = null;
    try {
      if (window.parent && window.parent.obOpenTab) shell = window.parent;
    } catch (_) {}
    if (!shell) return false;
    try {
      shell.postMessage({ source: 'obOpenTab', url: href, title: title || 'Форма', allowDup: !!allowDup }, '*');
    } catch (_) {}
    return true;
  };
  window.obCloseInShell = function () {
    var shell = null;
    try {
      if (window.parent && window.parent.obOpenTab) shell = window.parent;
    } catch (_) {}
    if (!shell) return false;
    try {
      shell.postMessage({ source: 'obCloseTab' }, window.location.origin);
    } catch (_) {
      return false;
    }
    return true;
  };
  document.addEventListener('click', function (e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target.closest ? e.target.closest('a[href]') : null;
    if (!a || a.target === '_blank') return;
    if (a.hasAttribute('data-ob-close-tab') && window.obCloseInShell()) {
      e.preventDefault();
      return;
    }
    var href = a.getAttribute('href') || '';
    var title = (a.getAttribute('title') || a.textContent || '').replace(/\s+/g, ' ').trim() || 'Форма';
    if (!window.obOpenInShell(href, title)) return;
    e.preventDefault();
  });
  // Фаза 3: сообщаем оболочке о несохранённых правках, чтобы она предупредила при
  // закрытии вкладки/окна (защита от потери ввода).
  (function () {
    var dirty = false;
    function report(d) {
      if (d === dirty) return;
      dirty = d;
      try {
        if (window.parent && window.parent.obOpenTab) window.parent.postMessage({ source: 'obDirty', dirty: d }, '*');
      } catch (_) {}
    }
    function onEdit(e) {
      var t = e.target;
      if (t && t.matches && t.matches('input,textarea,select')) report(true);
    }
    document.addEventListener('input', onEdit, true);
    document.addEventListener('change', onEdit, true);
    document.addEventListener('submit', function () {
      report(false);
    }, true);
  })();
}

function obReady(fn) {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', fn);
  else fn();
}

function obReadJSONScript(id, fallback) {
  var el = document.getElementById(id);
  if (!el) return fallback;
  var raw = el.textContent || '';
  if (!raw.trim()) return fallback;
  try {
    return JSON.parse(raw);
  } catch (e) {
    return fallback;
  }
}

(function () {
  if (window.__obNavInit) return;
  window.__obNavInit = true;
  function setNav(open) {
    document.body.classList.toggle('nav-open', open);
    var btn = document.querySelector('.nav-toggle');
    if (btn) btn.setAttribute('aria-expanded', open ? 'true' : 'false');
  }
  window.obNavToggle = function () {
    setNav(!document.body.classList.contains('nav-open'));
  };
  obReady(function () {
    document.addEventListener('click', function (e) {
      if (!e.target.closest) return;
      var navToggle = e.target.closest('[data-ob-nav-toggle]');
      if (navToggle) {
        e.preventDefault();
        window.obNavToggle();
        return;
      }
      var toggle = e.target.closest('[data-ob-toggle-target]');
      if (toggle) {
        e.preventDefault();
        var target = document.getElementById(toggle.getAttribute('data-ob-toggle-target') || '');
        if (target) target.classList.toggle('open');
        return;
      }
      var prevent = e.target.closest('[data-ob-prevent]');
      if (prevent) e.preventDefault();
    });
    document.addEventListener('click', function (e) {
      if (!document.body.classList.contains('nav-open')) return;
      if (e.target.closest && e.target.closest('.nav-toggle')) return;
      var as = document.getElementById('ob-nav');
      if (as && as.contains(e.target)) return;
      setNav(false);
    }, true);
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && document.body.classList.contains('nav-open')) setNav(false);
    });
    try {
      document.querySelectorAll('aside details.navsec').forEach(function (d) {
        var key = 'navsec:' + d.getAttribute('data-navsec');
        var saved = localStorage.getItem(key);
        if (saved === '1') d.open = true;
        else if (saved === '0') d.open = false;
        d.addEventListener('toggle', function () { localStorage.setItem(key, d.open ? '1' : '0'); });
      });
    } catch (e) {}
  });
})();

function obApplyValueAxisFormatter(opt) {
  if (opt && opt.yAxis && opt.yAxis.type === 'value') {
    opt.yAxis.axisLabel = {
      formatter: function (v) {
        if (Math.abs(v) >= 1e6) return (v / 1e6).toFixed(1) + 'M';
        if (Math.abs(v) >= 1e3) return (v / 1e3).toFixed(1) + 'k';
        return v % 1 === 0 ? v : v.toFixed(2);
      }
    };
  }
}

function obInitMappedCharts(jsonID, selector, attrName, errorText, formatValueAxis) {
  if (!window.echarts) return;
  var charts = obReadJSONScript(jsonID, {});
  var nodes = document.querySelectorAll(selector);
  for (var i = 0; i < nodes.length; i++) {
    var node = nodes[i];
    if (node.getAttribute('data-ob-init')) continue;
    var opt = charts[node.getAttribute(attrName)];
    if (!opt) continue;
    node.setAttribute('data-ob-init', '1');
    try {
      var c = echarts.init(node);
      opt.animation = false;
      if (formatValueAxis) obApplyValueAxisFormatter(opt);
      c.setOption(opt);
      (function (chart) { window.addEventListener('resize', function () { chart.resize(); }); })(c);
    } catch (e) {
      console.error(errorText, e);
    }
  }
}

function obInitReportChart() {
  if (!window.echarts) return;
  var node = document.getElementById('ob-chart');
  if (!node || node.getAttribute('data-ob-init')) return;
  var opt = obReadJSONScript('ob-report-chart', null);
  if (!opt) return;
  node.setAttribute('data-ob-init', '1');
  try {
    var c = echarts.init(node);
    opt.animation = false;
    obApplyValueAxisFormatter(opt);
    c.setOption(opt);
    window.addEventListener('resize', function () { c.resize(); });
  } catch (e) {
    console.error('report chart init failed', e);
  }
}

obReady(function () {
  obInitMappedCharts('ob-widget-charts', '.w-chart-canvas[data-widget]', 'data-widget', 'chart init failed', true);
  obInitMappedCharts('ob-page-charts', '.w-chart-canvas[data-pagechart]', 'data-pagechart', 'page chart init failed', false);
  obInitReportChart();
});

function obInitFormDirty() {
  var f = document.querySelector('#main-form[data-ob-dirty-watch="1"]');
  if (!f) return;
  window._obFormDirty = false;
  var base = document.title;
  function mark() {
    window._obFormDirty = true;
    if (document.title.charAt(0) !== '●') document.title = '● ' + base;
  }
  f.addEventListener('input', mark, true);
  f.addEventListener('change', mark, true);
  f.addEventListener('submit', function () { window._obFormDirty = false; });
  window.addEventListener('beforeunload', function (e) {
    if (window._obFormDirty) {
      e.preventDefault();
      e.returnValue = '';
      return '';
    }
  });
  // Auto-generated entity forms do not load managed.js, so their documented
  // Escape shortcut has to live here. Let an open picker consume Escape first.
  document.addEventListener('keydown', function (e) {
    if (e.defaultPrevented || (e.key !== 'Escape' && e.keyCode !== 27) || obHasBlockingModal()) return;
    var cancel = document.querySelector('[data-ob-popup-cancel], [data-ob-close-tab], a.btn-cancel');
    if (!cancel) return;
    if (window._obFormDirty && !confirm('Данные были изменены и не записаны. Закрыть форму?')) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    cancel.click();
  }, true);
}
obReady(obInitFormDirty);

function obInitAttachments() {
  var panel = document.querySelector('[data-ob-attachments]');
  if (!panel) return;
  var url = panel.getAttribute('data-attachments-url') || '';
  if (!url) return;
  function fmtSize(b) {
    if (b < 1024) return b + ' Б';
    if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' КБ';
    return (b / 1024 / 1024).toFixed(1) + ' МБ';
  }
  function loadAtts() {
    fetch(url)
      .then(function (r) { return r.json(); })
      .then(function (atts) {
        var cnt = document.getElementById('att-count');
        var list = document.getElementById('att-list');
        if (!cnt || !list) return;
        cnt.textContent = atts.length ? atts.length + ' файл(ов)' : '';
        if (!atts.length) {
          list.innerHTML = '<p style="color:#94a3b8;font-size:13px;margin:0">Нет вложений</p>';
          return;
        }
        list.innerHTML = '';
        atts.forEach(function (a) {
          var row = document.createElement('div');
          row.style.cssText = 'display:flex;align-items:center;gap:8px;padding:6px 0;border-bottom:1px solid #f1f5f9';
          var nameEl = document.createElement('span');
          nameEl.style.cssText = 'flex:1;font-size:13px;word-break:break-all';
          nameEl.textContent = String(a.filename == null ? '' : a.filename);
          var sizeEl = document.createElement('span');
          sizeEl.style.cssText = 'color:#94a3b8;font-size:12px;white-space:nowrap';
          sizeEl.textContent = fmtSize(a.size_bytes);
          var aid = encodeURIComponent(String(a.id));
          var dl = document.createElement('a');
          dl.href = '/ui/attachments/' + aid + '/download';
          dl.className = 'btn btn-sm btn-secondary';
          dl.style.cssText = 'padding:3px 10px;font-size:12px';
          dl.textContent = '↓';
          var delForm = document.createElement('form');
          delForm.method = 'POST';
          delForm.action = '/ui/attachments/' + aid + '/delete';
          delForm.style.margin = '0';
          delForm.addEventListener('submit', function (e) {
            if (!confirm('Удалить вложение?')) e.preventDefault();
          });
          var delBtn = document.createElement('button');
          delBtn.type = 'submit';
          delBtn.className = 'btn btn-sm btn-danger';
          delBtn.style.cssText = 'padding:3px 8px;font-size:12px';
          delBtn.textContent = '×';
          delForm.appendChild(delBtn);
          row.appendChild(nameEl);
          row.appendChild(sizeEl);
          row.appendChild(dl);
          row.appendChild(delForm);
          list.appendChild(row);
        });
      }).catch(function () {});
  }
  loadAtts();
}
obReady(obInitAttachments);

function rsNorm(v) { return String(v || '').toLowerCase(); }

function rsFieldMap(values) {
  var out = {};
  (values || []).forEach(function (v) { if (v) out[rsNorm(v)] = v; });
  return out;
}

window.rsBeforeSubmit = function (ev) {
  var form = ev && ev.target;
  if (form && form.dataset && form.dataset.skipCollect === '1') {
    form.dataset.skipCollect = '';
    return true;
  }
  window.rsCollect();
  return true;
};

window.rsChoosePreset = function (sel) {
  if (!sel || !sel.form) return;
  var h = sel.form.querySelector('input[name="__settings"]');
  if (h) h.remove();
  sel.form.dataset.skipCollect = '1';
  sel.form.submit();
};

function obPresetReportSettings() {
  var hidden = document.getElementById('rs-json');
  if (!hidden) return;
  var raw = hidden.value || hidden.dataset.base || '';
  if (!raw) return;
  if (!hidden.value) hidden.value = raw;
  try {
    var s = JSON.parse(raw);
    var comp = (s && s.composition) || {};
    var groups = comp.Groupings || comp.groupings || [];
    var meas = comp.Measures || comp.measures || [];
    var mf = meas.map(function (m) { return m.Field || m.field; });
    var groupMap = rsFieldMap(groups);
    var measureMap = rsFieldMap(mf);
    document.querySelectorAll('.rs-group,.rs-measure').forEach(function (el) { el.checked = false; });
    document.querySelectorAll('.rs-group').forEach(function (el) { if (groupMap[rsNorm(el.value)]) el.checked = true; });
    document.querySelectorAll('.rs-measure').forEach(function (el) { if (measureMap[rsNorm(el.value)]) el.checked = true; });
    var ap = comp.Appearance || comp.appearance || {};
    var lines = ap.lines || ap.Lines || '';
    if (lines === 'horizontal') lines = '';
    var le = document.getElementById('rs-lines');
    if (le) le.value = lines;
    var ze = document.getElementById('rs-zebra');
    if (ze) ze.checked = !!(ap.zebra || ap.Zebra);
    // Начальное сворачивание групп: пустое поле = ключа нет = всё развёрнуто.
    // Ноль — значимое значение, поэтому проверяем именно на null/undefined.
    var ce = document.getElementById('rs-collapse-to');
    if (ce) {
      var ct = ap.collapse_to;
      if (ct === null || ct === undefined) ct = ap.CollapseTo;
      ce.value = (ct === null || ct === undefined) ? '' : ct;
    }
  } catch (e) {}
}

window.rsCollect = function () {
  var hidden = document.getElementById('rs-json');
  var prev = {};
  var raw = hidden ? (hidden.value || hidden.dataset.base || '') : '';
  if (hidden && !hidden.value && raw) hidden.value = raw;
  if (raw) {
    try {
      prev = JSON.parse(raw) || {};
    } catch (e) {
      prev = {};
    }
  }
  var prevComp = (prev && prev.composition) || {};
  var prevGroups = prevComp.Groupings || prevComp.groupings || [];
  var prevGroupByField = rsFieldMap(prevGroups);
  var prevMeasures = prevComp.Measures || prevComp.measures || [];
  var prevByField = {};
  var prevMeasureField = {};
  prevMeasures.forEach(function (m) {
    var f = m && (m.Field || m.field);
    if (f) {
      prevByField[rsNorm(f)] = m;
      prevMeasureField[rsNorm(f)] = f;
    }
  });
  var groupings = [];
  document.querySelectorAll('.rs-group:checked').forEach(function (c) {
    groupings.push(prevGroupByField[rsNorm(c.value)] || c.value);
  });
  var measures = [];
  document.querySelectorAll('.rs-measure:checked').forEach(function (c) {
    var key = rsNorm(c.value);
    var src = prevByField[key] || {};
    var m = { Field: prevMeasureField[key] || c.value, Agg: src.Agg || src.agg || 'sum' };
    var title = src.Title || src.title;
    if (title) m.Title = title;
    var align = src.Align || src.align;
    if (align) m.Align = align;
    var format = src.Format || src.format;
    if (format) m.Format = format;
    measures.push(m);
  });
  var filters = [];
  document.querySelectorAll('.rs-filter-row').forEach(function (row) {
    var f = row.querySelector('.rs-f-field');
    var op = row.querySelector('.rs-f-op');
    var v = row.querySelector('.rs-f-value');
    if (f && op && f.value) filters.push({ field: f.value, op: op.value, value: v ? v.value : '' });
  });
  var variantEl = document.querySelector('input[name="__variant"]');
  var lines = (document.getElementById('rs-lines') || {}).value || '';
  var zebra = !!(document.getElementById('rs-zebra') || {}).checked;
  var columns = prevComp.Columns || prevComp.columns || [];
  var sort = prevComp.Sort || prevComp.sort || [];
  var totals = prevComp.Totals || prevComp.totals;
  var detail = (typeof prevComp.Detail !== 'undefined') ? prevComp.Detail : prevComp.detail;
  var appearance = { lines: lines, zebra: zebra };
  var collapseTo = parseInt(((document.getElementById('rs-collapse-to') || {}).value || '').trim(), 10);
  if (!isNaN(collapseTo) && collapseTo >= 0) appearance.collapse_to = collapseTo;
  var nextComp = { Groupings: groupings, Measures: measures, Appearance: appearance };
  if (columns && columns.length) nextComp.Columns = columns;
  if (sort && sort.length) nextComp.Sort = sort;
  if (totals) nextComp.Totals = totals;
  if (typeof detail !== 'undefined') nextComp.Detail = !!detail;
  var s = { variant: variantEl ? variantEl.value : '', composition: nextComp, filters: filters };
  if (hidden) hidden.value = JSON.stringify(s);
};

window.rsAddFilter = function () {
  var tpl = document.getElementById('rs-filter-tpl');
  var rows = document.getElementById('rs-filter-rows');
  if (!tpl || !tpl.content || !rows) return;
  rows.appendChild(tpl.content.cloneNode(true));
};

function obReportRemoveSettingsInput(form) {
  if (!form) return;
  var hidden = form.querySelector('input[name="__settings"]');
  if (hidden) hidden.remove();
}

function obReportSubmitSelect(sel, resetPreset) {
  if (!sel || !sel.form) return;
  obReportRemoveSettingsInput(sel.form);
  if (resetPreset) {
    var preset = sel.form.querySelector('select[name="__preset"]');
    if (preset) preset.value = '__standard';
  }
  sel.form.submit();
}

function obInitReportDelegates() {
  if (window.__obReportDelegates) return;
  window.__obReportDelegates = true;
  document.addEventListener('change', function (e) {
    if (!e.target.closest) return;
    var preset = e.target.closest('[data-ob-report-preset-submit]');
    if (preset) {
      obReportSubmitSelect(preset, false);
      return;
    }
    var variant = e.target.closest('[data-ob-report-variant-submit]');
    if (variant) {
      obReportSubmitSelect(variant, true);
      return;
    }
    var rsPreset = e.target.closest('[data-ob-rs-choose-preset]');
    if (rsPreset) window.rsChoosePreset(rsPreset);
  });
  document.addEventListener('click', function (e) {
    if (!e.target.closest) return;
    var addFilter = e.target.closest('[data-ob-rs-add-filter]');
    if (addFilter) {
      e.preventDefault();
      window.rsAddFilter();
    }
  });
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || !form.matches || !form.matches('[data-ob-rs-before-submit]')) return;
    if (window.rsBeforeSubmit(e) === false) e.preventDefault();
  }, true);
}

// rcBuildView — состояние сворачивания одной скомпонованной таблицы отчёта.
// Каждая группа помнит, раскрыта ли она; видимость строки пересчитывается по
// цепочке предков. Прежний код прятал/показывал поддерево целиком по префиксу
// пути, из-за чего клик по свёрнутой группе вываливал сразу все уровни до
// деталей — с начальным сворачиванием (issue #575) это делало его бесполезным.
function rcBuildView(table) {
  var tbody = table.tBodies[0] || table;
  var groups = [];
  var byPath = {};
  tbody.querySelectorAll('tr.grp').forEach(function (tr) {
    var cell = tr.querySelector('td');
    var g = {
      tr: tr,
      cell: cell,
      path: tr.getAttribute('data-group') || '',
      // Начальное состояние рисует сервер (appearance.collapse_to): «▶» —
      // группа свёрнута. Без ключа все группы приходят развёрнутыми.
      open: !cell || cell.textContent.trim().charAt(0) !== '▶'
    };
    groups.push(g);
    byPath[g.path] = g;
  });

  // Путь родителя — путь без последнего сегмента. Разделитель внутри значения
  // группировки сервер экранирует (%2F), поэтому резать по «/» безопасно.
  function parentOf(path) {
    var i = path.lastIndexOf('/');
    return i <= 0 ? '' : path.slice(0, i);
  }
  function ancestorsOpen(path) {
    for (var p = parentOf(path); p !== ''; p = parentOf(p)) {
      var g = byPath[p];
      if (g && !g.open) return false;
    }
    return true;
  }
  function apply() {
    groups.forEach(function (g) {
      g.tr.style.display = ancestorsOpen(g.path) ? '' : 'none';
      if (!g.cell) return;
      var mark = g.open ? '▼' : '▶';
      var text = g.cell.textContent;
      if (text.charAt(0) !== mark) g.cell.textContent = mark + text.slice(1);
    });
    tbody.querySelectorAll('tr.det,tr.subtotal').forEach(function (tr) {
      var g = byPath[tr.getAttribute('data-parent') || ''];
      var show = !g || (g.open && ancestorsOpen(g.path));
      tr.style.display = show ? '' : 'none';
    });
  }

  groups.forEach(function (g) {
    g.tr.style.cursor = 'pointer';
    g.tr.addEventListener('click', function () {
      g.open = !g.open;
      apply();
    });
  });

  return {
    setAll: function (open) {
      groups.forEach(function (g) { g.open = open; });
      apply();
    }
  };
}

function obInitReportCompositionControls() {
  var views = [];
  document.querySelectorAll('table.report-composed').forEach(function (table) {
    views.push(rcBuildView(table));
  });
  if (!views.length) return;
  function setAll(open) {
    return function () { views.forEach(function (v) { v.setAll(open); }); };
  }
  var expandBtn = document.getElementById('rc-expand');
  if (expandBtn) expandBtn.addEventListener('click', setAll(true));
  var collapseBtn = document.getElementById('rc-collapse');
  if (collapseBtn) collapseBtn.addEventListener('click', setAll(false));
}

function obInitReportBlocks() {
  try {
    document.querySelectorAll('details.report-block').forEach(function (el) {
      var key = 'rb-' + location.pathname + '-' + el.dataset.block;
      var saved = localStorage.getItem(key);
      if (saved === '1') el.open = true;
      else if (saved === '0') el.open = false;
      el.addEventListener('toggle', function () { localStorage.setItem(key, el.open ? '1' : '0'); });
    });
  } catch (e) {}
}

function jlMove(btn, dir) {
  var tr = btn && btn.closest ? btn.closest('tr') : null;
  if (!tr || !tr.parentNode) return;
  if (dir < 0 && tr.previousElementSibling) tr.parentNode.insertBefore(tr, tr.previousElementSibling);
  if (dir > 0 && tr.nextElementSibling) tr.parentNode.insertBefore(tr.nextElementSibling, tr);
}

function jlCollect() {
  var rows = document.querySelectorAll('#jl-columns .jl-col-row');
  var cols = [];
  rows.forEach(function (row) {
    var cb = row.querySelector('.jl-visible');
    cols.push({ field: row.getAttribute('data-field') || '', visible: !!(cb && cb.checked) });
  });
  var hidden = document.getElementById('jl-settings-json');
  if (hidden) hidden.value = JSON.stringify({ columns: cols });
}

function jlBeforeSubmit() {
  jlCollect();
  return true;
}

window.jlMove = jlMove;
window.jlCollect = jlCollect;
window.jlBeforeSubmit = jlBeforeSubmit;

function obInitJournalDelegates() {
  if (window.__obJournalDelegates) return;
  window.__obJournalDelegates = true;
  document.addEventListener('click', function (e) {
    if (!e.target.closest) return;
    var move = e.target.closest('[data-ob-jl-move]');
    if (move) {
      e.preventDefault();
      jlMove(move, parseInt(move.getAttribute('data-ob-jl-move') || '0', 10));
      return;
    }
  });
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || !form.matches || !form.matches('[data-ob-jl-before-submit]')) return;
    if (jlBeforeSubmit() === false) e.preventDefault();
  }, true);
}

obReady(function () {
  obInitReportDelegates();
  obInitJournalDelegates();
  obPresetReportSettings();
  obInitReportCompositionControls();
  obInitReportBlocks();
});

// Выделенная строка списка — ссылка на DOM-узел, а не id: команды меню читают
// с неё data-*-url. Читать переменную напрямую нельзя, только через listSel():
// узел мог быть отцеплен от документа живым обновлением списка (план 87
// заменяет innerHTML контейнера целиком). Тогда подсветки на экране уже нет, а
// переменная всё ещё указывает на строку — команда сработала бы по записи,
// которую пользователь не выбирал и не видит.
var _listSel = null;

function listSel() {
  if (_listSel && (!document.contains(_listSel) || !obElementVisible(_listSel))) {
    var stale = _listSel;
    _listSel = null;
    listSelPaint(stale, false);
    stale.setAttribute('aria-selected', 'false');
    stale.setAttribute('tabindex', '-1');
  }
  return _listSel;
}

function obListConfig() {
  return obReadJSONScript('ob-list-config', { labels: {} }) || { labels: {} };
}

function obListLabel(key, fallback) {
  var labels = obListConfig().labels || {};
  return labels[key] || fallback;
}

function listTitle() {
  var h = document.querySelector('h2');
  return h ? h.textContent.replace(/\s+/g, ' ').trim() : 'Форма';
}

function listOpen(url, title) {
  if (!url) return;
  try {
    if (window.obOpenInShell && window.obOpenInShell(url, title || listTitle())) return;
  } catch (e) {}
  window.location.href = url;
}

function listSelPaint(el, on) {
  el.querySelectorAll('td').forEach(function (td) { td.style.background = on ? '#dbeafe' : ''; });
  el.classList.toggle('tile-selected', on);
}

// Единственное место, где меняется выделение: снимает подсветку с прежней
// строки, ставит на новую (null — снять выделение) и приводит кнопку
// «Действия» в соответствие новому состоянию.
function listSetSel(tr, options) {
  var prev = listSel();
  if (prev && prev !== tr) {
    listSelPaint(prev, false);
    prev.setAttribute('aria-selected', 'false');
    prev.setAttribute('tabindex', '-1');
  }
  document.querySelectorAll('[data-ob-list-row]').forEach(function (row) {
    if (row === tr) return;
    row.setAttribute('aria-selected', 'false');
    row.setAttribute('tabindex', '-1');
  });
  _listSel = tr || null;
  if (_listSel) {
    listSelPaint(_listSel, true);
    _listSel.setAttribute('aria-selected', 'true');
    _listSel.setAttribute('tabindex', '0');
    if (options && options.focus && _listSel.focus) {
      try { _listSel.focus({ preventScroll: true }); } catch (_) { _listSel.focus(); }
    }
  } else {
    var first = options && options.root
      ? obListRowsIn(options.root)[0]
      : obListRows()[0];
    if (first) {
      first.setAttribute('tabindex', '0');
      if (options && options.focus && first.focus) {
        try { first.focus({ preventScroll: true }); } catch (_) { first.focus(); }
      }
    }
  }
  listSyncActionsBtn();
  // Панель следует за курсором: стрелки ↑↓ двигают выбор. Для списков сущностей
  // карточка при открытой панели лениво перечитывается отдельным защищённым GET.
  if (typeof obDetailRender === 'function') obDetailRender();
}

function obListRowsIn(root) {
  if (!root || !root.querySelectorAll) return [];
  return Array.prototype.slice.call(root.querySelectorAll('[data-ob-list-row]')).filter(obElementVisible);
}

// A live refresh replaces every row node. Even without a selected row, each
// refreshed list must keep one Tab entry point (the first visible row).
function obEnsureListRovingTabindex(root) {
  var rows = obListRowsIn(root);
  var selected = listSel();
  var current = selected && root.contains && root.contains(selected) ? selected : null;
  for (var i = 0; i < rows.length; i++) {
    rows[i].setAttribute('tabindex', rows[i] === (current || rows[0]) ? '0' : '-1');
  }
}

// Пока строка не выбрана, кнопка «Действия» приглушена, но остаётся на месте и
// остаётся кликабельной. Прятать её нельзя: на свежеоткрытом списке не выбрано
// ничего, и кнопка исчезала бы при каждом открытии — пользователь просто не
// узнал бы, что она есть. Атрибут disabled тоже не годится: браузер гасит на
// нём клик, и объяснить причину было бы негде.
function listSyncActionsBtn() {
  var on = !!listSel();
  document.querySelectorAll('[data-ob-list-actions]').forEach(function (b) {
    b.setAttribute('aria-disabled', on ? 'false' : 'true');
    b.title = on
      ? obListLabel('actionsReady', 'Команды для выбранной строки')
      : obListLabel('selectRowFirst', 'Сначала выберите строку списка');
  });
}

// Возврат выделения после перерисовки списка: та же запись опознаётся по
// data-open-url (в нём id, у всех трёх видов строк — таблица, плитка, дерево).
// Записи не стало в выдаче — выделение снимается, а не остаётся на призраке.
function listRestoreSel(key, root, options) {
  var next = null;
  if (key) {
    var rows = (root || document).querySelectorAll('[data-ob-list-row]');
    for (var i = 0; i < rows.length; i++) {
      if (rows[i].getAttribute('data-open-url') === key) { next = rows[i]; break; }
    }
  }
  listSetSel(next, { focus: !!(options && options.focus), root: root || document });
  obEnsureListRovingTabindex(root || document);
}

function obReplaceLiveListContents(cur, fresh) {
  if (!cur || !fresh) return;
  var selected = listSel();
  var selMine = !!(selected && cur.contains(selected));
  var restoreFocus = !!(selMine && document.activeElement && cur.contains(document.activeElement));
  var selKey = selMine ? listSelKey() : '';
  // Даже если выбранной строки сейчас нет, кэш мог остаться от прежнего выбора.
  // После live refresh такой ответ уже не описывает новую версию списка.
  if (typeof obDetailInvalidate === 'function') obDetailInvalidate();
  cur.innerHTML = fresh.innerHTML;
  if (selMine) listRestoreSel(selKey, cur, { focus: restoreFocus });
  else obEnsureListRovingTabindex(cur);
}

function listSelKey() {
  var sel = listSel();
  return sel ? (sel.getAttribute('data-open-url') || '') : '';
}

function listRowClick(e, tr) {
  if (obIsInteractiveTarget(e.target)) return;
  listSetSel(tr, { focus: true });
}

function listRowDblClick(e, tr) {
  if (obIsInteractiveTarget(e.target)) return;
  listActivateRow(tr);
}

function listActivateRow(tr) {
  if (!tr) return;
  if (tr.dataset.isFolder === '1') window.location.href = tr.dataset.folderUrl;
  else listOpen(tr.dataset.openUrl);
}

// Встроенные горячие клавиши — привычные по 1С. Живут в ui.js, потому что нужны
// и на списках, и на формах — как автогенерируемых, так и управляемых
// (managed.js грузится только на вторых).
//
//   Форма:  Ctrl+Enter — «Провести и закрыть» (нет такой кнопки — «Записать»),
//           Ctrl+S — «Записать».
//   Список: Ins — создать, ↑/↓ — курсор по строкам, Enter/F2 — открыть,
//           Ctrl+F — строка поиска. Delete (пометить на удаление) — отдельно.
//
// Буквенные сочетания разбираем по e.code, а не по e.key: при русской раскладке
// Ctrl+S приходит как «ы», и проверка по e.key просто не сработала бы.
function obFormActionButton(values) {
  for (var i = 0; i < values.length; i++) {
    var btn = document.querySelector('button[name="_action"][value="' + values[i] + '"]');
    if (btn && !btn.disabled) return btn;
  }
  return null;
}

function obIsTypingTarget(el) {
  if (!el) return false;
  if (el.isContentEditable || (el.closest && el.closest('[contenteditable]:not([contenteditable="false"])'))) return true;
  if (!el.tagName) return false;
  return /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName);
}

function obIsInteractiveTarget(el) {
  if (!el || !el.closest) return false;
  return !!el.closest('a[href],button,input,textarea,select,option,summary,[contenteditable]:not([contenteditable="false"]),[role="button"],[role="link"],[role="menuitem"]');
}

function obHasBlockingModal() {
  return !!(document.getElementById('_ref-picker-modal') ||
    document.getElementById('_item-picker-modal') ||
    document.getElementById('_ref-create-modal'));
}
window.obHasBlockingModal = obHasBlockingModal;

function obElementVisible(el) {
  if (!el) return false;
  for (var cur = el; cur && cur.nodeType === 1; cur = cur.parentElement) {
    if (cur.hidden || cur.getAttribute('aria-hidden') === 'true' || cur.style.display === 'none' || cur.style.visibility === 'hidden') return false;
    if (window.getComputedStyle) {
      var style = window.getComputedStyle(cur);
      if (style && (style.display === 'none' || style.visibility === 'hidden')) return false;
    }
  }
  return true;
}
window.obElementVisible = obElementVisible;

function obNormalizeFormHotkey(value) {
  var key = String(value || '').trim().toUpperCase();
  if (key === 'F2' || key === 'F4' || key === 'F7' || key === 'F8' || key === 'F9' || key === 'F10') return key;
  return '';
}
window.obNormalizeFormHotkey = obNormalizeFormHotkey;

function obFormHotkeyCandidateEnabled(candidate, root) {
  if (!candidate || typeof candidate.click !== 'function') return false;
  for (var cur = candidate; cur && cur.nodeType === 1; cur = cur.parentElement) {
    if (cur.disabled === true || cur.inert === true) return false;
    if (cur.getAttribute) {
      if (String(cur.getAttribute('aria-disabled') || '').trim().toLowerCase() === 'true') return false;
      if (cur.hasAttribute && cur.hasAttribute('inert')) return false;
      // matches(':disabled') follows the browser's fieldset rules, including
      // controls disabled by an ancestor rather than by their own property.
      if (cur.matches) {
        try { if (cur.matches(':disabled')) return false; } catch (_) {}
      }
      // Minimal DOM implementations may not support :disabled. Fail closed for
      // an explicitly disabled fieldset there as well.
      if (String(cur.tagName || '').toUpperCase() === 'FIELDSET' &&
          (cur.disabled === true || cur.hasAttribute('disabled'))) return false;
    }
    if (cur === root) break;
  }
  return true;
}

// obResolveActionableFormHotkey is the single authority for both dispatching
// a form hotkey and deciding whether that hotkey suppresses a built-in table
// action. It deliberately ignores the rest of the document: a stale, hidden,
// detached or background-form button must never steal F9 from the active form.
function obResolveActionableFormHotkey(key) {
  var wanted = obNormalizeFormHotkey(key);
  if (!wanted) return null;
  var form = document.getElementById('main-form');
  if (!form || !document.contains(form)) return null;
  var candidates = form.querySelectorAll('[data-ob-hotkey]');
  for (var i = 0; i < candidates.length; i++) {
    var candidate = candidates[i];
    if (obNormalizeFormHotkey(candidate.getAttribute('data-ob-hotkey')) !== wanted) continue;
    if (!document.contains(candidate) || !form.contains(candidate)) continue;
    // Managed buttons without an official click action are visual decoration:
    // clicking them is a no-op and must not suppress F9's table-row copy.
    if (!candidate.getAttribute || !String(candidate.getAttribute('data-ob-fire-click') || '').trim()) continue;
    if (!obElementVisible(candidate) || !obFormHotkeyCandidateEnabled(candidate, form)) continue;
    return candidate;
  }
  return null;
}
window.obResolveActionableFormHotkey = obResolveActionableFormHotkey;

function obListRows() {
  return Array.prototype.slice.call(document.querySelectorAll('[data-ob-list-row]')).filter(obElementVisible);
}

function obListFocusedRow() {
  var active = document.activeElement;
  var row = active && active.closest ? active.closest('[data-ob-list-row]') : null;
  return row && document.contains(row) && obElementVisible(row) ? row : null;
}

function obListCurrentRow() {
  return obListFocusedRow() || listSel();
}

function obListCanMarkDelete(row) {
  var cfg = obListConfig();
  return !!(row && row.dataset && cfg.canDelete === true &&
    row.dataset.predefined !== '1' && String(row.dataset.markUrl || '').trim());
}

function obHandleListDeleteShortcut(e) {
  if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || obIsInteractiveTarget(e.target)) return;
  if (obHasBlockingModal() || e.key !== 'Delete') return;
  var sel = obListCurrentRow();
  // Fail closed до preventDefault/confirm/network: предопределённая строка,
  // отсутствующее право или пустой endpoint не должны даже открыть confirm.
  if (!obListCanMarkDelete(sel)) return;
  e.preventDefault();
  if (listSel() !== sel) listSetSel(sel);
  listSubmit(sel.dataset.markUrl, obListLabel('markDeleteConfirm', 'Пометить на удаление?'));
}

function obInitListFocusSelection() {
  if (window.__obListFocusSelection) return;
  window.__obListFocusSelection = true;
  // Строка сама является focusable option. Когда пользователь попал на неё
  // клавишей Tab, фокус сразу становится тем же текущим элементом, что и клик:
  // Enter/F2 работают без предварительной стрелки, а ArrowDown идёт ко второй
  // строке, а не повторно выбирает первую.
  document.addEventListener('focusin', function (e) {
    if (!e.target || !e.target.closest) return;
    var row = e.target.closest('[data-ob-list-row]');
    if (row && document.contains(row) && obElementVisible(row) && listSel() !== row) listSetSel(row);
  });
}

function obListMoveCursor(delta) {
  var rows = obListRows();
  if (!rows.length) return false;
  // Читаем через listSel(), пишем через listSetSel(): после живого обновления
  // списка узел прежней строки мог быть отцеплен от документа, а сама смена
  // выделения обязана пройти через единственную точку (она же гасит/включает
  // кнопку «Действия»).
  var cur = obListCurrentRow();
  var idx = cur ? rows.indexOf(cur) : -1;
  var next = idx < 0 ? (delta > 0 ? 0 : rows.length - 1) : idx + delta;
  if (next < 0 || next >= rows.length) return true; // упёрлись в край — клавишу всё равно съедаем
  listSetSel(rows[next], { focus: true });
  if (rows[next].scrollIntoView) rows[next].scrollIntoView({ block: 'nearest' });
  return true;
}

function obHasActionableHotkey(key) {
  return !!obResolveActionableFormHotkey(key);
}

function obDOMTableFromTarget(el) {
  return el && el.closest ? el.closest('table[data-ob-dom-table]') : null;
}

function obSlickGridFromTarget(el) {
  return el && el.closest ? el.closest('.ob-grid[data-sg-tp]') : null;
}

function obDOMTableReadOnly(table) {
  // Fail closed: a DOM table is writable only when the server rendered an
  // explicit marker. This also covers a missing CanWrite value safely.
  return !table || table.getAttribute('data-ob-readonly') !== '0';
}

function obDOMSetCurrentRow(table, row, focus) {
  if (!table) return;
  var body = table.tBodies && table.tBodies[0];
  if (!body) return;
  if (row && row.parentElement !== body) row = null;
  var tabRow = row || (body.rows.length ? body.rows[0] : null);
  Array.prototype.forEach.call(body.rows, function (item) {
    item.setAttribute('aria-selected', item === row ? 'true' : 'false');
    item.setAttribute('tabindex', item === tabRow ? '0' : '-1');
  });
  table._obCurrentRow = row || null;
  window._obActiveDOMTable = table;
  window._obActiveGridName = '';
  if (row && focus && row.focus) {
    try { row.focus({ preventScroll: true }); } catch (_) { row.focus(); }
  }
}
window.obDOMSetCurrentRow = obDOMSetCurrentRow;

function obDOMPrepareRow(table, row) {
  if (!table || !row) return;
  if (!row.hasAttribute('tabindex')) row.setAttribute('tabindex', '-1');
  if (!row.hasAttribute('aria-selected')) row.setAttribute('aria-selected', 'false');
  if (obDOMTableReadOnly(table)) {
    row.querySelectorAll('input,select,textarea,button').forEach(function (control) {
      if (control.hasAttribute && (control.hasAttribute('data-ob-ref-current') || control.hasAttribute('data-ob-ref-current-self'))) return;
      control.disabled = true;
    });
  }
}
window.obDOMPrepareRow = obDOMPrepareRow;

function obDOMActiveTable(target) {
  var source = target || document.activeElement;
  var direct = obDOMTableFromTarget(source);
  if (direct) {
    // A concrete DOM-table context supersedes every previously active
    // SlickGrid, even when this table is currently hidden or detached.
    window._obActiveGridName = '';
    if (!document.contains(direct) || !obElementVisible(direct)) {
      if (window._obActiveDOMTable === direct) window._obActiveDOMTable = null;
      return null;
    }
    window._obActiveDOMTable = direct;
    return direct;
  }
  // A concrete SlickGrid target is authoritative. Never apply a shortcut to
  // an unrelated DOM table merely because it was active earlier.
  if (obSlickGridFromTarget(source)) {
    window._obActiveDOMTable = null;
    return null;
  }
  var remembered = window._obActiveDOMTable;
  if (remembered && document.contains(remembered) && obElementVisible(remembered)) return remembered;
  window._obActiveDOMTable = null;
  return null;
}

function obDOMCurrentRow(table) {
  if (!table || !table.tBodies || !table.tBodies[0]) return null;
  var body = table.tBodies[0];
  var active = document.activeElement;
  var direct = active && active.closest ? active.closest('tr') : null;
  if (direct && direct.parentElement === body) return direct;
  if (table._obCurrentRow && table._obCurrentRow.parentElement === body) return table._obCurrentRow;
  var selected = table.querySelector('tbody ._tp-sel:checked');
  var selectedRow = selected && selected.closest ? selected.closest('tr') : null;
  return selectedRow && selectedRow.parentElement === body ? selectedRow : null;
}

function obDOMCommit(table) {
  var active = document.activeElement;
  if (!active || !table.contains(active) || !/^(INPUT|SELECT|TEXTAREA)$/.test(active.tagName || '')) return true;
  if (active.disabled) return true;
  if (active.checkValidity && !active.checkValidity()) {
    if (active.reportValidity) active.reportValidity();
    return false;
  }
  if (active.blur) active.blur();
  return true;
}

function obDOMReindex(table) {
  if (!table || !table.tBodies || !table.tBodies[0]) return;
  var tpName = table.getAttribute('data-ob-dom-table') || '';
  var prefix = 'tp.' + tpName + '.';
  Array.prototype.forEach.call(table.tBodies[0].rows, function (row, index) {
    row.querySelectorAll('[name]').forEach(function (control) {
      var name = control.getAttribute('name') || '';
      if (name.indexOf(prefix) !== 0) return;
      var suffix = name.slice(prefix.length);
      var dot = suffix.indexOf('.');
      if (dot >= 0) control.setAttribute('name', prefix + index + suffix.slice(dot));
    });
  });
}
window.obDOMReindex = obDOMReindex;

function obDOMRefreshTotals(table) {
  if (typeof recalcTpTotals !== 'function') return;
  var number = table.querySelector('tbody [data-tp-num]');
  if (number) recalcTpTotals(number);
}

function obDOMNotifyMutation(table, kind) {
  if (!table || window._obDOMDeferRowEvent) return;
  var attr = kind === 'add' ? 'data-ob-rowadd' : 'data-ob-rowdel';
  if (table.getAttribute(attr) !== '1' || typeof window.obFire !== 'function') return;
  var element = table.getAttribute('data-ob-element') || table.getAttribute('data-ob-dom-table') || '';
  var tpName = table.getAttribute('data-ob-dom-table') || '';
  window.obFire(element, kind === 'add' ? 'ПриДобавленииСтроки' : 'ПриУдаленииСтроки', { _tp: tpName });
}
window.obDOMNotifyMutation = obDOMNotifyMutation;

function obDOMAddButton(table) {
  var tpName = table.getAttribute('data-ob-dom-table') || '';
  var buttons = document.querySelectorAll('[data-ob-add-tp-row],[data-ob-add-tp]');
  for (var i = 0; i < buttons.length; i++) {
    var owner = buttons[i].getAttribute('data-tp-name') || buttons[i].getAttribute('data-ob-add-tp') || '';
    // Duplicate readonly/writable placements share the same logical table
    // name. The readonly add button is disabled and must not shadow the sole
    // writable control merely because it appears first in DOM order.
    if (owner === tpName && !buttons[i].disabled) return buttons[i];
  }
  return null;
}

function obDOMFinishMutation(table, row, focusControl) {
  obDOMReindex(table);
  obDOMRefreshTotals(table);
  window._obFormDirty = true;
  var body = table && table.tBodies && table.tBodies[0];
  if (!row || !body || row.parentElement !== body) {
    obDOMSetCurrentRow(table, null, false);
    return;
  }
  obDOMPrepareRow(table, row);
  obDOMSetCurrentRow(table, row, !focusControl);
  if (focusControl) {
    var control = row.querySelector('input:not([type="hidden"]):not(:disabled),select:not(:disabled),textarea:not(:disabled)');
    if (control && control.focus) control.focus();
    else obDOMSetCurrentRow(table, row, true);
  }
}
window.obDOMFinishMutation = obDOMFinishMutation;

function obDOMAddRow(table) {
  if (!obDOMCommit(table)) return;
  var button = obDOMAddButton(table);
  var body = table.tBodies && table.tBodies[0];
  if (!button || button.disabled || !body) return;
  var count = body.rows.length;
  button.click();
  if (body.rows.length <= count) return;
  obDOMFinishMutation(table, body.rows[body.rows.length - 1], true);
}

function obDOMCopyControlValue(source, target) {
  if (source.type === 'checkbox' || source.type === 'radio') target.checked = source.checked;
  else target.value = source.value;
}

function obDOMCopyRow(table) {
  if (!obDOMCommit(table)) return;
  var source = obDOMCurrentRow(table);
  var body = table.tBodies && table.tBodies[0];
  var button = obDOMAddButton(table);
  if (!source || !body || !button || button.disabled) return;
  var count = body.rows.length;
  var wasDeferred = window._obDOMDeferRowEvent;
  window._obDOMDeferRowEvent = true;
  try { button.click(); } finally { window._obDOMDeferRowEvent = wasDeferred; }
  if (body.rows.length <= count) return;
  var copy = body.rows[body.rows.length - 1];
  var from = source.querySelectorAll('input[name],select[name],textarea[name]');
  var to = copy.querySelectorAll('input[name],select[name],textarea[name]');
  for (var i = 0; i < from.length && i < to.length; i++) obDOMCopyControlValue(from[i], to[i]);
  copy.className = source.className;
  if (source.cells && copy.cells) {
    for (var cell = 0; cell < source.cells.length && cell < copy.cells.length; cell++) copy.cells[cell].className = source.cells[cell].className;
  }
  if (source.nextSibling) body.insertBefore(copy, source.nextSibling);
  obDOMFinishMutation(table, copy, true);
  obDOMNotifyMutation(table, 'add');
}

function obDOMMoveRow(table, delta) {
  if (!obDOMCommit(table)) return;
  var row = obDOMCurrentRow(table);
  var body = table.tBodies && table.tBodies[0];
  if (!row || !body) return;
  var rows = Array.prototype.slice.call(body.rows);
  var index = rows.indexOf(row);
  var next = index + delta;
  if (index < 0 || next < 0 || next >= rows.length) return;
  if (delta < 0) body.insertBefore(row, rows[next]);
  else body.insertBefore(row, rows[next].nextSibling);
  obDOMFinishMutation(table, row, false);
}

function obDOMDeleteRows(table) {
  if (!obDOMCommit(table)) return;
  var body = table.tBodies && table.tBodies[0];
  if (!body) return;
  var rows = Array.prototype.slice.call(body.rows);
  var checked = Array.prototype.slice.call(body.querySelectorAll('._tp-sel:checked')).map(function (item) { return item.closest('tr'); }).filter(function (row) {
    return row && row.parentElement === body;
  });
  var current = obDOMCurrentRow(table);
  var remove = checked.length ? checked : (current ? [current] : []);
  if (!remove.length) return;
  var currentIndex = current ? rows.indexOf(current) : rows.indexOf(remove[0]);
  var wasDeferred = window._obDOMDeferRowEvent;
  window._obDOMDeferRowEvent = true;
  try {
    remove.forEach(function (row) {
      var button = row.querySelector('[data-ob-remove-row],.del-btn');
      if (button && button.click) button.click();
      else row.remove();
    });
  } finally { window._obDOMDeferRowEvent = wasDeferred; }
  var nextRows = Array.prototype.slice.call(body.rows);
  var next = nextRows.length ? nextRows[Math.min(Math.max(currentIndex, 0), nextRows.length - 1)] : null;
  obDOMFinishMutation(table, next, false);
  obDOMNotifyMutation(table, 'delete');
}

function obHandleDOMTableShortcut(e) {
  var direct = obDOMTableFromTarget(e.target);
  // Remembered table shortcuts are convenient after its focus sink, but never
  // steal keys from an unrelated interactive control.
  if (!direct && (obSlickGridFromTarget(e.target) || obIsInteractiveTarget(e.target))) return false;
  if (direct && e.target && e.target.closest && e.target.closest('a[href],button,summary,[contenteditable]:not([contenteditable="false"])')) return false;
  var table = obDOMActiveTable(e.target);
  if (!table || obDOMTableReadOnly(table)) return false;
  var action = null;
  if (!e.ctrlKey && e.key === 'Insert') action = function () { obDOMAddRow(table); };
  else if (!e.ctrlKey && e.key === 'F9' && !obHasActionableHotkey('F9')) action = function () { obDOMCopyRow(table); };
  else if (e.ctrlKey && e.key === 'ArrowUp') action = function () { obDOMMoveRow(table, -1); };
  else if (e.ctrlKey && e.key === 'ArrowDown') action = function () { obDOMMoveRow(table, 1); };
  else if (!e.ctrlKey && e.key === 'Delete' && (!obIsTypingTarget(e.target) || (e.target.matches && e.target.matches('._tp-sel')))) action = function () { obDOMDeleteRows(table); };
  if (!action) return false;
  e.preventDefault();
  e.stopPropagation();
  action();
  return true;
}

function obInitDOMTables() {
  document.querySelectorAll('table[data-ob-dom-table]').forEach(function (table) {
    var body = table.tBodies && table.tBodies[0];
    if (!body) return;
    Array.prototype.forEach.call(body.rows, function (row) { obDOMPrepareRow(table, row); });
    if (body.rows.length) body.rows[0].setAttribute('tabindex', '0');
  });
  function remember(e) {
    var table = obDOMTableFromTarget(e.target);
    if (!table) return;
    var row = e.target.closest && e.target.closest('tr');
    if (row && row.parentElement === (table.tBodies && table.tBodies[0])) obDOMSetCurrentRow(table, row, false);
    else obDOMSetCurrentRow(table, null, false);
  }
  document.addEventListener('mousedown', remember, true);
  document.addEventListener('focusin', remember);
}

function obInitKeyboardShortcuts() {
  if (window.__obKeyShortcuts) return;
  window.__obKeyShortcuts = true;
  obInitListFocusSelection();
  document.addEventListener('keydown', function (e) {
    if (e.defaultPrevented || e.altKey || e.metaKey || e.shiftKey) return;
    // Модальный подбор забирает клавиатуру себе.
    if (obHasBlockingModal()) return;

    if (obHandleDOMTableShortcut(e)) return;

    if (e.ctrlKey) {
      if (e.key === 'Enter') {
        var go = obFormActionButton(['post_and_close', '']);
        if (go) { e.preventDefault(); go.click(); }
        return;
      }
      if (e.code === 'KeyS') {
        var write = obFormActionButton(['']);
        if (write) { e.preventDefault(); write.click(); }
        return;
      }
      if (e.code === 'KeyF') {
        var q = document.getElementById('ob-list-config') && document.getElementById('ob-list-search');
        if (q) { e.preventDefault(); q.focus(); q.select(); }
      }
      return;
    }

    // Ввод текста важнее списковых клавиш, включая Insert: выбранная ранее
    // строка списка не должна превращать Insert в действие над данными,
    // пока пользователь редактирует поиск или другое поле.
    if (obIsInteractiveTarget(e.target)) return;
    if (e.key === 'Insert') {
      var create = document.querySelector('[data-ob-list-create]');
      if (create) { e.preventDefault(); create.click(); }
      return;
    }
    if (!obListRows().length) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      if (obListMoveCursor(e.key === 'ArrowDown' ? 1 : -1)) e.preventDefault();
      return;
    }
    var sel = obListCurrentRow();
    if ((e.key === 'Enter' || e.key === 'F2') && sel) {
      e.preventDefault();
      if (listSel() !== sel) listSetSel(sel);
      if (e.key === 'F2') listOpen(sel.dataset.openUrl);
      else listActivateRow(sel);
      return;
    }
    // F9 в списке — «Создать копированием», как в 1С. В форме та же клавиша
    // копирует строку ТЧ, но туда обработчик не доходит: obHandleDOMTableShortcut
    // выше забирает F9 себе, когда активна таблица ТЧ.
    if (e.key === 'F9' && sel && sel.dataset.copyUrl) {
      e.preventDefault();
      if (listSel() !== sel) listSetSel(sel);
      listOpen(sel.dataset.copyUrl);
    }
  });
  document.addEventListener('keydown', obHandleListDeleteShortcut);
}

function initTreeToggle(btn) {
  btn.addEventListener('click', function (e) {
    e.stopPropagation();
    toggleTreeNode(btn);
  });
}

function toggleTreeNode(btn) {
  var tr = btn.closest('tr');
  var fid = btn.dataset.folderId;
  var expanded = btn.getAttribute('data-expanded') === '1';
  if (expanded) {
    var selected = listSel();
    treeSetVisible(fid, false);
    btn.setAttribute('data-expanded', '0');
    btn.textContent = '▶';
    if (selected && !obElementVisible(selected)) listSetSel(tr, { focus: true });
    return;
  }
  if (btn.getAttribute('data-loaded') === '1') {
    treeSetVisible(fid, true);
    btn.setAttribute('data-expanded', '1');
    btn.textContent = '▼';
    return;
  }
  btn.disabled = true;
  btn.textContent = '…';
  var cfg = obListConfig();
  var depth = (tr && tr.dataset.treeDepth) || '0';
  var url = '/ui/_tree-children/' + encodeURIComponent(cfg.treeEntity || '') + '?parent=' + encodeURIComponent(fid) + '&depth=' + encodeURIComponent(depth);
  if (cfg.subsystem) url += '&subsystem=' + encodeURIComponent(cfg.subsystem);
  fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
    .then(function (resp) {
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      return resp.json();
    })
    .then(function (data) {
      var rows = (data && data.rows) || [];
      insertTreeRows(tr, rows);
      btn.setAttribute('data-loaded', '1');
      btn.setAttribute('data-expanded', '1');
      btn.textContent = rows.length ? '▼' : '•';
    })
    .catch(function () {
      btn.textContent = '▶';
    })
    .finally(function () { btn.disabled = false; });
}

function treeSetVisible(parentId, visible) {
  document.querySelectorAll('[data-tree-parent="' + parentId + '"]').forEach(function (row) {
    row.style.display = visible ? '' : 'none';
    var childId = row.dataset.treeId;
    if (childId) {
      var expanded = row.querySelector('.tree-toggle[data-expanded="1"]') !== null;
      treeSetVisible(childId, visible && (row.dataset.isFolder !== '1' || expanded));
    }
  });
}

function insertTreeRows(parentTr, rows) {
  if (!parentTr || !rows.length) return;
  var tbody = parentTr.parentNode;
  var parentDepth = parseInt(parentTr.dataset.treeDepth || '0', 10);
  var before = parentTr.nextElementSibling;
  while (before && parseInt(before.dataset.treeDepth || '0', 10) > parentDepth) before = before.nextElementSibling;
  rows.forEach(function (row) {
    var tr = makeTreeRow(row);
    tbody.insertBefore(tr, before);
  });
}

function makeTreeRow(row) {
  var tr = document.createElement('tr');
  tr.style.cursor = 'pointer';
  if (row.marked) {
    tr.style.opacity = '0.45';
    tr.style.textDecoration = 'line-through';
  }
  tr.dataset.treeId = row.id || '';
  tr.dataset.treeDepth = String(row.depth || 0);
  tr.dataset.treeParent = row.parent_id || '';
  tr.dataset.predefined = row.predefined ? '1' : '';
  tr.dataset.isFolder = row.is_folder ? '1' : '';
  tr.dataset.folderUrl = row.folder_url || '';
  tr.dataset.markUrl = row.mark_url || '';
  tr.dataset.delUrl = row.delete_url || '';
  tr.dataset.posted = row.posted ? '1' : '';
  tr.dataset.marked = row.marked ? '1' : '';
  tr.dataset.unpostUrl = row.unpost_url || '';
  tr.dataset.unmarkUrl = row.unmark_url || '';
  tr.dataset.activityEnabled = row.activity_enabled ? '1' : '';
  tr.dataset.activityInactive = row.activity_inactive ? '1' : '';
  tr.dataset.activityHideUrl = row.activity_hide_url || '';
  tr.dataset.activityShowUrl = row.activity_show_url || '';
  tr.dataset.openUrl = row.open_url || '';
  tr.dataset.copyUrl = row.copy_url || '';
  tr.dataset.obDetailUrl = row.detail_url || '';
  tr.setAttribute('data-ob-list-row', '');
  tr.setAttribute('tabindex', '-1');
  tr.setAttribute('aria-selected', 'false');
  var rowShortcuts = 'ArrowUp ArrowDown Enter F2';
  if (row.copy_url) rowShortcuts += ' F9';
  if (obListConfig().canDelete === true && !row.predefined && row.mark_url) rowShortcuts += ' Delete';
  tr.setAttribute('aria-keyshortcuts', rowShortcuts);
  var cells = row.cells || [];
  var treeCell = row.tree_cell || 0;
  for (var i = 0; i < cells.length; i++) {
    var td = document.createElement('td');
    if (i === treeCell) {
      var indent = document.createElement('span');
      indent.style.display = 'inline-block';
      indent.style.width = ((row.depth || 0) * 20) + 'px';
      td.appendChild(indent);
      if (row.is_folder) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'tree-toggle';
        btn.setAttribute('data-folder-id', row.id || '');
        btn.setAttribute('data-expanded', '0');
        btn.setAttribute('data-loaded', '0');
        btn.title = obListLabel('collapseExpand', 'Свернуть/Развернуть');
        btn.style.cssText = 'background:none;border:none;cursor:pointer;padding:0 2px;font-size:13px';
        btn.textContent = '▶';
        initTreeToggle(btn);
        td.appendChild(btn);
        td.appendChild(document.createTextNode(' 📁 '));
      } else {
        td.appendChild(document.createTextNode('📄 '));
      }
      td.appendChild(document.createTextNode(cells[i] || ''));
      if (row.predefined) {
        var star = document.createElement('span');
        star.title = obListLabel('predefined', 'Предопределённый');
        star.style.cssText = 'color:#f59e0b;font-size:11px';
        star.textContent = ' ★';
        td.appendChild(star);
      }
    } else {
      td.textContent = cells[i] || '';
    }
    tr.appendChild(td);
  }
  var action = document.createElement('td');
  var a = document.createElement('a');
  a.className = row.is_folder ? 'btn btn-sm btn-secondary' : 'btn btn-sm btn-primary';
  a.href = row.is_folder ? (row.folder_url || '#') : (row.open_url || '#');
  a.textContent = row.is_folder ? obListLabel('enter', '▶ Войти') : obListLabel('open', 'Открыть');
  action.appendChild(a);
  tr.appendChild(action);
  return tr;
}

function listMenuItems(tr) {
  var cfg = obListConfig();
  var labels = cfg.labels || {};
  var isPredefined = tr.dataset.predefined === '1';
  var isFolder = tr.dataset.isFolder === '1';
  var items = [];
  if (isFolder) {
    items.push({ label: labels.enterGroup || '▶ Войти в группу', fn: function () { window.location.href = tr.dataset.folderUrl; } });
    items.push({ label: labels.edit || 'Редактировать', fn: function () { listOpen(tr.dataset.openUrl); } });
  } else {
    items.push({ label: labels.open || 'Открыть', fn: function () { listOpen(tr.dataset.openUrl); } });
  }
  // «Скопировать» (F9): открывает форму создания, заполненную значениями строки.
  // Пустой data-copy-url = нет права записи, пункт не показываем.
  if (tr.dataset.copyUrl) {
    items.push({ label: labels.copy || 'Скопировать', fn: function () { listOpen(tr.dataset.copyUrl); } });
  }
  if (cfg.canWrite && tr.dataset.activityEnabled === '1') {
    if (tr.dataset.activityInactive === '1') {
      items.push({ label: labels.activityShow || 'Вернуть в выбор', fn: function () { listSubmit(tr.dataset.activityShowUrl, labels.activityShowConfirm || 'Вернуть в выбор?'); } });
    } else {
      items.push({ label: labels.activityHide || 'Скрыть из выбора', fn: function () { listSubmit(tr.dataset.activityHideUrl, labels.activityHideConfirm || 'Скрыть из выбора?'); } });
    }
  }
  if (cfg.canDelete) {
    if (!isPredefined) {
      items.push({ label: labels.markDelete || 'Пометить на удаление', danger: true, fn: function () { listSubmit(tr.dataset.markUrl, labels.markDeleteConfirm || 'Пометить на удаление?'); } });
    } else {
      items.push({ label: labels.predefinedNoDelete || 'Предопределённый — нельзя удалить', disabled: true });
    }
  }
  if (cfg.canUnpost && tr.dataset.posted === '1') {
    items.push({ label: labels.unpost || 'Отменить проведение', fn: function () { listSubmit(tr.dataset.unpostUrl, labels.unpostConfirm || 'Отменить проведение?'); } });
  }
  if (cfg.canDelete && tr.dataset.marked === '1' && !isPredefined) {
    items.push({ label: labels.unmarkDelete || 'Снять пометку на удаление', fn: function () { listSubmit(tr.dataset.unmarkUrl, labels.unmarkDeleteConfirm || 'Снять пометку на удаление?'); } });
  }
  if (cfg.isAdmin && !isPredefined) {
    items.push({ label: labels.deleteForever || 'Удалить навсегда', danger: true, fn: function () { listSubmit(tr.dataset.delUrl, labels.deleteForeverConfirm || 'Удалить запись навсегда?'); } });
  }
  return items;
}

function showListMenu(items, x, y) {
  var old = document.getElementById('_lctx');
  if (old) old.remove();
  var m = document.createElement('div');
  m.id = '_lctx';
  m.style.cssText = 'position:fixed;z-index:999;background:#fff;border:1px solid #c8d0de;border-radius:6px;box-shadow:0 4px 16px rgba(0,0,0,.18);padding:4px 0;min-width:190px;font-size:13px';
  m.style.left = x + 'px';
  m.style.top = y + 'px';
  items.forEach(function (item) {
    var mi = document.createElement('div');
    mi.textContent = item.label;
    if (item.hint) {
      mi.style.cssText = 'padding:7px 14px;margin-bottom:4px;border-bottom:1px solid #e2e8f0;color:#64748b;font-size:12px;cursor:default';
    } else if (item.disabled) {
      mi.style.cssText = 'padding:8px 14px;color:#94a3b8;cursor:default;font-style:italic';
    } else {
      mi.style.cssText = 'padding:8px 14px;cursor:pointer' + (item.danger ? ';color:#dc2626' : '');
      mi.onmouseenter = function () { mi.style.background = '#f8fafc'; };
      mi.onmouseleave = function () { mi.style.background = ''; };
      mi.onclick = function () { m.remove(); item.fn(); };
    }
    m.appendChild(mi);
  });
  document.body.appendChild(m);
  setTimeout(function () {
    document.addEventListener('click', function h() {
      m.remove();
      document.removeEventListener('click', h);
    }, { once: true });
  }, 0);
}

function listCtxMenu(e, tr) {
  if (e.target.closest('a,button')) return;
  e.preventDefault();
  listRowClick(e, tr);
  showListMenu(listMenuItems(tr), e.clientX, e.clientY);
}

// Меню для случая «строка не выбрана»: причина сверху, ниже — те же команды
// неактивными. Это дешевле модального alert() (не требует «ОК» на предсказуемое
// состояние) и заодно показывает, что кнопка вообще умеет.
function listMenuNoSel() {
  var cfg = obListConfig();
  var labels = cfg.labels || {};
  var items = [{ label: labels.selectRowFirst || 'Сначала выберите строку списка', hint: true }];
  items.push({ label: labels.open || 'Открыть', disabled: true });
  if (cfg.canDelete) items.push({ label: labels.markDelete || 'Пометить на удаление', disabled: true });
  if (cfg.canUnpost) items.push({ label: labels.unpost || 'Отменить проведение', disabled: true });
  return items;
}

function listActionsBtnClick(e, btn) {
  e.preventDefault();
  var sel = listSel();
  var r = (btn || e.currentTarget).getBoundingClientRect();
  showListMenu(sel ? listMenuItems(sel) : listMenuNoSel(), r.left, r.bottom);
}

function obInitListDelegates() {
  if (window.__obListDelegates) return;
  window.__obListDelegates = true;
  document.addEventListener('click', function (e) {
    if (!e.target.closest) return;
    var actions = e.target.closest('[data-ob-list-actions]');
    if (actions) {
      listActionsBtnClick(e, actions);
      return;
    }
    var picker = e.target.closest('[data-ob-ref-picker]');
    if (picker) {
      e.preventDefault();
      openRefPicker(picker.getAttribute('data-ob-ref-picker') || '');
      return;
    }
    var row = e.target.closest('[data-ob-list-row]');
    if (row) listRowClick(e, row);
  });
  document.addEventListener('dblclick', function (e) {
    if (!e.target.closest) return;
    var row = e.target.closest('[data-ob-list-row]');
    if (row) listRowDblClick(e, row);
  });
  document.addEventListener('contextmenu', function (e) {
    if (!e.target.closest) return;
    var row = e.target.closest('[data-ob-list-row]');
    if (row) listCtxMenu(e, row);
  });
  document.addEventListener('input', function (e) {
    if (!e.target.closest) return;
    var input = e.target.closest('[data-ob-auto-submit]');
    if (!input || !input.form) return;
    var delay = parseInt(input.getAttribute('data-ob-auto-submit') || '320', 10);
    if (!Number.isFinite(delay) || delay < 0) delay = 320;
    clearTimeout(input._obAutoSubmitTimer);
    input._obAutoSubmitTimer = setTimeout(function () {
      var form = input.form;
      if (!form) return;
      // Named controls can shadow form.submit (for example name="submit").
      // Call the native prototype method when available so query/form fields
      // cannot disable the debounced search submission.
      var proto = window.HTMLFormElement && window.HTMLFormElement.prototype;
      var nativeSubmit = proto && typeof proto.submit === 'function' ? proto.submit : null;
      if (nativeSubmit) nativeSubmit.call(form);
      else if (typeof form.submit === 'function') form.submit();
    }, delay);
  });
}

function listSubmit(url, msg) {
  if (!url) return;
  if (confirm(msg)) {
    var f = document.createElement('form');
    f.method = 'POST';
    f.action = url;
    document.body.appendChild(f);
    f.submit();
  }
}

function obInitFeed() {
  var more = document.getElementById('feed-more');
  if (!more) return;
  var loading = false;
  var done = false;
  function stop() {
    done = true;
    if (more && more.parentNode) more.parentNode.removeChild(more);
  }
  function loadNext() {
    if (loading || done) return;
    var n = parseInt(more.getAttribute('data-next'), 10);
    var pages = parseInt(more.getAttribute('data-pages'), 10);
    if (!n || n > pages) {
      stop();
      return;
    }
    var sel = more.getAttribute('data-container');
    var c = document.querySelector(sel);
    if (!c) {
      stop();
      return;
    }
    loading = true;
    var sp = new URLSearchParams(window.location.search);
    sp.set('page', n);
    sp.set('lm', 'feed');
    fetch(window.location.pathname + '?' + sp.toString(), { credentials: 'same-origin' })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, 'text/html');
        var items = doc.querySelectorAll(sel + ' > ' + more.getAttribute('data-item'));
        if (!items.length) {
          stop();
          return;
        }
        items.forEach(function (el) { c.appendChild(document.importNode(el, true)); });
        var loaded = document.getElementById('feed-loaded');
        if (loaded) loaded.textContent = c.children.length;
        n++;
        more.setAttribute('data-next', n);
        loading = false;
        if (n > pages) {
          stop();
          return;
        }
        var rect = more.getBoundingClientRect();
        if (rect.top < (window.innerHeight || document.documentElement.clientHeight) + 300) loadNext();
      })
      .catch(function () { loading = false; });
  }
  more.addEventListener('click', function (e) {
    var a = e.target.closest('a');
    if (a) {
      e.preventDefault();
      loadNext();
    }
  });
  if ('IntersectionObserver' in window) {
    new IntersectionObserver(function (ents) {
      ents.forEach(function (en) { if (en.isIntersecting) loadNext(); });
    }, { rootMargin: '300px' }).observe(more);
  }
}

obReady(function () {
  obInitListDelegates();
  obInitDOMTables();
  obInitKeyboardShortcuts();
  var firstListRow = obListRows()[0];
  if (firstListRow) firstListRow.setAttribute('tabindex', '0');
  listSyncActionsBtn();
  document.querySelectorAll('.tree-toggle').forEach(initTreeToggle);
  obInitFeed();
  initDetailPanel();
});

// Стили плавающих виджетов — ИИ-помощника (план 51, F3) и панели сообщений
// («Окно сообщений» как в 1С). Разметку обоих строит сам ui.js, поэтому CSS
// живёт здесь же: страницы с собственным <head> без общего стиля приложения
// (админские «Система» → Пользователи/Обмен/…) иначе показывали голую
// разметку виджетов внизу страницы.
(function () {
  if (document.getElementById('ob-widget-style')) return;
  var st = document.createElement('style');
  st.id = 'ob-widget-style';
  st.textContent =
    '#ob-ai-btn{position:fixed;right:18px;bottom:44px;z-index:320;width:48px;height:48px;border-radius:50%;background:#2563eb;color:#fff;border:none;cursor:pointer;font-size:22px;box-shadow:0 4px 14px rgba(37,99,235,.4)}' +
    '#ob-ai-btn:hover{background:#1d4ed8}' +
    '#ob-ai-panel{position:fixed;right:18px;bottom:44px;z-index:321;width:440px;max-width:calc(100vw - 24px);height:540px;max-height:calc(100vh - 80px);background:#fff;border:1px solid #cbd5e1;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,.22);display:none;flex-direction:column;overflow:hidden;font-family:system-ui,sans-serif}' +
    '#ob-ai-panel.open{display:flex}' +
    '#ob-ai-head{background:#2563eb;color:#fff;padding:10px 14px;display:flex;align-items:center;gap:8px;font-weight:600;font-size:14px}' +
    '#ob-ai-head .sp{flex:1}' +
    '#ob-ai-head button{background:none;border:none;color:#fff;cursor:pointer;font-size:18px;line-height:1}' +
    '#ob-ai-log{flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:10px;background:#f8fafc}' +
    '#ob-ai-log .m{max-width:85%;padding:8px 11px;border-radius:12px;font-size:13px;line-height:1.4;white-space:pre-wrap;word-break:break-word}' +
    '#ob-ai-log .m.u{align-self:flex-end;background:#2563eb;color:#fff;border-bottom-right-radius:3px}' +
    '#ob-ai-log .m.a{align-self:flex-start;background:#fff;border:1px solid #e2e8f0;color:#1e293b;border-bottom-left-radius:3px;white-space:normal;max-width:92%;overflow-x:auto}' +
    '#ob-ai-log .m.a p{margin:0 0 6px}' +
    '#ob-ai-log .m.a p:last-child{margin-bottom:0}' +
    '#ob-ai-log .m.a h1,#ob-ai-log .m.a h2,#ob-ai-log .m.a h3,#ob-ai-log .m.a h4{font-size:13px;font-weight:700;margin:8px 0 4px}' +
    '#ob-ai-log .m.a ul,#ob-ai-log .m.a ol{margin:4px 0;padding-left:20px}' +
    '#ob-ai-log .m.a li{margin:2px 0}' +
    '#ob-ai-log .m.a a{color:#2563eb;text-decoration:underline}' +
    '#ob-ai-log .m.a code{background:#f1f5f9;border-radius:3px;padding:1px 4px;font-family:ui-monospace,Consolas,monospace;font-size:12px}' +
    '#ob-ai-log .m.a pre{background:#f1f5f9;border-radius:6px;padding:8px;overflow-x:auto;margin:6px 0}' +
    '#ob-ai-log .m.a pre code{background:none;padding:0}' +
    // Таблица живёт в собственной скролл-обёртке .tw и держит естественную
    // ширину; ячейкам возвращаем word-break:normal — иначе наследованный от .m
    // break-word даёт min-width колонки в один символ и текст жмётся посимвольно.
    '#ob-ai-log .m.a .tw{overflow-x:auto;margin:6px 0}' +
    '#ob-ai-log .m.a table{border-collapse:collapse;margin:0;font-size:12px;width:max-content}' +
    '#ob-ai-log .m.a th,#ob-ai-log .m.a td{border:1px solid #e2e8f0;padding:4px 7px;text-align:left;vertical-align:top;word-break:normal;max-width:240px}' +
    '#ob-ai-log .m.a th{background:#f1f5f9;font-weight:600;white-space:nowrap}' +
    '#ob-ai-log .m.a tbody tr:nth-child(even){background:#f8fafc}' +
    '#ob-ai-log .m.err{align-self:stretch;background:#fef2f2;border:1px solid #fecaca;color:#b91c1c}' +
    '#ob-ai-log .hint{color:#94a3b8;font-size:12px;text-align:center;margin:auto 0}' +
    '#ob-ai-foot{border-top:1px solid #e2e8f0;padding:8px;display:flex;gap:6px;background:#fff}' +
    '#ob-ai-input{flex:1;resize:none;border:1px solid #cbd5e1;border-radius:8px;padding:8px;font-size:13px;font-family:inherit;max-height:90px}' +
    '#ob-ai-send{background:#2563eb;color:#fff;border:none;border-radius:8px;padding:0 14px;cursor:pointer;font-size:14px}' +
    '#ob-ai-send:disabled{opacity:.5;cursor:default}' +
    '#ob-ai-rs{position:absolute;left:0;top:0;bottom:0;width:6px;cursor:ew-resize;touch-action:none;z-index:2}' +
    '#ob-ai-rs:hover,#ob-ai-rs.drag{background:rgba(37,99,235,.25)}' +
    '#ob-ai-log .m.act{align-self:stretch;max-width:100%;background:#eef2ff;border:1px solid #c7d2fe;color:#1e293b;white-space:normal}' +
    '#ob-ai-log .m.act .lbl{white-space:pre-wrap;margin-bottom:8px}' +
    '#ob-ai-log .m.act .btns{display:flex;gap:8px}' +
    '#ob-ai-log .m.act .btns button{background:#2563eb;color:#fff;border:none;border-radius:6px;padding:6px 12px;cursor:pointer;font-size:13px}' +
    '#ob-ai-log .m.act .btns button.sec{background:#e2e8f0;color:#334155}' +
    '#ob-ai-log .m.act .btns button:disabled{opacity:.6;cursor:default}' +
    '#ob-ai-log .m.act .res{font-size:13px}' +
    '#ob-ai-log .m.act .res a{color:#2563eb;text-decoration:underline;cursor:pointer}' +
    '#ob-msg-bar{position:fixed;left:0;right:0;bottom:0;z-index:300;background:#fff;border-top:1px solid #cbd5e1;box-shadow:0 -2px 8px rgba(0,0,0,.08);font-family:system-ui,sans-serif;font-size:13px;color:#1e293b;transform:translateY(calc(100% - 30px));transition:transform .18s ease}' +
    '#ob-msg-bar.open{transform:translateY(0)}' +
    '#ob-msg-bar.hidden{display:none}' +
    '#ob-msg-head{height:30px;display:flex;align-items:center;padding:0 10px;cursor:pointer;background:#f1f5f9;user-select:none;gap:10px}' +
    '#ob-msg-head .ttl{font-weight:600;color:#334155;flex:1;display:flex;align-items:center;gap:8px}' +
    '#ob-msg-head .cnt{background:#ef4444;color:#fff;border-radius:10px;padding:1px 8px;font-size:11px;font-weight:700;min-width:18px;text-align:center;display:none}' +
    '#ob-msg-head .cnt.show{display:inline-block}' +
    '#ob-msg-head .arr{color:#64748b;font-size:11px;width:14px;text-align:center}' +
    '#ob-msg-bar.open #ob-msg-head .arr{transform:rotate(180deg)}' +
    '#ob-msg-head button{background:none;border:none;color:#64748b;cursor:pointer;font-size:12px;padding:4px 8px;border-radius:5px}' +
    '#ob-msg-head button:hover{background:#e2e8f0;color:#1e293b}' +
    '#ob-msg-list{max-height:200px;overflow-y:auto;padding:6px 0;background:#fff}' +
    '#ob-msg-list .it{padding:5px 14px;border-bottom:1px solid #f1f5f9;display:flex;gap:10px;align-items:flex-start;font-family:Consolas,monospace;font-size:12px;white-space:pre-wrap;word-break:break-word}' +
    '#ob-msg-list .it:last-child{border-bottom:none}' +
    '#ob-msg-list .it .t{color:#94a3b8;flex-shrink:0;font-size:11px;padding-top:1px}' +
    '#ob-msg-list .empty{padding:10px 14px;color:#94a3b8;font-style:italic}';
  (document.head || document.documentElement).appendChild(st);
})();

(function () {
  if (window.__obAiInit) return;
  window.__obAiInit = true;
  // Во вкладочной оболочке ui.js загружается и в верхнем окне, и внутри
  // каждой вкладки-iframe. Помощник принадлежит оболочке: если строить его во
  // фрейме, поверх кнопки оболочки появляется второй робот.
  if (window.__obEmbedded) return;
  function init() {
    if (document.getElementById('ob-ai-btn')) return;
    fetch('/ui/ai/enabled').then(function (r) { return r.json(); }).then(function (d) {
      if (d && d.enabled) buildUI();
    }).catch(function () {});
  }
  function buildUI() {
    var btn = document.createElement('button');
    btn.id = 'ob-ai-btn';
    btn.title = 'ИИ-помощник';
    btn.textContent = '🤖';
    var panel = document.createElement('div');
    panel.id = 'ob-ai-panel';
    panel.innerHTML = '<div id="ob-ai-rs" title="Потяните, чтобы изменить ширину; двойной клик — сброс"></div>' +
      '<div id="ob-ai-head"><span>🤖 ИИ-помощник</span><span class="sp"></span><button type="button" id="ob-ai-close" title="Закрыть">×</button></div>' +
      '<div id="ob-ai-log"><div class="hint">Спросите про данные, отчёт или как что-то сделать.</div></div>' +
      '<div id="ob-ai-foot"><textarea id="ob-ai-input" rows="1" placeholder="Ваш вопрос…"></textarea><button id="ob-ai-send" type="button" title="Отправить">▶</button></div>';
    document.body.appendChild(btn);
    document.body.appendChild(panel);
    var log = document.getElementById('ob-ai-log');
    var input = document.getElementById('ob-ai-input');
    var send = document.getElementById('ob-ai-send');
    var history = [];
    var busy = false;
    // Служебные заметки для модели (подтверждение/отмена действий) — уходят
    // префиксом следующего сообщения пользователя, в журнале чата не видны.
    var pendingNote = '';
    // Изменяемая ширина панели: ручка на левой кромке (панель прижата к правому
    // краю, тянуть естественно влево). Ширина живёт в localStorage.
    (function () {
      var rs = document.getElementById('ob-ai-rs');
      var saved = parseInt(localStorage.getItem('obAiW'), 10);
      if (saved) panel.style.width = saved + 'px';
      function clampW(w) {
        var max = Math.min(Math.round(window.innerWidth * 0.9), window.innerWidth - 24);
        return Math.max(320, Math.min(max, w));
      }
      rs.addEventListener('pointerdown', function (e) {
        e.preventDefault();
        rs.setPointerCapture(e.pointerId);
        rs.classList.add('drag');
        function move(ev) {
          panel.style.width = clampW(window.innerWidth - 18 - ev.clientX) + 'px';
        }
        function up() {
          rs.removeEventListener('pointermove', move);
          rs.removeEventListener('pointerup', up);
          rs.removeEventListener('pointercancel', up);
          rs.classList.remove('drag');
          var w = parseInt(panel.style.width, 10);
          if (w) localStorage.setItem('obAiW', String(w));
        }
        rs.addEventListener('pointermove', move);
        rs.addEventListener('pointerup', up);
        rs.addEventListener('pointercancel', up);
      });
      rs.addEventListener('dblclick', function () {
        panel.style.width = '';
        localStorage.removeItem('obAiW');
      });
    })();
    function open() {
      panel.classList.add('open');
      btn.style.display = 'none';
      input.focus();
    }
    function close() {
      panel.classList.remove('open');
      btn.style.display = '';
    }
    btn.addEventListener('click', open);
    document.getElementById('ob-ai-close').addEventListener('click', close);
    // mdToHtml — безопасный мини-рендер Markdown для ответов ассистента. Сначала
    // экранируем HTML (ответ модели недоверенный → любой <тег> становится
    // текстом), затем разворачиваем ограниченный набор разметки: таблицы GFM,
    // заголовки, списки, код, ссылки (только http/https/относительные),
    // жирный/курсив. Сырой HTML из ответа наружу не попадает.
    function mdToHtml(src) {
      src = String(src == null ? '' : src);
      function esc(s) {
        return s.replace(/[&<>"]/g, function (c) {
          return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
        });
      }
      function inline(s) {
        var codes = [];
        s = s.replace(/`([^`]+)`/g, function (_, c) { codes.push(c); return '\u0000' + (codes.length - 1) + '\u0000'; });
        s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, function (_, t, u) {
          return /^(https?:\/\/|\/)/i.test(u) ? '<a href="' + u + '" target="_blank" rel="noopener">' + t + '</a>' : t;
        });
        s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>').replace(/__([^_]+)__/g, '<strong>$1</strong>');
        s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>');
        s = s.replace(/\u0000(\d+)\u0000/g, function (_, i) { return '<code>' + codes[+i] + '</code>'; });
        return s;
      }
      function cells(l) { return l.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map(function (c) { return c.trim(); }); }
      function isSep(l) { return /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)+\|?\s*$/.test(l || ''); }
      function tcell(tag, align, html) { return '<' + tag + (align ? ' style="text-align:' + align + '"' : '') + '>' + html + '</' + tag + '>'; }
      var lines = esc(src).replace(/\r\n?/g, '\n').split('\n');
      var out = [], i = 0;
      while (i < lines.length) {
        var line = lines[i];
        if (/^```/.test(line)) {
          var buf = []; i++;
          while (i < lines.length && !/^```/.test(lines[i])) { buf.push(lines[i]); i++; }
          i++;
          out.push('<pre><code>' + buf.join('\n') + '</code></pre>');
          continue;
        }
        if (line.indexOf('|') >= 0 && isSep(lines[i + 1])) {
          var head = cells(line);
          // Выравнивание из GFM-разделителя: `---:` → вправо, `:---:` → по центру.
          var aligns = cells(lines[i + 1]).map(function (c) {
            var a = /^(:)?-+(:)?$/.exec(c);
            return a && a[2] ? (a[1] ? 'center' : 'right') : '';
          });
          i += 2; var body = '';
          while (i < lines.length && lines[i].indexOf('|') >= 0 && lines[i].trim() !== '') {
            var r = cells(lines[i]), tds = '';
            for (var k = 0; k < head.length; k++) tds += tcell('td', aligns[k], inline(r[k] || ''));
            body += '<tr>' + tds + '</tr>'; i++;
          }
          var ths = ''; for (var h = 0; h < head.length; h++) ths += tcell('th', aligns[h], inline(head[h]));
          out.push('<div class="tw"><table><thead><tr>' + ths + '</tr></thead><tbody>' + body + '</tbody></table></div>');
          continue;
        }
        var hm = /^(#{1,6})\s+(.*)$/.exec(line);
        if (hm) { out.push('<h' + hm[1].length + '>' + inline(hm[2]) + '</h' + hm[1].length + '>'); i++; continue; }
        if (/^\s*([-*+]|\d+\.)\s+/.test(line)) {
          var ol = /^\s*\d+\.\s+/.test(line), lis = '';
          while (i < lines.length && /^\s*([-*+]|\d+\.)\s+/.test(lines[i])) {
            lis += '<li>' + inline(lines[i].replace(/^\s*([-*+]|\d+\.)\s+/, '')) + '</li>'; i++;
          }
          out.push(ol ? '<ol>' + lis + '</ol>' : '<ul>' + lis + '</ul>');
          continue;
        }
        if (line.trim() === '') { i++; continue; }
        var para = [];
        while (i < lines.length && lines[i].trim() !== '' && !/^(```|#{1,6}\s|\s*([-*+]|\d+\.)\s)/.test(lines[i]) && !(lines[i].indexOf('|') >= 0 && isSep(lines[i + 1]))) {
          para.push(lines[i]); i++;
        }
        out.push('<p>' + inline(para.join('<br>')) + '</p>');
      }
      return out.join('');
    }
    function addMsg(role, text) {
      var h = log.querySelector('.hint');
      if (h) h.remove();
      var d = document.createElement('div');
      d.className = 'm ' + (role === 'user' ? 'u' : role === 'error' ? 'err' : 'a');
      if (role === 'assistant') d.innerHTML = mdToHtml(text);
      else d.textContent = text;
      log.appendChild(d);
      log.scrollTop = log.scrollHeight;
      return d;
    }
    // Открытие формы из чата: во вкладочной оболочке — новой вкладкой, на
    // отдельной странице — новым окном (паттерн openRefCurrent).
    function aiOpenURL(url, title) {
      try {
        if (window.__obEmbedded && window.parent && window.parent.obOpenTab) {
          window.parent.postMessage({ source: 'obOpenTab', url: url, title: title || 'Форма' }, '*');
          return;
        }
      } catch (e) {}
      window.open(url, '_blank');
    }
    // URL собирается на клиенте из провалидированных сервером частей: «вид» —
    // белый список, сущность/id — через encodeURIComponent. Сырые URL от
    // сервера/модели не принимаются (нет open redirect / javascript:).
    function aiActionURL(a) {
      var v = a['вид'], ent = encodeURIComponent(a['сущность'] || '');
      if (!ent) return '';
      if (a.id && (v === 'document' || v === 'catalog')) return '/ui/_ref-open/' + ent + '/' + encodeURIComponent(a.id);
      if (v === 'document' || v === 'catalog' || v === 'report' || v === 'processor') return '/ui/' + v + '/' + ent;
      return '';
    }
    // Карточка отложенного действия (план 51): создание исполняется только по
    // кнопке «Создать» (POST /ui/ai/action), «открыть» — просто кнопка перехода.
    // Всё содержимое вставляется через textContent — HTML из данных не рендерится.
    function addAction(a) {
      if (!a || typeof a !== 'object') return;
      var isCreate = a['тип'] === 'создать';
      var openURL = a['тип'] === 'открыть' ? aiActionURL(a) : '';
      if (!isCreate && !openURL) return;
      var card = document.createElement('div');
      card.className = 'm act';
      var lbl = document.createElement('div');
      lbl.className = 'lbl';
      lbl.textContent = a['подпись'] || '';
      card.appendChild(lbl);
      var btns = document.createElement('div');
      btns.className = 'btns';
      function finish(text, link) {
        btns.remove();
        var res = document.createElement('div');
        res.className = 'res';
        res.textContent = text;
        if (link) res.appendChild(link);
        card.appendChild(res);
        log.scrollTop = log.scrollHeight;
      }
      if (isCreate) {
        var ok = document.createElement('button');
        ok.type = 'button';
        ok.textContent = 'Создать';
        var no = document.createElement('button');
        no.type = 'button';
        no.className = 'sec';
        no.textContent = 'Отмена';
        ok.addEventListener('click', function () {
          ok.disabled = no.disabled = true;
          ok.textContent = 'Создаю…';
          fetch('/ui/ai/action', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(a) })
            .then(function (r) { return r.json(); })
            .then(function (d) {
              if (d && d.ok) {
                var link = document.createElement('a');
                link.textContent = d['подпись'] || 'открыть';
                link.href = d.url || '#';
                link.addEventListener('click', function (ev) {
                  ev.preventDefault();
                  aiOpenURL(d.url, d['подпись']);
                });
                finish('✓ Создано: ', link);
                pendingNote += '[Служебно: пользователь подтвердил действие, создан объект «' + (d['подпись'] || '') + '», id ' + (d.id || '') + '.]\n';
              } else {
                finish('✗ ' + ((d && d.error) || 'Ошибка'));
                pendingNote += '[Служебно: действие не выполнено: ' + ((d && d.error) || 'ошибка') + ']\n';
              }
            })
            .catch(function () {
              ok.disabled = no.disabled = false;
              ok.textContent = 'Создать';
            });
        });
        no.addEventListener('click', function () {
          finish('Отменено');
          pendingNote += '[Служебно: пользователь отклонил предложенное действие «' + (a['подпись'] || '').split('\n')[0] + '».]\n';
        });
        btns.appendChild(ok);
        btns.appendChild(no);
      } else {
        var go = document.createElement('button');
        go.type = 'button';
        go.textContent = 'Открыть';
        go.addEventListener('click', function () { aiOpenURL(openURL, a['сущность']); });
        btns.appendChild(go);
      }
      card.appendChild(btns);
      log.appendChild(card);
      log.scrollTop = log.scrollHeight;
    }
    function doSend() {
      var t = input.value.trim();
      if (!t || busy) return;
      input.value = '';
      addMsg('user', t);
      // Служебные заметки (результаты подтверждений) — префиксом в историю,
      // чтобы модель знала итог; в журнале чата показан только текст пользователя.
      history.push({ role: 'user', content: (pendingNote ? pendingNote + '\n' : '') + t });
      pendingNote = '';
      busy = true;
      send.disabled = true;
      var pend = addMsg('assistant', '…');
      fetch('/ui/ai/chat', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ messages: history }) })
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d && d.ok) {
            pend.innerHTML = mdToHtml(d.text);
            history.push({ role: 'assistant', content: d.text });
            if (d.actions && d.actions.length) {
              for (var i = 0; i < d.actions.length; i++) addAction(d.actions[i]);
            }
          } else {
            history.pop();
            pend.className = 'm err';
            pend.textContent = (d && d.error) || 'Ошибка';
          }
        })
        .catch(function () {
          history.pop();
          pend.className = 'm err';
          pend.textContent = 'Ошибка сети';
        })
        .finally(function () {
          busy = false;
          send.disabled = false;
          log.scrollTop = log.scrollHeight;
          input.focus();
        });
    }
    send.addEventListener('click', doSend);
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        doSend();
      }
    });
    btn.style.display = '';
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();

(function () {
  if (window.__obMsgInit) return;
  window.__obMsgInit = true;
  function init() {
    if (document.getElementById('ob-msg-bar')) return;
    var bar = document.createElement('div');
    bar.id = 'ob-msg-bar';
    bar.className = 'hidden';
    bar.innerHTML = '<div id="ob-msg-head"><span class="ttl">Сообщения <span class="cnt" id="ob-msg-cnt">0</span></span><button type="button" id="ob-msg-clear" title="Очистить">Очистить</button><span class="arr">▲</span></div><div id="ob-msg-list"><div class="empty">Сообщений нет</div></div>';
    document.body.appendChild(bar);
    var list = document.getElementById('ob-msg-list');
    var cnt = document.getElementById('ob-msg-cnt');
    var head = document.getElementById('ob-msg-head');
    var btnClear = document.getElementById('ob-msg-clear');
    var prevSig = sessionStorage.getItem('obMsgSig') || '';
    var prevOpen = sessionStorage.getItem('obMsgOpen') === '1';
    var lastHtml = '';
    function fmtTime(ts) {
      try {
        var d = new Date(ts);
        var h = String(d.getHours()).padStart(2, '0');
        var m = String(d.getMinutes()).padStart(2, '0');
        var s = String(d.getSeconds()).padStart(2, '0');
        return h + ':' + m + ':' + s;
      } catch (e) {
        return '';
      }
    }
    function escapeHtml(s) {
      return String(s).replace(/[&<>"']/g, function (c) {
        return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
      });
    }
    function render(msgs) {
      if (!msgs || !msgs.length) {
        bar.classList.add('hidden');
        bar.classList.remove('open');
        list.innerHTML = '<div class="empty">Сообщений нет</div>';
        lastHtml = '';
        cnt.classList.remove('show');
        prevSig = '';
        sessionStorage.removeItem('obMsgSig');
        return;
      }
      bar.classList.remove('hidden');
      var html = '';
      for (var i = 0; i < msgs.length; i++) {
        var m = msgs[i];
        html += '<div class="it"><span class="t">' + fmtTime(m.time) + '</span><span>' + escapeHtml(m.text) + '</span></div>';
      }
      if (html !== lastHtml) {
        // Не перерисовывать пока пользователь выделяет текст внутри панели —
        // иначе сбрасывается выделение.
        var sel = window.getSelection ? window.getSelection() : null;
        if (!(sel && !sel.isCollapsed && sel.anchorNode && list.contains(sel.anchorNode))) {
          list.innerHTML = html;
          lastHtml = html;
          list.scrollTop = list.scrollHeight;
        }
      }
      cnt.textContent = msgs.length;
      cnt.classList.add('show');
      var sig = msgs.length ? msgs[msgs.length - 1].time + '|' + msgs.length : '';
      if (sig !== prevSig) {
        bar.classList.add('open');
        prevOpen = true;
        sessionStorage.setItem('obMsgOpen', '1');
      } else if (prevOpen) {
        bar.classList.add('open');
      }
      prevSig = sig;
      sessionStorage.setItem('obMsgSig', sig);
    }
    head.addEventListener('click', function (e) {
      if (e.target === btnClear) return;
      bar.classList.toggle('open');
      prevOpen = bar.classList.contains('open');
      sessionStorage.setItem('obMsgOpen', prevOpen ? '1' : '0');
    });
    btnClear.addEventListener('click', function (e) {
      e.stopPropagation();
      fetch('/ui/messages/clear', { method: 'POST' }).then(function () { render([]); });
    });
    function load() {
      fetch('/ui/messages').then(function (r) { return r.json(); }).then(function (d) {
        render(d.messages || []);
      }).catch(function () {});
    }
    window.obReloadMessages = load;
    load();
    setInterval(load, 3000);
    document.addEventListener('submit', function () { setTimeout(load, 400); }, true);
  }
  // Во вкладочной оболочке (issue #322/#323) панель сообщений держит только
  // верхнее окно: каждый iframe со своим setInterval(/ui/messages) + SSE упирал
  // браузер в лимит ~6 соединений на хост, и переключение вкладок «зависало».
  // Во фрейме панель не строим и не поллим — после submit просим верхнее окно
  // обновиться, чтобы сообщение появилось сразу, а не через интервал.
  if (window.__obEmbedded) {
    document.addEventListener('submit', function () {
      setTimeout(function () {
        try { if (window.top && window.top.obReloadMessages) window.top.obReloadMessages(); } catch (e) {}
      }, 400);
    }, true);
    return;
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();

if ('serviceWorker' in navigator) {
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/sw.js').catch(function () {});
  });
}

function obInitRichText() {
  if (typeof Quill === 'undefined') return;
  var fields = document.querySelectorAll('textarea.richtext-field');
  fields.forEach(function (ta) {
    var holder = ta.nextElementSibling;
    if (!holder || !holder.classList || !holder.classList.contains('richtext-editor')) return;
    if (holder.getAttribute('data-ql-ready') === '1') return;
    holder.setAttribute('data-ql-ready', '1');
    var q = new Quill(holder, {
      theme: 'snow',
      modules: { toolbar: [
        [{ header: [1, 2, 3, false] }],
        ['bold', 'italic', 'underline', 'strike'],
        [{ list: 'ordered' }, { list: 'bullet' }],
        ['blockquote', 'link', 'image'],
        ['clean']
      ] }
    });
    q.setContents(q.clipboard.convert({ html: ta.value }), 'silent');
    ta.style.display = 'none';
    function normalizeLists(html) {
      var box = document.createElement('div');
      box.innerHTML = html;
      box.querySelectorAll('ol').forEach(function (ol) {
        var items = Array.prototype.slice.call(ol.children).filter(function (el) {
          return el.tagName === 'LI';
        });
        if (!items.length) return;
        var isBullet = items[0].getAttribute('data-list') === 'bullet';
        if (isBullet) {
          var ul = document.createElement('ul');
          while (ol.firstChild) ul.appendChild(ol.firstChild);
          ol.parentNode.replaceChild(ul, ol);
        }
      });
      box.querySelectorAll('li[data-list]').forEach(function (li) { li.removeAttribute('data-list'); });
      box.querySelectorAll('.ql-ui').forEach(function (n) { n.remove(); });
      return box.innerHTML;
    }
    function sync() { ta.value = normalizeLists(q.root.innerHTML); }
    q.on('text-change', sync);
    var form = ta.form;
    if (form) form.addEventListener('submit', sync);
  });
}
obReady(obInitRichText);

function obImageUpload(input, url) {
  var file = input.files && input.files[0];
  if (!file) return;
  var wrap = input.closest('.img-field');
  var fd = new FormData();
  fd.append('file', file);
  fetch(url, { method: 'POST', body: fd, credentials: 'same-origin' })
    .then(function (resp) {
      if (!resp.ok) {
        return resp.text().then(function (t) { throw new Error(t || ('HTTP ' + resp.status)); });
      }
      return resp.json();
    })
    .then(function (data) {
      if (!wrap || !data || !data.ref) return;
      wrap.querySelector('input[type=hidden]').value = data.ref;
      var prev = wrap.querySelector('.img-preview');
      if (prev) {
        var img = prev.querySelector('img');
        if (img) img.src = '/ui/_image/' + data.ref;
        prev.style.display = '';
      }
      var clr = wrap.querySelector('.img-clear-btn');
      if (clr) clr.style.display = '';
    })
    .catch(function (e) { alert('Ошибка загрузки картинки: ' + e.message); })
    .finally(function () { input.value = ''; });
}

function obImageClear(btn) {
  var wrap = btn.closest('.img-field');
  if (!wrap) return;
  var hidden = wrap.querySelector('input[type=hidden]');
  if (hidden) hidden.value = '';
  var prev = wrap.querySelector('.img-preview');
  if (prev) {
    prev.style.display = 'none';
    var img = prev.querySelector('img');
    if (img) img.removeAttribute('src');
  }
  btn.style.display = 'none';
}

function obSplitDataList(value) {
  return String(value || '').split(',').map(function (v) { return v.trim(); }).filter(Boolean);
}

function obClosestSelect(btn) {
  var parent = btn && btn.parentElement;
  return parent ? parent.querySelector('select') : null;
}

function obSendPopupCancel() {
  try {
    if (window.parent) window.parent.postMessage({ source: 'obRefCancel' }, '*');
  } catch (e) {}
}

function obInitFormDelegates() {
  if (window.__obFormDelegates) return;
  window.__obFormDelegates = true;
  document.addEventListener('click', function (e) {
    if (!e.target.closest) return;
    var selectOnClick = e.target.closest('[data-ob-select-on-click]');
    if (selectOnClick && selectOnClick.select) {
      selectOnClick.select();
      return;
    }
    var popupCancel = e.target.closest('[data-ob-popup-cancel]');
    if (popupCancel) {
      e.preventDefault();
      obSendPopupCancel();
      return;
    }
    var toggleNext = e.target.closest('[data-ob-toggle-next]');
    if (toggleNext) {
      e.preventDefault();
      var next = toggleNext.nextElementSibling;
      if (next) next.style.display = next.style.display === 'none' ? 'block' : 'none';
      return;
    }
    var refCurrent = e.target.closest('[data-ob-ref-current]');
    if (refCurrent) {
      e.preventDefault();
      var targetId = refCurrent.getAttribute('data-ob-ref-current') || '';
      openRefCurrent(targetId === 'closest' ? obClosestSelect(refCurrent) : targetId);
      return;
    }
    var refPicker = e.target.closest('[data-ob-ref-picker]');
    if (refPicker) {
      e.preventDefault();
      var pickerTarget = refPicker.getAttribute('data-ob-ref-picker') || '';
      openRefPicker(pickerTarget === 'closest' ? obClosestSelect(refPicker) : pickerTarget);
      return;
    }
    var refPickerSelf = e.target.closest('[data-ob-ref-picker-self]');
    if (refPickerSelf) {
      e.preventDefault();
      openRefPicker(obClosestSelect(refPickerSelf));
      return;
    }
    var refCurrentSelf = e.target.closest('[data-ob-ref-current-self]');
    if (refCurrentSelf) {
      e.preventDefault();
      openRefCurrent(obClosestSelect(refCurrentSelf));
      return;
    }
    var imageClear = e.target.closest('[data-ob-image-clear]');
    if (imageClear) {
      e.preventDefault();
      obImageClear(imageClear);
      return;
    }
    var removeRow = e.target.closest('[data-ob-remove-row]');
    if (removeRow) {
      e.preventDefault();
      var row = removeRow.closest(removeRow.getAttribute('data-ob-remove-row') || 'tr');
      if (row) {
        var domTable = row.closest('table[data-ob-dom-table]');
        var body = row.parentElement;
        var index = row.sectionRowIndex;
        row.remove();
        if (domTable && body) {
          var next = body.rows && body.rows.length ? body.rows[Math.min(Math.max(index, 0), body.rows.length - 1)] : null;
          obDOMFinishMutation(domTable, next, false);
          obDOMNotifyMutation(domTable, 'delete');
        }
      }
      return;
    }
    var addTp = e.target.closest('[data-ob-add-tp-row]');
    if (addTp) {
      e.preventDefault();
      var tpName = addTp.getAttribute('data-tp-name') || '';
      var tbody = document.getElementById('tp-body-' + tpName);
      addTpRow(tpName, obSplitDataList(addTp.getAttribute('data-tp-fields')), obSplitDataList(addTp.getAttribute('data-tp-num-fields')), tbody ? tbody.rows.length : 0,
        null, null, obSplitDataList(addTp.getAttribute('data-tp-bool-fields')));
      var table = tbody && tbody.closest ? tbody.closest('table[data-ob-dom-table]') : null;
      if (table) obDOMNotifyMutation(table, 'add');
      return;
    }
    var fileClick = e.target.closest('[data-ob-file-click]');
    if (fileClick) {
      e.preventDefault();
      var fileInput = document.getElementById(fileClick.getAttribute('data-ob-file-click') || '');
      if (fileInput) fileInput.click();
    }
  });
  document.addEventListener('input', function (e) {
    if (!e.target.closest) return;
    var tpInput = e.target.closest('[data-ob-tp-recalc]');
    if (tpInput) recalcTpRow(tpInput);
  });
  document.addEventListener('change', function (e) {
    if (!e.target.closest) return;
    var imageInput = e.target.closest('[data-ob-image-upload]');
    if (imageInput) {
      obImageUpload(imageInput, imageInput.getAttribute('data-ob-image-upload') || '');
      return;
    }
    var submitInput = e.target.closest('[data-ob-submit-form]');
    if (submitInput) {
      var form = document.getElementById(submitInput.getAttribute('data-ob-submit-form') || '');
      if (form) form.submit();
    }
  });
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || !form.getAttribute) return;
    var msg = form.getAttribute('data-ob-confirm');
    if (msg && !confirm(msg)) e.preventDefault();
  }, true);
}
obReady(obInitFormDelegates);

function obTPRefOpts() {
  if (!window._tpRefOpts) window._tpRefOpts = obReadJSONScript('ob-tp-ref-opts', {});
  return window._tpRefOpts || {};
}

function obTPRefMeta() {
  if (!window._tpRefMeta) window._tpRefMeta = obReadJSONScript('ob-tp-ref-meta', {});
  return window._tpRefMeta || {};
}

// Подписи и порядок значений перечислений колонок ТЧ (#1010). На managed-форме
// эти же глобалы уже наполнил managed.js из своих script-тегов, поэтому читаем
// ТОЛЬКО когда их нет: иначе автоформенный тег (пустой на managed-странице)
// затёр бы данные грида.
function obTPEnumLabels() {
  if (!window._tpEnumLabels) window._tpEnumLabels = obReadJSONScript('ob-tp-enum-labels', {});
  return window._tpEnumLabels || {};
}

function obTPEnumOrder() {
  if (!window._tpEnumOrder) window._tpEnumOrder = obReadJSONScript('ob-tp-enum-order', {});
  return window._tpEnumOrder || {};
}

// boolFields — список булевых колонок: тип колонки в разметке автоформы больше
// нигде не виден, а флажок вместо текстового поля нужен и в добавленной строке.
function addTpRow(tpName, fields, numFields, idx, tbodyOverride, virtualFields, boolFields) {
  var tbody = tbodyOverride || document.getElementById('tp-body-' + tpName);
  var table = tbody && tbody.closest ? tbody.closest('table[data-ob-dom-table]') : null;
  var domWritable = !!(table && !obDOMTableReadOnly(table));
  var tr = document.createElement('tr');
  var refOpts = (obTPRefOpts()[tpName]) || {};
  var refMeta = (obTPRefMeta()[tpName]) || {};
  var enumLabels = (obTPEnumLabels()[tpName]) || {};
  var enumOrder = (obTPEnumOrder()[tpName]) || {};
  var bools = Array.isArray(boolFields) ? boolFields : [];
  if (tbody && tbody.getAttribute('data-tp-cmd') === '1') {
    var tdSel = document.createElement('td');
    tdSel.style.textAlign = 'center';
    var cbSel = document.createElement('input');
    cbSel.type = 'checkbox';
    cbSel.className = '_tp-sel';
    tdSel.appendChild(cbSel);
    tr.appendChild(tdSel);
  }
  fields.forEach(function (fn) {
    var td = document.createElement('td');
    if (refOpts[fn] !== undefined) {
      var wrapper = document.createElement('div');
      wrapper.style.cssText = 'display:flex;gap:4px;align-items:center';
      var sel = document.createElement('select');
      sel.name = 'tp.' + tpName + '.' + idx + '.' + fn;
      sel.style.flex = '1';
      var meta = refMeta[fn];
      if (meta && meta.entity) {
        sel.setAttribute('data-ref-entity', meta.entity);
        if (meta.allowCreate) sel.setAttribute('data-ref-allow-create', '1');
      }
      var defOpt = document.createElement('option');
      defOpt.value = '';
      defOpt.textContent = '— выбрать —';
      sel.appendChild(defOpt);
      (refOpts[fn] || []).forEach(function (opt) {
        var o = document.createElement('option');
        o.value = opt.id;
        o.textContent = opt._label || opt.id;
        sel.appendChild(o);
      });
      var pickBtn = document.createElement('button');
      pickBtn.type = 'button';
      pickBtn.textContent = '...';
      pickBtn.title = 'Выбрать из списка';
      pickBtn.style.cssText = 'padding:4px 8px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0';
      pickBtn.setAttribute('data-ob-ref-picker-self', '');
      wrapper.appendChild(sel);
      wrapper.appendChild(pickBtn);
      if (meta && meta.entity) {
        var openBtn = document.createElement('button');
        openBtn.type = 'button';
        openBtn.textContent = '🔍';
        openBtn.title = 'Открыть карточку';
        openBtn.style.cssText = 'padding:4px 7px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0';
        openBtn.setAttribute('data-ob-ref-current-self', '');
        wrapper.appendChild(openBtn);
      }
      td.appendChild(wrapper);
    } else if (enumLabels[fn]) {
      // Перечисление — список значений в порядке объявления values:, первый
      // пункт пустой (значение «не выбрано» законно).
      var enumSel = document.createElement('select');
      enumSel.name = 'tp.' + tpName + '.' + idx + '.' + fn;
      var enumEmpty = document.createElement('option');
      enumEmpty.value = '';
      enumEmpty.textContent = '— выбрать —';
      enumSel.appendChild(enumEmpty);
      var enumVals = (enumOrder[fn] && enumOrder[fn].length) ? enumOrder[fn] : Object.keys(enumLabels[fn]);
      enumVals.forEach(function (val) {
        var o = document.createElement('option');
        o.value = val;
        o.textContent = (enumLabels[fn][val] !== undefined) ? enumLabels[fn][val] : val;
        enumSel.appendChild(o);
      });
      td.appendChild(enumSel);
    } else if (bools.indexOf(fn) !== -1) {
      // Флажок: снятый чекбокс не уезжает в запрос вовсе, и разбор строки ТЧ
      // читает отсутствие поля как «ложь» — ровно то, что нужно.
      var cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.value = 'true';
      cb.name = 'tp.' + tpName + '.' + idx + '.' + fn;
      td.appendChild(cb);
    } else {
      var inp = document.createElement('input');
      inp.name = 'tp.' + tpName + '.' + idx + '.' + fn;
      if (numFields.indexOf(fn) !== -1) {
        inp.type = 'number';
        inp.setAttribute('data-tp-num', fn);
        inp.setAttribute('data-ob-tp-recalc', '');
      } else {
        inp.type = 'text';
      }
      td.appendChild(inp);
    }
    tr.appendChild(td);
  });
  (Array.isArray(virtualFields) ? virtualFields : []).forEach(function (name) {
    var td = document.createElement('td');
    td.setAttribute('data-ob-virtual-col', name);
    td.textContent = '';
    tr.appendChild(td);
  });
  var tdDel = document.createElement('td');
  var btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'del-btn';
  btn.textContent = '×';
  btn.setAttribute('data-ob-remove-row', 'tr');
  if (domWritable) {
    btn.title = 'Delete';
    btn.setAttribute('aria-keyshortcuts', 'Delete');
  }
  tdDel.appendChild(btn);
  tr.appendChild(tdDel);
  tbody.appendChild(tr);
  if (table) obDOMFinishMutation(table, tr, true);
}

function recalcTpRow(inp) {
  var tr = inp.closest('tr');
  var nums = tr.querySelectorAll('[data-tp-num]');
  if (nums.length === 3) {
    var a = parseFloat(nums[0].value) || 0;
    var b = parseFloat(nums[1].value) || 0;
    nums[2].value = (a * b).toFixed(2);
  }
  recalcTpTotals(inp);
}

function recalcTpTotals(inp) {
  var tbody = inp.closest('tbody');
  if (!tbody) return;
  var table = tbody.closest('table');
  if (!table) return;
  var tfoot = table.querySelector('tfoot');
  if (!tfoot) return;
  var totals = {};
  var numFields = [];
  tbody.querySelectorAll('[data-tp-num]').forEach(function (el) {
    var fn = el.getAttribute('data-tp-num');
    if (totals[fn] === undefined) {
      totals[fn] = 0;
      numFields.push(fn);
    }
    totals[fn] += parseFloat(el.value) || 0;
  });
  var hasData = false;
  numFields.forEach(function (fn) {
    tfoot.querySelectorAll('[data-tp-total]').forEach(function (cell) {
      var key = cell.getAttribute('data-tp-total');
      if (key && key.split('.').pop() === fn) {
        cell.textContent = totals[fn].toLocaleString('ru-RU', { minimumFractionDigits: 0, maximumFractionDigits: 2 });
      }
    });
    if (totals[fn] !== 0) hasData = true;
  });
  tfoot.style.display = hasData ? '' : 'none';
}

obReady(function () {
  document.querySelectorAll('.tp-table tfoot').forEach(function (tfoot) {
    var table = tfoot.closest('table');
    if (!table) return;
    var tbody = table.querySelector('tbody');
    if (!tbody || !tbody.rows.length) return;
    var firstNum = tbody.querySelector('[data-tp-num]');
    if (firstNum) recalcTpTotals(firstNum);
  });
});

function openItemPicker(payload, elementName, eventContext) {
  if (!payload) return;
  var cols = payload.columns || [];
  var rows = payload.rows || [];
  var cfg = payload.config || {};
  var old = document.getElementById('_item-picker-modal');
  if (old) old.remove();
  var modal = document.createElement('div');
  modal.id = '_item-picker-modal';
  modal.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;display:flex;align-items:center;justify-content:center';
  var box = document.createElement('div');
  box.style.cssText = 'background:#fff;border-radius:10px;padding:20px;width:720px;max-width:96vw;max-height:86vh;display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.18)';
  var head = document.createElement('div');
  head.style.cssText = 'display:flex;align-items:center;justify-content:space-between;margin-bottom:12px';
  var title = document.createElement('div');
  title.style.cssText = 'font-weight:600;font-size:15px;color:#1e293b';
  title.textContent = cfg.title || 'Подбор';
  var counter = document.createElement('div');
  counter.style.cssText = 'font-size:12px;color:#64748b';
  head.appendChild(title);
  head.appendChild(counter);
  box.appendChild(head);
  var search = document.createElement('input');
  search.type = 'text';
  search.placeholder = 'Поиск...';
  search.autocomplete = 'off';
  search.style.cssText = 'padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;margin-bottom:10px;outline:none';
  box.appendChild(search);
  var scroll = document.createElement('div');
  scroll.style.cssText = 'overflow:auto;flex:1;min-height:120px;border:1px solid #e2e8f0;border-radius:7px';
  var table = document.createElement('table');
  table.className = 'tp-table';
  table.style.cssText = 'width:100%;font-size:13px;margin:0';
  var thead = document.createElement('thead');
  var htr = document.createElement('tr');
  var thCb = document.createElement('th');
  thCb.style.width = '34px';
  var cbAll = document.createElement('input');
  cbAll.type = 'checkbox';
  thCb.appendChild(cbAll);
  htr.appendChild(thCb);
  cols.forEach(function (c) {
    var th = document.createElement('th');
    th.textContent = c.title || c.name;
    htr.appendChild(th);
  });
  thead.appendChild(htr);
  table.appendChild(thead);
  var tbody = document.createElement('tbody');
  function rowText(r) {
    return cols.map(function (c) {
      var v = (r.data || {})[c.name];
      return v == null ? '' : String(v);
    }).join(' ').toLowerCase();
  }
  rows.forEach(function (r) {
    var tr = document.createElement('tr');
    tr.setAttribute('data-id', r.id || '');
    tr.setAttribute('data-search', rowText(r));
    var tdCb = document.createElement('td');
    tdCb.style.textAlign = 'center';
    var cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.className = '_ip-cb';
    if (cfg.checkAll) cb.checked = true;
    cb.onchange = updateCounter;
    tdCb.appendChild(cb);
    tr.appendChild(tdCb);
    cols.forEach(function (c) {
      var td = document.createElement('td');
      var v = (r.data || {})[c.name];
      if (c.editable) {
        var inp = document.createElement('input');
        inp.type = (c.type === 'number') ? 'number' : 'text';
        if (c.type === 'number') inp.step = 'any';
        inp.value = (v == null ? '' : v);
        inp.className = '_ip-val';
        inp.setAttribute('data-col', c.name);
        inp.style.cssText = 'width:90px;padding:3px 6px';
        td.appendChild(inp);
      } else {
        td.textContent = (v == null ? '' : String(v));
        td.setAttribute('data-col', c.name);
      }
      tr.appendChild(td);
    });
    tbody.appendChild(tr);
  });
  tbody.addEventListener('input', function (e) {
    var inp = e.target;
    if (!inp.classList.contains('_ip-val')) return;
    if (cfg.qtyField && inp.getAttribute('data-col') !== cfg.qtyField) return;
    var tr = inp.closest('tr');
    if (!tr) return;
    var cb = tr.querySelector('._ip-cb');
    if (!cb) return;
    var val = parseFloat(inp.value);
    cb.checked = (!isNaN(val) && val > 0);
    updateCounter();
    updateBasket();
  });
  table.appendChild(tbody);
  scroll.appendChild(table);
  box.appendChild(scroll);
  var displayCol = null;
  for (var ci = 0; ci < cols.length; ci++) {
    if (cols[ci].name !== cfg.qtyField) {
      displayCol = cols[ci];
      break;
    }
  }
  var qtyCol = null;
  for (var qi = 0; qi < cols.length; qi++) {
    if (cols[qi].name === cfg.qtyField) {
      qtyCol = cols[qi];
      break;
    }
  }
  var basketHead = document.createElement('div');
  basketHead.style.cssText = 'display:flex;align-items:center;justify-content:space-between;margin-top:10px;padding:6px 10px;background:#f1f5f9;border-radius:7px;cursor:pointer;user-select:none;font-weight:600;font-size:13px;color:#334155';
  var basketTitle = document.createElement('span');
  basketTitle.textContent = 'Корзина';
  var basketBadge = document.createElement('span');
  basketBadge.style.cssText = 'font-size:12px;color:#64748b;font-weight:400';
  basketHead.appendChild(basketTitle);
  basketHead.appendChild(basketBadge);
  box.appendChild(basketHead);
  var basketScroll = document.createElement('div');
  basketScroll.style.cssText = 'overflow:auto;max-height:180px;margin-top:4px;border:1px solid #e2e8f0;border-radius:7px;display:none';
  var basketTable = document.createElement('table');
  basketTable.className = 'tp-table';
  basketTable.style.cssText = 'width:100%;font-size:13px;margin:0';
  var bThead = document.createElement('thead');
  var bHtr = document.createElement('tr');
  var bTh1 = document.createElement('th');
  bTh1.textContent = displayCol ? (displayCol.title || displayCol.name) : 'Номенклатура';
  bHtr.appendChild(bTh1);
  var bTh2 = document.createElement('th');
  bTh2.style.cssText = 'width:90px;text-align:right';
  bTh2.textContent = qtyCol ? (qtyCol.title || qtyCol.name) : 'Кол-во';
  bHtr.appendChild(bTh2);
  bThead.appendChild(bHtr);
  basketTable.appendChild(bThead);
  var bTbody = document.createElement('tbody');
  basketTable.appendChild(bTbody);
  basketScroll.appendChild(basketTable);
  box.appendChild(basketScroll);
  basketHead.addEventListener('click', function () {
    basketScroll.style.display = basketScroll.style.display === 'none' ? '' : 'none';
  });
  var foot = document.createElement('div');
  foot.style.cssText = 'margin-top:12px;display:flex;justify-content:flex-end;gap:8px';
  var btnCancel = document.createElement('button');
  btnCancel.type = 'button';
  btnCancel.textContent = 'Отмена';
  btnCancel.style.cssText = 'padding:7px 18px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px';
  var btnOk = document.createElement('button');
  btnOk.type = 'button';
  btnOk.textContent = 'Перенести в документ';
  btnOk.style.cssText = 'padding:7px 18px;border:1px solid #2563eb;border-radius:7px;background:#2563eb;color:#fff;cursor:pointer;font-size:13px;font-weight:600';
  foot.appendChild(btnCancel);
  foot.appendChild(btnOk);
  box.appendChild(foot);
  modal.appendChild(box);
  document.body.appendChild(modal);
  function checkedRows() {
    return Array.prototype.slice.call(tbody.querySelectorAll('._ip-cb')).filter(function (cb) {
      return cb.checked && cb.closest('tr').style.display !== 'none';
    });
  }
  function updateCounter() { counter.textContent = 'Выбрано: ' + checkedRows().length; }
  function updateBasket() {
    bTbody.innerHTML = '';
    var cnt = 0;
    if (!cfg.qtyField) return;
    Array.prototype.forEach.call(tbody.rows, function (tr) {
      if (tr.style.display === 'none') return;
      var inp = tr.querySelector('._ip-val[data-col="' + cfg.qtyField + '"]');
      if (!inp) return;
      var val = parseFloat(inp.value);
      if (isNaN(val) || val <= 0) return;
      cnt++;
      var bTr = document.createElement('tr');
      var tdName = document.createElement('td');
      if (displayCol) {
        var srcTd = tr.querySelector('td[data-col="' + displayCol.name + '"]');
        tdName.textContent = srcTd ? srcTd.textContent : '';
      }
      var tdQty = document.createElement('td');
      tdQty.style.cssText = 'text-align:right;font-weight:600';
      tdQty.textContent = inp.value;
      bTr.appendChild(tdName);
      bTr.appendChild(tdQty);
      bTbody.appendChild(bTr);
    });
    basketBadge.textContent = cnt > 0 ? (cnt + ' поз.') : 'пусто';
    if (cnt > 0 && basketScroll.style.display === 'none') basketScroll.style.display = '';
    if (cnt === 0) basketScroll.style.display = 'none';
  }
  updateCounter();
  updateBasket();
  search.focus();
  search.addEventListener('input', function () {
    var q = this.value.toLowerCase();
    Array.prototype.forEach.call(tbody.rows, function (tr) {
      tr.style.display = (tr.getAttribute('data-search') || '').indexOf(q) >= 0 ? '' : 'none';
    });
    updateCounter();
    updateBasket();
  });
  cbAll.addEventListener('change', function () {
    Array.prototype.forEach.call(tbody.rows, function (tr) {
      if (tr.style.display === 'none') return;
      var cb = tr.querySelector('._ip-cb');
      if (cb) cb.checked = cbAll.checked;
    });
    updateCounter();
    updateBasket();
  });
  btnCancel.addEventListener('click', function () { modal.remove(); });
  btnOk.addEventListener('click', function () {
    var result = checkedRows().map(function (cb) {
      var tr = cb.closest('tr');
      var obj = { id: tr.getAttribute('data-id') };
      cols.forEach(function (c) {
        if (c.editable) {
          var inp = tr.querySelector('._ip-val[data-col="' + c.name + '"]');
          obj[c.name] = inp ? inp.value : '';
        } else {
          var td = tr.querySelector('td[data-col="' + c.name + '"]');
          obj[c.name] = td ? td.textContent : '';
        }
      });
      return obj;
    });
    modal.remove();
    if (typeof obFire === 'function') {
      var params = {};
      if (eventContext) {
        Object.keys(eventContext).forEach(function (key) { params[key] = eventContext[key]; });
      }
      params._pick_result = JSON.stringify(result);
      obFire(elementName, 'Выбор', params);
    }
  });
}

function openRefPicker(selOrId) {
  var sel = (typeof selOrId === 'string') ? document.getElementById(selOrId) : selOrId;
  if (!sel) return;
  if (sel.disabled || sel.readOnly || sel.hasAttribute('readonly')) return;
  var refEntity = sel.getAttribute('data-ref-entity') || '';
  var allowCreate = sel.getAttribute('data-ref-allow-create') === '1';
  var localOpts = [];
  for (var i = 0; i < sel.options.length; i++) {
    var o = sel.options[i];
    if (o.value) localOpts.push({ id: o.value, label: o.text });
  }
  var old = document.getElementById('_ref-picker-modal');
  if (old) old.remove();
  var modal = document.createElement('div');
  modal.id = '_ref-picker-modal';
  modal.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;display:flex;align-items:center;justify-content:center';
  var inner = '<div style="background:#fff;border-radius:10px;padding:20px;width:480px;max-width:95vw;max-height:80vh;display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.18)">';
  inner += '<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px"><div style="font-weight:600;font-size:15px;color:#1e293b">Выбор из списка</div>';
  if (allowCreate && refEntity) {
    inner += '<button type="button" id="_rp-create" style="padding:5px 12px;border:1px solid #16a34a;border-radius:6px;background:#f0fdf4;cursor:pointer;font-size:12px;font-weight:600;color:#16a34a" title="Создать новый">+ Создать</button>';
  }
  inner += '</div>';
  inner += '<input id="_rp-search" type="text" placeholder="Поиск..." autocomplete="off" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;margin-bottom:10px;outline:none">';
  inner += '<div id="_rp-list" style="overflow-y:auto;flex:1;border:1px solid #e2e8f0;border-radius:7px"></div>';
  inner += '<div style="display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:12px"><div id="_rp-status" style="font-size:12px;color:#94a3b8"></div><button type="button" id="_rp-cancel" style="padding:6px 18px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px">Отмена</button></div>';
  inner += '</div>';
  modal.innerHTML = inner;
  document.body.appendChild(modal);
  var list = document.getElementById('_rp-list');
  var status = document.getElementById('_rp-status');
  // rpActive — подсвеченная строка списка. Форма выбора обязана работать с
  // клавиатуры целиком: ищем в поле, ↑/↓ ведут по найденному, Enter выбирает.
  // Раньше выбрать пункт можно было только мышью.
  var rpActive = -1;
  function rpItems() { return list ? list.querySelectorAll('._rp-item') : []; }
  function rpActiveId() {
    var items = rpItems();
    return (rpActive >= 0 && items[rpActive]) ? items[rpActive].getAttribute('data-id') : '';
  }
  function rpPaint() {
    var items = rpItems();
    for (var i = 0; i < items.length; i++) items[i].style.background = (i === rpActive) ? '#eef2ff' : '';
    if (rpActive >= 0 && items[rpActive] && items[rpActive].scrollIntoView) {
      items[rpActive].scrollIntoView({ block: 'nearest' });
    }
  }
  // keepId — не сбрасывать подсветку на первую строку, когда список
  // перестраивается ответом серверного поиска, а не действием пользователя.
  function renderItems(opts, keepId) {
    if (!list) return;
    list.innerHTML = '';
    rpActive = -1;
    if (!opts || opts.length === 0) {
      var empty = document.createElement('div');
      empty.style.cssText = 'padding:16px;color:#94a3b8;font-size:13px;text-align:center';
      empty.textContent = 'Список пуст';
      list.appendChild(empty);
      return;
    }
    for (var i = 0; i < opts.length; i++) {
      var item = document.createElement('div');
      item.className = '_rp-item';
      item.setAttribute('data-id', opts[i].id);
      item.setAttribute('data-label', opts[i].label);
      item.style.cssText = 'padding:9px 14px;cursor:pointer;border-bottom:1px solid #f1f5f9;font-size:14px;color:#1e293b';
      item.textContent = opts[i].label;
      (function (idx) {
        item.addEventListener('mouseenter', function () { rpActive = idx; rpPaint(); });
      })(i);
      list.appendChild(item);
    }
    rpActive = 0;
    if (keepId) {
      for (var k = 0; k < opts.length; k++) {
        if (String(opts[k].id) === String(keepId)) { rpActive = k; break; }
      }
    }
    rpPaint();
  }
  function renderLocal(q) {
    q = (q || '').toLowerCase();
    var filtered = localOpts;
    if (q) {
      filtered = localOpts.filter(function (opt) {
        return String(opt.label || '').toLowerCase().indexOf(q) >= 0;
      });
    }
    renderItems(filtered);
    if (status) status.textContent = '';
  }
  function selectItem(item) {
    if (!window._rpTarget) return;
    var id = item.getAttribute('data-id') || '';
    var label = item.getAttribute('data-label') || item.textContent || id;
    var exists = false;
    for (var i = 0; i < window._rpTarget.options.length; i++) {
      if (window._rpTarget.options[i].value === id) {
        exists = true;
        break;
      }
    }
    if (!exists && id) {
      var opt = document.createElement('option');
      opt.value = id;
      opt.textContent = label;
      window._rpTarget.appendChild(opt);
    }
    window._rpTarget.value = id;
    try {
      window._rpTarget.dispatchEvent(new Event('change', { bubbles: true }));
    } catch (e) {}
  }
  var requestSeq = 0;
  var searchTimer = null;
  function loadServer(q) {
    if (!refEntity || refEntity === '_users' || !window.fetch) {
      renderLocal(q);
      return;
    }
    var seq = ++requestSeq;
    if (status) status.textContent = 'Загрузка...';
    var url = '/ui/_ref-options/' + encodeURIComponent(refEntity) + '?limit=50&q=' + encodeURIComponent(q || '');
    fetch(url, { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (resp) {
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        return resp.json();
      })
      .then(function (data) {
        if (seq !== requestSeq) return;
        var keep = rpActiveId();
        var rows = (data && data.items) || [];
        var opts = rows.map(function (row) {
          var id = row && row.id != null ? String(row.id) : '';
          return { id: id, label: String((row && row._label) || id) };
        }).filter(function (opt) { return opt.id !== ''; });
        renderItems(opts, keep);
        if (status) {
          var total = data && typeof data.total === 'number' ? data.total : opts.length;
          status.textContent = total > opts.length ? 'Показано ' + opts.length + ' из ' + total : '';
        }
      })
      .catch(function () {
        if (seq !== requestSeq) return;
        renderLocal(q);
      });
  }
  window._rpTarget = sel;
  var search = document.getElementById('_rp-search');
  search.focus();
  search.addEventListener('input', function () {
    var q = this.value;
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(function () { loadServer(q); }, 180);
  });
  search.addEventListener('keydown', function (e) {
    var items = rpItems();
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!items.length) return;
      var next = rpActive + (e.key === 'ArrowDown' ? 1 : -1);
      if (next < 0) next = items.length - 1;
      if (next >= items.length) next = 0;
      rpActive = next;
      rpPaint();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (rpActive >= 0 && items[rpActive]) { selectItem(items[rpActive]); modal.remove(); }
      return;
    }
    // Esc закрываем здесь же: глобальный обработчик живёт в managed.js, а форма
    // выбора открывается и на автогенерируемых страницах, где его нет.
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); modal.remove(); }
  });
  renderItems(localOpts);
  loadServer('');
  document.getElementById('_rp-list').addEventListener('click', function (e) {
    var item = e.target.closest('._rp-item');
    if (!item) return;
    selectItem(item);
    modal.remove();
  });
  var createBtn = document.getElementById('_rp-create');
  if (createBtn) {
    createBtn.addEventListener('click', function () {
      modal.remove();
      openRefCreate(sel, refEntity);
    });
  }
  document.getElementById('_rp-cancel').addEventListener('click', function () { modal.remove(); });
}

function openRefCurrent(selOrId) {
  var sel = (typeof selOrId === 'string') ? document.getElementById(selOrId) : selOrId;
  if (!sel) return;
  var refEntity = sel.getAttribute('data-ref-entity') || '';
  if (!refEntity || !sel.value) return;
  var refURL = '/ui/_ref-open/' + encodeURIComponent(refEntity) + '/' + encodeURIComponent(sel.value);
  try {
    if (window.__obEmbedded && window.parent && window.parent.obOpenTab) {
      window.parent.postMessage({ source: 'obOpenTab', url: refURL, title: refEntity }, '*');
      return;
    }
  } catch (e) {}
  window.open(refURL, '_blank');
}

function openRefCreate(targetSelect, refEntity) {
  if (!targetSelect || !refEntity) return;
  var old = document.getElementById('_ref-create-modal');
  if (old) old.remove();
  var modal = document.createElement('div');
  modal.id = '_ref-create-modal';
  modal.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.5);z-index:10000;display:flex;align-items:center;justify-content:center';
  var box = document.createElement('div');
  box.style.cssText = 'background:#fff;border-radius:10px;width:780px;max-width:95vw;height:78vh;max-height:680px;display:flex;flex-direction:column;box-shadow:0 12px 40px rgba(0,0,0,.22);overflow:hidden';
  var iframe = document.createElement('iframe');
  iframe.src = '/ui/_ref-create/' + encodeURIComponent(refEntity);
  iframe.style.cssText = 'flex:1;border:0;width:100%';
  box.appendChild(iframe);
  modal.appendChild(box);
  document.body.appendChild(modal);

  function handler(ev) {
    var d = ev.data;
    if (!d || typeof d !== 'object') return;
    if (d.source === 'obRefCreate' && d.id) {
      var exists = false;
      for (var i = 0; i < targetSelect.options.length; i++) {
        if (targetSelect.options[i].value === d.id) {
          exists = true;
          break;
        }
      }
      if (!exists) {
        var o = document.createElement('option');
        o.value = d.id;
        o.textContent = d.label || d.id;
        targetSelect.appendChild(o);
      }
      targetSelect.value = d.id;
      try {
        targetSelect.dispatchEvent(new Event('change', { bubbles: true }));
      } catch (e) {}
      cleanup();
    } else if (d.source === 'obRefCancel') {
      cleanup();
    }
  }
  function cleanup() {
    window.removeEventListener('message', handler);
    modal.remove();
  }
  window.addEventListener('message', handler);
}

// Модальные окна закрываются только явным действием: кнопкой («Отмена», «×»)
// или Esc — но НЕ кликом мимо окна.
//
// Клик по фону закрывал окно и терял введённые данные: браузер шлёт click
// общему предку mousedown и mouseup, поэтому «размашистое» выделение текста
// в поле ввода, где кнопку мыши отпустили уже за границей окна, приходило как
// клик по фону. Проверено в браузере: подбор с введёнными количествами
// закрывался и данные пропадали. Модальные диалоги 1С ведут себя так же —
// мимо окна не закрываются.
//
// Esc для managed-форм обрабатывает managed.js (в фазе перехвата, с отменой
// правки ячейки грида и подтверждением «данные не записаны»); здесь — тот же
// быстрый выход для автогенерируемых форм, которые грузят только ui.js.
// Окно создания элемента (_ref-create-modal) сюда не входит намеренно: это
// форма ввода внутри iframe, у неё свои «Отмена»/«×» и свой Esc с вопросом
// о несохранённых данных.
(function () {
  if (window.__obModalEsc) return;
  window.__obModalEsc = true;
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape' && e.keyCode !== 27) return;
    var modal = document.getElementById('_item-picker-modal') || document.getElementById('_ref-picker-modal');
    if (!modal) return;
    modal.remove();
    e.preventDefault();
    e.stopPropagation();
  });
})();

// onebaseDevice — тонкий мост браузер→локальный device-agent кассира.
// Сервер onebase к агенту не ходит (агент за NAT на машине кассира); ходит
// сам браузер кассира — он на той же машине, что и агент. Адрес и токен агента
// per-машина, поэтому живут в localStorage (см. «Настройки агента»).
window.onebaseDevice = {
  get base() {
    return (localStorage.getItem('obAgentURL') || 'http://127.0.0.1:8765').replace(/\/+$/, '');
  },
  get token() {
    return localStorage.getItem('obAgentToken') || '';
  },
  async call(path, body) {
    const r = await fetch(this.base + path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Agent-Token': this.token }, body: JSON.stringify(body || {}) });
    let d = {};
    try {
      d = await r.json();
    } catch (e) {}
    if (!r.ok) throw new Error(d.error || ('HTTP ' + r.status));
    return d;
  },
  health() {
    return fetch(this.base + '/health').then(function (r) { return r.json(); });
  },
  printReceipt(driver, params, receipt) {
    return this.call('/print', { driver, params, receipt });
  },
  drawer(driver, params) {
    return this.call('/drawer', { driver, params });
  },
  display(driver, params, lines) {
    return this.call('/display', { driver, params, lines });
  },
  weight(driver, params) {
    return this.call('/weight', { driver, params });
  },
  pay(driver, params, amount) {
    return this.call('/pay', { driver, params, amount });
  },
  fiscal(driver, params, receipt) {
    return this.call('/fiscal', { driver, params, receipt });
  },
  // events — SSE-поток сканера ШК в форму. EventSource не шлёт заголовки,
  // поэтому токен и параметры устройства передаются строкой запроса.
  events(driver, params, onCode) {
    const q = new URLSearchParams(Object.assign({ driver: driver, token: this.token }, params || {}));
    const es = new EventSource(this.base + '/events?' + q.toString());
    es.onmessage = function (e) { onCode(e.data, es); };
    return es;
  }
};

/* План 74: real-time-шина уведомлений сервер->браузер.
   Любая страница слушает window-событие 'onebase:<имя>'. Событие
   "уведомление" со строкой показывается тостом без дополнительного кода. */
(function () {
  if (window.__obEventsInit) return;
  window.__obEventsInit = true;
  function afterPaint(fn) {
    var raf = window.requestAnimationFrame || function (cb) { return setTimeout(cb, 0); };
    raf(fn);
  }
  function emitOnebaseEvent(name, data) {
    try {
      if (typeof window.CustomEvent === 'function') {
        window.dispatchEvent(new CustomEvent(name, { detail: data }));
        return;
      }
      var ev = document.createEvent('CustomEvent');
      ev.initCustomEvent(name, false, false, data);
      window.dispatchEvent(ev);
    } catch (_) {}
  }
  // В режиме вкладок SSE принадлежит оболочке. Ретранслируем уже разобранное
  // JSON-событие во все её same-origin iframe, чтобы сохранить публичный
  // контракт window-событий onebase:<имя> для страниц и live-панелей.
  function forwardOnebaseEvent(msg) {
    var frames = document.querySelectorAll('.ob-tabbody iframe');
    for (var i = 0; i < frames.length; i++) {
      try {
        if (frames[i].contentWindow) {
          frames[i].contentWindow.postMessage({
            source: 'obRealtime',
            name: msg.name,
            data: msg.data
          }, window.location.origin);
        }
      } catch (_) {}
    }
  }
  if (window.__obEmbedded) {
    window.addEventListener('message', function (ev) {
      // Принимаем realtime-события только от своей same-origin оболочки.
      if (ev.source !== window.parent || ev.origin !== window.location.origin) return;
      var msg = ev.data;
      if (!msg || msg.source !== 'obRealtime' || typeof msg.name !== 'string' || !msg.name) return;
      emitOnebaseEvent('onebase:' + msg.name, msg.data);
    });
  }
  function toast(text) {
    var box = document.getElementById('ob-toasts');
    if (!box) {
      box = document.createElement('div');
      box.id = 'ob-toasts';
      box.style.cssText = 'position:fixed;right:16px;bottom:16px;z-index:9999;display:flex;flex-direction:column;gap:8px;max-width:360px';
      (document.body || document.documentElement).appendChild(box);
    }
    var el = document.createElement('div');
    el.style.cssText = 'background:#1f2937;color:#fff;padding:10px 14px;border-radius:8px;box-shadow:0 6px 16px rgba(0,0,0,.25);font-size:14px;line-height:1.35;opacity:0;transition:opacity .2s';
    el.textContent = text;
    box.appendChild(el);
    afterPaint(function () { el.style.opacity = '1'; });
    setTimeout(function () {
      el.style.opacity = '0';
      setTimeout(function () { el.remove(); }, 250);
    }, 6000);
  }
  /* План 75 (телефония/CTI): входящий звонок -> «скрин-поп» на любой странице.
     Конфигурация публикует ОтправитьУведомление(логин,"звонок.входящий",
     {номер,клиент,ссылка,id}); здесь рисуем тост с именем клиента и ссылкой на
     карточку. Слушатель безвреден вне телефонии: срабатывает только на это
     событие. DOM собираем textContent/href — без innerHTML (защита от XSS). */
  function callToast(d) {
    d = d || {};
    var box = document.getElementById('ob-toasts');
    if (!box) {
      box = document.createElement('div');
      box.id = 'ob-toasts';
      box.style.cssText = 'position:fixed;right:16px;bottom:16px;z-index:9999;display:flex;flex-direction:column;gap:8px;max-width:360px';
      (document.body || document.documentElement).appendChild(box);
    }
    var el = document.createElement('div');
    el.style.cssText = 'position:relative;background:#065f46;color:#fff;padding:12px 28px 12px 14px;border-radius:8px;box-shadow:0 6px 16px rgba(0,0,0,.3);font-size:14px;line-height:1.4';
    var head = document.createElement('div');
    head.style.cssText = 'font-weight:600;margin-bottom:4px';
    head.textContent = '📞 Входящий звонок';
    el.appendChild(head);
    var line = document.createElement('div');
    line.textContent = (d['номер'] || '') + (d['клиент'] ? (' — ' + d['клиент']) : '');
    el.appendChild(line);
    var url = d['ссылка'];
    if (typeof url === 'string' && url.charAt(0) === '/') {
      var a = document.createElement('a');
      a.href = url;
      a.textContent = 'Открыть карточку клиента';
      a.style.cssText = 'display:inline-block;margin-top:6px;color:#a7f3d0;text-decoration:underline';
      el.appendChild(a);
    }
    var x = document.createElement('button');
    x.textContent = '×';
    x.setAttribute('aria-label', 'Закрыть');
    x.style.cssText = 'position:absolute;top:4px;right:8px;background:none;border:none;color:#fff;font-size:18px;line-height:1;cursor:pointer';
    x.onclick = function () { el.remove(); };
    el.appendChild(x);
    box.appendChild(el);
    setTimeout(function () {
      if (el.parentNode) el.remove();
    }, 20000);
  }
  // Событие ретранслируется и во фреймы для пользовательских слушателей, но
  // встроенную всплывашку рисует только оболочка — иначе снова будут дубли.
  if (!window.__obEmbedded) {
    window.addEventListener('onebase:звонок.входящий', function (ev) { callToast(ev.detail); });
  }

  /* Ступень B (план 87): клиентские команды ui.*. Рисует/исполняет только
     оболочка (как callToast) — событие ретранслируется во фреймы, но всплывашку и
     открытие вкладки делает верхнее окно, иначе дубли. */
  function formURL(link) {
    link = link || {};
    var kind = String(link['вид'] || link.kind || 'document').toLowerCase();
    var ent = link['сущность'] || link.entity || '';
    var id = link['id'] || link.id || '';
    if (!ent) return '';
    if (kind === 'processor' || kind === 'report' || kind === 'page') {
      return '/ui/' + kind + '/' + encodeURIComponent(String(ent));
    }
    var base = '/ui/' + kind + '/' + encodeURIComponent(String(ent).toLowerCase());
    return id ? base + '/' + encodeURIComponent(String(id)) : base;
  }
  function openFormTab(link) {
    var url = formURL(link);
    if (!url) return;
    var title = (link && (link['сущность'] || link.entity)) || '';
    try {
      // Вкладочная оболочка в этом окне.
      if (typeof window.obOpenTab === 'function') { window.obOpenTab(url, title); return; }
      // Мы во фрейме оболочки — просим родителя открыть вкладку.
      if (window.parent && window.parent !== window && typeof window.parent.obOpenTab === 'function') {
        window.parent.postMessage({ source: 'obOpenTab', url: url, title: title }, window.location.origin);
        return;
      }
    } catch (_) {}
    // Нет оболочки (например нативное GUI-окно): переходим в ТЕКУЩЕМ окне, а не
    // через window.open — иначе WebView2 откроет внешнее окно/браузер с базой.
    window.location.assign(url);
  }
  // Богатый тост (аналог ПоказатьОповещениеПользователя): заголовок/текст,
  // «важное» не исчезает само, клик по тосту со ссылкой открывает форму.
  function richToast(d) {
    d = d || {};
    var box = document.getElementById('ob-toasts');
    if (!box) {
      box = document.createElement('div');
      box.id = 'ob-toasts';
      box.style.cssText = 'position:fixed;right:16px;bottom:16px;z-index:9999;display:flex;flex-direction:column;gap:8px;max-width:360px';
      (document.body || document.documentElement).appendChild(box);
    }
    var important = String(d['важность'] || '') === 'важное';
    var link = d['ссылка'];
    var el = document.createElement('div');
    el.style.cssText = 'position:relative;background:' + (important ? '#7c2d12' : '#1f2937') + ';color:#fff;padding:12px 28px 12px 14px;border-radius:8px;box-shadow:0 6px 16px rgba(0,0,0,.3);font-size:14px;line-height:1.4' + (link ? ';cursor:pointer' : '');
    if (d['заголовок']) {
      var head = document.createElement('div');
      head.style.cssText = 'font-weight:600;margin-bottom:4px';
      head.textContent = d['заголовок'];
      el.appendChild(head);
    }
    if (d['текст']) {
      var body = document.createElement('div');
      body.textContent = d['текст'];
      el.appendChild(body);
    }
    if (link) { el.addEventListener('click', function () { openFormTab(link); }); }
    var x = document.createElement('button');
    x.textContent = '×';
    x.setAttribute('aria-label', 'Закрыть');
    x.style.cssText = 'position:absolute;top:4px;right:8px;background:none;border:none;color:#fff;font-size:18px;line-height:1;cursor:pointer';
    x.addEventListener('click', function (e) { e.stopPropagation(); el.remove(); });
    el.appendChild(x);
    box.appendChild(el);
    if (!important) {
      setTimeout(function () { if (el.parentNode) el.remove(); }, 8000);
    }
  }
  if (!window.__obEmbedded) {
    window.addEventListener('onebase:ui.оповещение', function (ev) { richToast(ev.detail); });
    window.addEventListener('onebase:ui.открытьФорму', function (ev) { openFormTab(ev.detail); });
  }
  // BEGIN onebase-dev-system-handler (executed directly by the Node regression test)
  function obHandleDevSystem(msg, devEnabled, state, reload) {
    if (!devEnabled || !msg || !msg.system) return false;
    if (msg.system === 'dev-generation') {
      if (state.generation !== null && state.generation !== msg.data) reload();
      state.generation = msg.data;
      return true;
    }
    if (msg.system === 'dev-reload') {
      reload();
      return true;
    }
    return false;
  }
  // END onebase-dev-system-handler
  var sseOpened = false;
  var devState = { generation: null };
  function connect() {
    if (typeof EventSource === 'undefined') return;
    var es = new EventSource('/ui/events');
    window.__obEvents = es;
    es.onopen = function () {
      // План 87: служебные события данные.* не переигрываются из replay-окна.
      // После РЕконнекта (не первого open) просим живые списки перечитаться —
      // один актуальный GET безопаснее, чем догадка по устаревшему снимку прав.
      if (sseOpened) {
        emitOnebaseEvent('onebase:__oblive_refresh_all__', null);
        forwardOnebaseEvent({ name: '__oblive_refresh_all__', data: null });
      }
      sseOpened = true;
    };
    es.onmessage = function (ev) {
      var msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (e) {
        return;
      }
      if (!msg) return;
      // Only the server can create a system envelope, and the rendered page
      // must independently opt in to dev behavior. A DSL notification with a
      // look-alike name remains an ordinary onebase:<name> event.
      var devEnabled = !!(document.body && document.body.getAttribute('data-ob-dev') === '1');
      if (obHandleDevSystem(msg, devEnabled, devState, function () { location.reload(); })) return;
      if (!msg.name) return;
      emitOnebaseEvent('onebase:' + msg.name, msg.data);
      forwardOnebaseEvent(msg);
      if (msg.name === 'уведомление' || msg.name === 'notify') {
        toast(typeof msg.data === 'string' ? msg.data : JSON.stringify(msg.data));
      }
    };
    es.onerror = function () {};
  }
  // Вкладочная оболочка (issue #322/#323): единственный SSE-поток /ui/events
  // держит верхнее окно оболочки. Во фрейме не подключаемся — иначе N вкладок =
  // N постоянных соединений (упор в лимит браузера ~6/хост) и дубли тостов
  // (Hub.Publish доставляет каждому подписчику). Произвольные onebase:<имя>
  // события оболочка ретранслирует во фреймы через проверенный postMessage.
  if (!window.__obEmbedded) {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', connect);
    else connect();
  }
})();

/* План 87, ступень A — «Живой список». Контейнер списка помечается
   data-ob-refresh-on="имя1 имя2" (+ data-ob-live="ключ" для сопоставления при
   перечитывании). Универсальный слушатель ловит window-событие onebase:<имя>,
   помечает подписанные контейнеры dirty и перечитывает тем же GET (как F5), но с
   debounce и только в видимой вкладке. Ни одного НОВОГО обязательного запроса:
   перечитывание — тот же GET, что делает пользователь по F5, реже и лишь когда
   видно. Событие __oblive_refresh_all__ (SSE reconnect) помечает dirty все. */
(function () {
  if (window.__obLiveListInit) return;
  window.__obLiveListInit = true;
  var DEBOUNCE = 700;
  var timers = {}; // key -> timeout id
  var dirty = {};  // key -> true, ждёт видимости вкладки

  function liveContainers() { return document.querySelectorAll('[data-ob-refresh-on]'); }
  function keyOf(el, idx) { return el.getAttribute('data-ob-live') || ('__idx' + idx); }
  function eventsOf(el) {
    return (el.getAttribute('data-ob-refresh-on') || '').split(/\s+/).filter(Boolean);
  }
  function findByKey(root, key) {
    // data-ob-live есть у всех списков (и статичных) — так императивный
    // ui.обновитьСписок находит контейнер, даже если он не подписан декларативно.
    var all = root.querySelectorAll('[data-ob-live]');
    for (var i = 0; i < all.length; i++) {
      if (keyOf(all[i], i) === key) return all[i];
    }
    return null;
  }

  function doRefresh(el, key) {
    var src = el.getAttribute('data-ob-refresh-src') || window.location.href;
    fetch(src, { credentials: 'same-origin', headers: { 'X-Requested-With': 'obLiveList' } })
      .then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, 'text/html');
        var cur = findByKey(document, key);
        var fresh = findByKey(doc, key);
        if (!cur || !fresh) return;
        var sc = cur.scrollTop;
        // Строки заменяются целиком, поэтому выбранная строка отцепляется от
        // документа. Запоминаем запись до замены и возвращаем выделение на неё
        // после — иначе автообновление молча съедало бы выбор пользователя.
        // Трогаем выделение, только если оно жило в ЭТОМ списке: на странице
        // может быть несколько живых списков, чужой выбор не наше дело.
        obReplaceLiveListContents(cur, fresh); // содержимое; атрибуты контейнера сохраняются
        try { cur.scrollTop = sc; } catch (_) {}
      })
      .catch(function () {}); // офлайн/редирект логина — тихо, F5 пользователя выручит
  }

  // Помечаем контейнер dirty и планируем перечитывание (или откладываем до
  // видимости вкладки). Склейка пачки событий в один GET допустима: семантика —
  // «показать актуальное состояние», от объединения не страдает.
  function schedule(key) {
    if (document.visibilityState === 'hidden') { dirty[key] = true; return; }
    if (timers[key]) clearTimeout(timers[key]);
    timers[key] = setTimeout(function () {
      timers[key] = null;
      var el = findByKey(document, key);
      if (el) doRefresh(el, key);
    }, DEBOUNCE);
  }

  function markAll() {
    var cs = liveContainers();
    for (var i = 0; i < cs.length; i++) schedule(keyOf(cs[i], i));
  }

  // Обработчик конкретного события: помечает те контейнеры, что на него подписаны.
  function onNamed(name) {
    var cs = liveContainers();
    for (var i = 0; i < cs.length; i++) {
      if (eventsOf(cs[i]).indexOf(name) >= 0) schedule(keyOf(cs[i], i));
    }
  }

  // Собираем уникальные имена событий со страницы и вешаем по слушателю на каждое.
  var bound = {};
  function bindEvents() {
    var cs = liveContainers();
    for (var i = 0; i < cs.length; i++) {
      var names = eventsOf(cs[i]);
      for (var j = 0; j < names.length; j++) {
        (function (name) {
          if (bound[name]) return;
          bound[name] = true;
          window.addEventListener('onebase:' + name, function () { onNamed(name); });
        })(names[j]);
      }
    }
  }

  // Скрытая вкладка копит dirty; при возврате — один перечитывающий GET.
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState !== 'visible') return;
    var keys = Object.keys(dirty);
    dirty = {};
    for (var i = 0; i < keys.length; i++) schedule(keys[i]);
  });

  // SSE reconnect: события данные.* не переигрываются → перечитать все живые списки.
  window.addEventListener('onebase:__oblive_refresh_all__', markAll);

  // Ступень B — императивная команда «обнови список сейчас»: ui.обновитьСписок
  // ({сущность}) дёргает контейнеры этой сущности независимо от YAML-подписки.
  function refreshByEntity(name) {
    if (!name) return;
    var want = String(name).toLowerCase();
    var all = document.querySelectorAll('[data-ob-live]');
    for (var i = 0; i < all.length; i++) {
      var key = all[i].getAttribute('data-ob-live') || '';
      var slash = key.lastIndexOf('/');
      var ename = slash >= 0 ? key.slice(slash + 1) : key;
      if (ename === want) schedule(key);
    }
  }
  window.addEventListener('onebase:ui.обновитьСписок', function (ev) {
    var d = ev.detail || {};
    refreshByEntity(d['сущность'] || d.entity);
  });

  function init() { bindEvents(); }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();

/* ===== Боковая панель деталей активной записи (план 118B, issue #670) =====

   Журналы и регистры используют уже проверенный inline payload. Списки сущностей
   запрашивают выбранную запись лениво: endpoint заново проверяет объектные права,
   строковые политики, серверный read-hook формы и маску ПДн.

   Живёт СНАРУЖИ контейнера [data-ob-live]: живой список подменяет его innerHTML
   целиком, и панель внутри пересоздавалась бы, теряя вкладку и ширину.

   Состояние (включена, ширина, активная вкладка) — в localStorage по объекту,
   как ширина панелей конфигуратора. Серверная настройка потребовала бы нового
   POST-маршрута и записи в БД на каждое перетаскивание ручки. */

var OB_DETAIL_MIN = 220, OB_DETAIL_MAX = 640;

function obDetailKey(suffix) {
  var live = document.querySelector('[data-ob-live]');
  var scope = live ? live.getAttribute('data-ob-live') : (location.pathname || 'list');
  return 'obdetail:' + scope + ':' + suffix;
}

function obDetailStore(suffix, value) {
  try { localStorage.setItem(obDetailKey(suffix), value); } catch (e) {}
}

function obDetailRead(suffix) {
  try { return localStorage.getItem(obDetailKey(suffix)); } catch (e) { return null; }
}

function obDetailEl() { return document.getElementById('ob-detail'); }

function obDetailEnabled() { return obDetailRead('on') === '1'; }

// obDetailRender перерисовывает панель по выбранной строке. Вызывается из
// listSetSel, поэтому стрелки ↑↓ двигают курсор — панель следует за ним.
// obDetailCache хранит последний загруженный payload панели: одна строка за раз,
// как и сама панель. Больше не нужно — переключение строк перезапрашивает.
// BEGIN onebase-detail-fetch
var obDetailCache = { url: '', body: '' };
var obDetailRequestSeq = 0;
var obDetailPending = { url: '', controller: null };

function obDetailInvalidate() {
  obDetailRequestSeq++;
  if (obDetailPending.controller) obDetailPending.controller.abort();
  obDetailPending = { url: '', controller: null };
  obDetailCache = { url: '', body: '' };
}

// obDetailFetch подгружает payload панели для строки и перерисовывает её.
// Ошибку показываем в самой панели: молча пустая панель неотличима от «у записи
// нет данных», и человек будет считать, что так и надо.
function obDetailFetch(row, url) {
  var panel = obDetailEl();
  if (!panel) return;
  // Повторный render той же строки не должен порождать второй параллельный GET.
  if (obDetailPending.url === url) return;
  obDetailRequestSeq++;
  if (obDetailPending.controller) obDetailPending.controller.abort();
  var seq = obDetailRequestSeq;
  var controller = (typeof AbortController === 'function') ? new AbortController() : null;
  obDetailPending = { url: url, controller: controller };
  var fieldsEl = panel.querySelector('[data-ob-detail-fields]');
  var emptyEl = panel.querySelector('[data-ob-detail-empty]');
  if (emptyEl) emptyEl.hidden = true;
  if (fieldsEl) fieldsEl.textContent = '…';
  var options = { credentials: 'same-origin', headers: { 'X-Onebase-Ajax': '1' } };
  if (controller) options.signal = controller.signal;
  fetch(url, options)
    .then(function (resp) {
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      return resp.text();
    })
    .then(function (body) {
      if (seq !== obDetailRequestSeq) return;
      obDetailPending = { url: '', controller: null };
      // Строка могла смениться, пока ответ шёл. Старый ответ не должен даже
      // попадать в общий кэш, иначе следующий render покажет чужую версию.
      var current = (typeof listSel === 'function') ? listSel() : null;
      if (!current || current.getAttribute('data-ob-detail-url') !== url) return;
      obDetailCache = { url: url, body: body };
      obDetailRender();
    })
    .catch(function (err) {
      if (seq !== obDetailRequestSeq) return;
      obDetailPending = { url: '', controller: null };
      if (err && err.name === 'AbortError') return;
      var current = (typeof listSel === 'function') ? listSel() : null;
      if (!current || current.getAttribute('data-ob-detail-url') !== url) return;
      obDetailCache = { url: '', body: '' };
      if (fieldsEl) fieldsEl.textContent = 'Не удалось загрузить детали: ' + err.message;
    });
}
// END onebase-detail-fetch

function obDetailRender() {
  var panel = obDetailEl();
  if (!panel) return;
  if (!obDetailEnabled()) { panel.hidden = true; return; }
  panel.hidden = false;

  var titleEl = panel.querySelector('[data-ob-detail-title]');
  var tabsEl = panel.querySelector('[data-ob-detail-tabs]');
  var fieldsEl = panel.querySelector('[data-ob-detail-fields]');
  var emptyEl = panel.querySelector('[data-ob-detail-empty]');
  var row = (typeof listSel === 'function') ? listSel() : null;
  // Payload берётся либо из разметки (журналы и регистры сведений: там в панели
  // ровно те поля, что уже показаны в таблице), либо отдельным запросом
  // (списки сущностей: полная карточка не должна лежать в DOM каждой строки —
  // #860). Ответ кэшируется на строку: переключение закладок не должно
  // дёргать сервер.
  var raw = row ? row.getAttribute('data-ob-detail') : '';
  var lazyURL = row ? row.getAttribute('data-ob-detail-url') : '';
  if (!raw && lazyURL) {
    if (obDetailCache.url === lazyURL) {
      raw = obDetailCache.body;
    } else {
      obDetailFetch(row, lazyURL);
      return;
    }
  }
  var data = null;
  if (raw) { try { data = JSON.parse(raw); } catch (e) { data = null; } }

  if (!data || !data.tabs || !data.tabs.length) {
    if (titleEl) titleEl.textContent = '';
    if (tabsEl) tabsEl.innerHTML = '';
    if (fieldsEl) fieldsEl.innerHTML = '';
    if (emptyEl) emptyEl.hidden = false;
    return;
  }
  if (emptyEl) emptyEl.hidden = true;
  if (titleEl) titleEl.textContent = data.title || '';

  function tabKey(tab) { return tab.key || tab.title; }
  var active = obDetailRead('tab') || tabKey(data.tabs[0]);
  var found = false;
  data.tabs.forEach(function (t) { if (tabKey(t) === active) found = true; });
  if (!found) active = tabKey(data.tabs[0]);

  if (tabsEl) {
    tabsEl.innerHTML = '';
    if (data.tabs.length > 1) {
      data.tabs.forEach(function (tab) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'ob-detail-tab' + (tabKey(tab) === active ? ' active' : '');
        btn.textContent = tab.title;
        btn.addEventListener('click', function () {
          obDetailStore('tab', tabKey(tab));
          obDetailRender();
        });
        tabsEl.appendChild(btn);
      });
    }
  }

  if (fieldsEl) {
    fieldsEl.innerHTML = '';
    data.tabs.forEach(function (tab) {
      if (tabKey(tab) !== active) return;
      (tab.fields || []).forEach(function (f) {
        var wrap = document.createElement('div');
        wrap.className = 'ob-detail-field';
        var label = document.createElement('span');
        label.className = 'ob-detail-label';
        label.textContent = f.label;
        wrap.appendChild(label);
        if (f.kind === 'image' && f.value) {
          var img = document.createElement('img');
          img.className = 'ob-detail-image';
          img.src = '/ui/_image/' + encodeURIComponent(f.value);
          img.alt = f.label;
          wrap.appendChild(img);
        } else {
          var val = document.createElement('span');
          val.className = 'ob-detail-value';
          val.textContent = f.value || '—';
          wrap.appendChild(val);
        }
        fieldsEl.appendChild(wrap);
      });
    });
  }
}

function obDetailApplyWidth() {
  var panel = obDetailEl();
  if (!panel) return;
  var saved = obDetailRead('w');
  var configured = panel.getAttribute('data-ob-default-width') || '320';
  var w = parseInt(saved === null ? configured : saved, 10);
  if (isNaN(w) || w === 0) w = 320;
  w = Math.max(OB_DETAIL_MIN, Math.min(OB_DETAIL_MAX, w));
  panel.style.width = w + 'px';
}

function obDetailToggle(on) {
  obDetailStore('on', on ? '1' : '0');
  if (!on) obDetailInvalidate();
  var btn = document.querySelector('[data-ob-detail-toggle]');
  if (btn) {
    btn.classList.toggle('active', on);
    btn.setAttribute('aria-expanded', on ? 'true' : 'false');
  }
  obDetailApplyWidth();
  obDetailRender();
}

function initDetailPanel() {
  var panel = obDetailEl();
  if (!panel) return;
  var btn = document.querySelector('[data-ob-detail-toggle]');
  if (btn) {
    btn.classList.toggle('active', obDetailEnabled());
    btn.setAttribute('aria-expanded', obDetailEnabled() ? 'true' : 'false');
    btn.addEventListener('click', function (e) {
      e.preventDefault();
      obDetailToggle(!obDetailEnabled());
    });
  }
  var close = panel.querySelector('[data-ob-detail-close]');
  if (close) close.addEventListener('click', function () { obDetailToggle(false); });

  var grip = panel.querySelector('[data-ob-detail-grip]');
  if (grip) {
    var startX = 0, startW = 0, dragging = false;
    grip.addEventListener('mousedown', function (e) {
      dragging = true; startX = e.clientX; startW = panel.offsetWidth;
      e.preventDefault();
    });
    document.addEventListener('mousemove', function (e) {
      if (!dragging) return;
      var w = Math.max(OB_DETAIL_MIN, Math.min(OB_DETAIL_MAX, startW + (startX - e.clientX)));
      panel.style.width = w + 'px';
    });
    document.addEventListener('mouseup', function () {
      if (!dragging) return;
      dragging = false;
      obDetailStore('w', String(panel.offsetWidth));
    });
  }
  obDetailApplyWidth();
  obDetailRender();
}

/* Стили боковой панели деталей (план 118B). Живут рядом с остальным
   JS-строимым UI: страницы со своим <head> без общего стиля приложения
   получают панель в рабочем виде. */
(function () {
  var css = '' +
    '.ob-list-wrap{display:flex;gap:12px;align-items:flex-start}' +
    '.ob-list-wrap>.card{flex:1 1 auto;min-width:0}' +
    '.ob-detail{position:relative;flex:0 0 auto;width:320px;background:#fff;border:1px solid #e2e8f0;' +
    'border-radius:8px;padding:0;align-self:stretch;max-height:calc(100vh - 160px);overflow:auto}' +
    '.ob-detail-grip{position:absolute;left:-4px;top:0;bottom:0;width:8px;cursor:col-resize}' +
    '.ob-detail-body{padding:12px 14px}' +
    '.ob-detail-head{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:8px}' +
    '.ob-detail-close{border:0;background:transparent;color:#94a3b8;cursor:pointer;font-size:14px}' +
    '.ob-detail-tabs{display:flex;gap:4px;flex-wrap:wrap;margin-bottom:10px}' +
    '.ob-detail-tab{border:1px solid #e2e8f0;background:#f8fafc;border-radius:6px;padding:3px 9px;' +
    'font-size:12px;cursor:pointer;color:#475569}' +
    '.ob-detail-tab.active{background:#e0e7ff;border-color:#c7d2fe;color:#3730a3}' +
    '.ob-detail-field{display:flex;gap:8px;padding:4px 0;border-bottom:1px solid #f1f5f9;font-size:13px}' +
    '.ob-detail-label{flex:0 0 42%;color:#64748b}' +
    '.ob-detail-value{flex:1 1 auto;word-break:break-word}' +
    '.ob-detail-image{max-width:100%;border-radius:6px;margin-top:4px}' +
    '.ob-detail-empty{color:#94a3b8;font-size:13px;margin:4px 0 0}' +
    /* Узкий экран: третья колонка недопустима — панель уходит под список. */
    '@media(max-width:820px){.ob-list-wrap{flex-direction:column}.ob-detail{width:auto!important;max-height:none}}';
  var st = document.createElement('style');
  st.textContent = css;
  (document.head || document.documentElement).appendChild(st);
})();
