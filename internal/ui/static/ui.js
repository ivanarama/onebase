// Вкладочная оболочка (issue #129/#130): когда страница открыта во фрейме
// оболочки /ui/app, прячем хром (топбар/подсистемы) — навигация идёт из оболочки.
window.__obEmbedded = window.self !== window.top;
if (window.__obEmbedded) {
  document.documentElement.className += ' ob-embedded';
  // Фаза 2: открытие записи/новой формы/отчёта внутри вкладки — это новая
  // вкладка рядом, а не замена текущей (пагинация/сортировка/фильтры остаются
  // в той же вкладке — у них тот же путь списка, без id-сегмента).
  var obOpenableForm = function (href) {
    if (!/^\/ui\//.test(href)) return false;
    if (/^\/ui\/(admin|about|logout|login|logo|debug|app|_)/.test(href)) return false;
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
  document.addEventListener('click', function (e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target.closest ? e.target.closest('a[href]') : null;
    if (!a || a.target === '_blank') return;
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
  var nextComp = { Groupings: groupings, Measures: measures, Appearance: { lines: lines, zebra: zebra } };
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

function obInitReportCompositionControls() {
  function rcEscape(key) {
    return (window.CSS && CSS.escape) ? CSS.escape(key) : key.replace(/["\\\]]/g, '\\$&');
  }
  function rcSetOpen(tr, open) {
    var key = tr.getAttribute('data-group');
    var ek = rcEscape(key);
    var cell = tr.querySelector('td');
    var sel = '[data-parent="' + ek + '"],[data-parent^="' + ek + '/"],[data-group^="' + ek + '/"]';
    document.querySelectorAll(sel).forEach(function (el) { el.style.display = open ? '' : 'none'; });
    if (cell) cell.textContent = (open ? '▼' : '▶') + cell.textContent.slice(1);
  }
  document.querySelectorAll('tr.grp').forEach(function (tr) {
    tr.style.cursor = 'pointer';
    tr.addEventListener('click', function () {
      var cell = tr.querySelector('td');
      var open = cell.textContent.trim().charAt(0) === '▼';
      rcSetOpen(tr, !open);
    });
  });
  var expandBtn = document.getElementById('rc-expand');
  var collapseBtn = document.getElementById('rc-collapse');
  if (expandBtn) {
    expandBtn.addEventListener('click', function () {
      var tbody = document.querySelector('table.report-composed tbody');
      if (!tbody) return;
      tbody.querySelectorAll('tr').forEach(function (tr) { tr.style.display = ''; });
      tbody.querySelectorAll('tr.grp').forEach(function (tr) {
        var cell = tr.querySelector('td');
        if (cell && cell.textContent.trim().charAt(0) === '▶') {
          cell.textContent = '▼' + cell.textContent.slice(1);
        }
      });
    });
  }
  if (collapseBtn) {
    collapseBtn.addEventListener('click', function () {
      var tbody = document.querySelector('table.report-composed tbody');
      if (!tbody) return;
      tbody.querySelectorAll('tr.det,tr.subtotal').forEach(function (tr) { tr.style.display = 'none'; });
      tbody.querySelectorAll('tr.grp').forEach(function (tr) {
        var level = parseInt(tr.getAttribute('data-level') || '0', 10);
        if (level > 0) {
          tr.style.display = 'none';
        } else {
          var cell = tr.querySelector('td');
          if (cell && cell.textContent.trim().charAt(0) === '▼') {
            cell.textContent = '▶' + cell.textContent.slice(1);
          }
        }
      });
    });
  }
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
    var row = e.target.closest('[data-ob-journal-open-url]');
    if (row) {
      if (e.target.closest('a,button')) return;
      window.location.href = row.getAttribute('data-ob-journal-open-url') || '#';
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

var _listSel = null;

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

function listRowClick(e, tr) {
  if (e.target.closest('a,button')) return;
  if (_listSel) {
    _listSel.querySelectorAll('td').forEach(function (td) { td.style.background = ''; });
    _listSel.classList.remove('tile-selected');
  }
  _listSel = tr;
  tr.querySelectorAll('td').forEach(function (td) { td.style.background = '#dbeafe'; });
  tr.classList.add('tile-selected');
}

function listRowDblClick(e, tr) {
  if (e.target.closest('a,button')) return;
  if (tr.dataset.isFolder === '1') window.location.href = tr.dataset.folderUrl;
  else listOpen(tr.dataset.openUrl);
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
    treeSetVisible(fid, false);
    btn.setAttribute('data-expanded', '0');
    btn.textContent = '▶';
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
      treeSetVisible(childId, visible && row.dataset.isFolder !== '1' || row.querySelector('.tree-toggle[data-expanded="1"]') !== null);
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
  tr.setAttribute('data-ob-list-row', '');
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
    if (item.disabled) {
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

function listActionsBtnClick(e, btn) {
  e.preventDefault();
  if (!_listSel) {
    alert(obListLabel('selectRowFirst', 'Сначала выберите строку списка'));
    return;
  }
  var r = (btn || e.currentTarget).getBoundingClientRect();
  showListMenu(listMenuItems(_listSel), r.left, r.bottom);
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
      input.form.submit();
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
  document.querySelectorAll('.tree-toggle').forEach(initTreeToggle);
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Delete' && _listSel && obListConfig().canDelete) {
      listSubmit(_listSel.dataset.markUrl, obListLabel('markDeleteConfirm', 'Пометить на удаление?'));
    }
  });
  obInitFeed();
});

(function () {
  if (window.__obAiInit) return;
  window.__obAiInit = true;
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
    panel.innerHTML = '<div id="ob-ai-head"><span>🤖 ИИ-помощник</span><span class="sp"></span><button type="button" id="ob-ai-close" title="Закрыть">×</button></div>' +
      '<div id="ob-ai-log"><div class="hint">Спросите про данные, отчёт или как что-то сделать.</div></div>' +
      '<div id="ob-ai-foot"><textarea id="ob-ai-input" rows="1" placeholder="Ваш вопрос…"></textarea><button id="ob-ai-send" type="button" title="Отправить">▶</button></div>';
    document.body.appendChild(btn);
    document.body.appendChild(panel);
    var log = document.getElementById('ob-ai-log');
    var input = document.getElementById('ob-ai-input');
    var send = document.getElementById('ob-ai-send');
    var history = [];
    var busy = false;
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
    function addMsg(role, text) {
      var h = log.querySelector('.hint');
      if (h) h.remove();
      var d = document.createElement('div');
      d.className = 'm ' + (role === 'user' ? 'u' : role === 'error' ? 'err' : 'a');
      d.textContent = text;
      log.appendChild(d);
      log.scrollTop = log.scrollHeight;
      return d;
    }
    function doSend() {
      var t = input.value.trim();
      if (!t || busy) return;
      input.value = '';
      addMsg('user', t);
      history.push({ role: 'user', content: t });
      busy = true;
      send.disabled = true;
      var pend = addMsg('assistant', '…');
      fetch('/ui/ai/chat', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ messages: history }) })
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d && d.ok) {
            pend.textContent = d.text;
            history.push({ role: 'assistant', content: d.text });
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
    load();
    setInterval(load, 3000);
    document.addEventListener('submit', function () { setTimeout(load, 400); }, true);
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
      openRefCurrent(refCurrent.getAttribute('data-ob-ref-current') || '');
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
      if (row) row.remove();
      return;
    }
    var addTp = e.target.closest('[data-ob-add-tp-row]');
    if (addTp) {
      e.preventDefault();
      var tpName = addTp.getAttribute('data-tp-name') || '';
      var tbody = document.getElementById('tp-body-' + tpName);
      addTpRow(tpName, obSplitDataList(addTp.getAttribute('data-tp-fields')), obSplitDataList(addTp.getAttribute('data-tp-num-fields')), tbody ? tbody.rows.length : 0);
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

function addTpRow(tpName, fields, numFields, idx) {
  var tbody = document.getElementById('tp-body-' + tpName);
  var tr = document.createElement('tr');
  var refOpts = (obTPRefOpts()[tpName]) || {};
  var refMeta = (obTPRefMeta()[tpName]) || {};
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
  var tdDel = document.createElement('td');
  var btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'del-btn';
  btn.textContent = '×';
  btn.setAttribute('data-ob-remove-row', 'tr');
  tdDel.appendChild(btn);
  tr.appendChild(tdDel);
  tbody.appendChild(tr);
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

function openItemPicker(payload, elementName) {
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
  modal.addEventListener('click', function (e) { if (e.target === modal) modal.remove(); });
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
      obFire(elementName, 'Выбор', { _pick_result: JSON.stringify(result) });
    }
  });
}

function openRefPicker(selOrId) {
  var sel = (typeof selOrId === 'string') ? document.getElementById(selOrId) : selOrId;
  if (!sel) return;
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
  function renderItems(opts) {
    if (!list) return;
    list.innerHTML = '';
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
      list.appendChild(item);
    }
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
        var rows = (data && data.items) || [];
        var opts = rows.map(function (row) {
          var id = row && row.id != null ? String(row.id) : '';
          return { id: id, label: String((row && row._label) || id) };
        }).filter(function (opt) { return opt.id !== ''; });
        renderItems(opts);
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
  modal.addEventListener('click', function (e) {
    if (e.target === modal) modal.remove();
  });
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
  modal.addEventListener('click', function (e) {
    if (e.target === modal) cleanup();
  });
}

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
    requestAnimationFrame(function () { el.style.opacity = '1'; });
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
  window.addEventListener('onebase:звонок.входящий', function (ev) { callToast(ev.detail); });
  function connect() {
    if (typeof EventSource === 'undefined') return;
    var es = new EventSource('/ui/events');
    window.__obEvents = es;
    es.onmessage = function (ev) {
      var msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (e) {
        return;
      }
      if (!msg || !msg.name) return;
      window.dispatchEvent(new CustomEvent('onebase:' + msg.name, { detail: msg.data }));
      if (msg.name === 'уведомление' || msg.name === 'notify') {
        toast(typeof msg.data === 'string' ? msg.data : JSON.stringify(msg.data));
      }
    };
    es.onerror = function () {};
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', connect);
  else connect();
})();
