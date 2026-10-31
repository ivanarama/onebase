/* Read-only form projections: each DOM instance owns its revision. */
(function () {
  'use strict';
  var bootstrap = document.getElementById('ob-managed-config');
  if (!bootstrap) return;
  var cfg; try { cfg = JSON.parse(bootstrap.textContent); } catch (_) { return; }
  var listBootstrap = document.getElementById('ob-entity-list-config');
  if (!listBootstrap) return;
  var listConfig; try { listConfig = JSON.parse(listBootstrap.textContent); } catch (_) { return; }
  cfg.entityListForm = listConfig.form; cfg.entityListState = listConfig.state; cfg.entityListMessages = listConfig.messages;
  var form = document.getElementById('main-form');
  if (!form || !cfg.entityListForm) return;
  var nodes = Array.from(form.querySelectorAll('.ob-entity-list'));
  if (!nodes.length) return;
  var token = cfg.entityListState || '';
  var messages = cfg.entityListMessages || {};
  var generation = 0;
  var disclosureExpires = Object.create(null);
  function sourceControls(node) {
    var paths = JSON.parse(node.getAttribute('data-ob-list-sources') || '{}');
    return Array.from(form.querySelectorAll('[data-ob-concealed]')).filter(function(el) {
      return Object.keys(paths).some(function(path) { return String(paths[path]).toLowerCase() === String(el.name).toLowerCase(); });
    });
  }
  function concealed(node) { return sourceControls(node).some(function(el) { return el.disabled; }); }
  function concealChanged() {
    nodes.forEach(function(node) {
      if (!concealed(node)) return;
      generation++; invalidate(node); state(node, 'denied');
    });
  }

  function sources(node) {
    var paths = JSON.parse(node.getAttribute('data-ob-list-sources') || '{}');
    var body = new FormData(form), values = {};
    Object.keys(paths).sort().forEach(function (path) {
      var name = paths[path];
      var control = Array.from(form.elements).find(function (el) { return el.name === name && !el.disabled && !el.matches(':disabled'); });
      values[path] = control && control.type === 'checkbox' ? String(control.checked) : String(body.get(name) || '');
    });
    return values;
  }
  function state(node, status) {
    node.setAttribute('data-ob-list-state', status);
    node.querySelector('.ob-list-status').textContent = messages[status] || '';
    node.querySelector('tbody').replaceChildren();
    node.querySelector('thead tr').replaceChildren();
  }
  function current(node, revision, fingerprint) {
    return node.isConnected && form.isConnected && document.getElementById('main-form') === form && node._listRevision === revision && JSON.stringify(sources(node)) === fingerprint;
  }
  async function refresh(node) {
    var revision = (node._listRevision || 0) + 1; node._listRevision = revision;
    var values = sources(node), fingerprint = JSON.stringify(values);
    state(node, 'loading');
    if (concealed(node)) { state(node, 'denied'); return; }
    if (node._listAbort) node._listAbort.abort();
    var abort = typeof AbortController === 'function' ? new AbortController() : null; node._listAbort = abort;
    var body = new URLSearchParams();
    body.set('form', cfg.entityListForm); body.set('element', node.getAttribute('data-ob-list-id'));
    body.set('id', cfg.docId || ''); body.set('state', token); body.set('sources', fingerprint);
    try {
      var response = await fetch(String(cfg.url).replace(/form-event$/, 'form-list-refresh'), { method: 'POST', body: body, credentials: 'same-origin', headers: {'X-OneBase-Conceal-Context': form.getAttribute('data-ob-conceal-context') || ''}, signal: abort ? abort.signal : undefined });
      var data = await response.json();
      if (!current(node, revision, fingerprint)) return;
      if (!response.ok || !data.ok) { state(node, response.status === 403 ? 'denied' : 'error'); return; }
      paint(node, data.entityLists[node.getAttribute('data-ob-list-id')]);
    } catch (_) { if (current(node, revision, fingerprint)) state(node, 'error'); }
  }
  function paint(node, list) {
      if (!list || !Array.isArray(list.rows) || !Array.isArray(list.columns)) { state(node, 'error'); return; }
      if (concealed(node)) { state(node, 'denied'); return; }
      var deadlines = list.disclosureExpires || {};
      var expired = false;
      Object.keys(deadlines).forEach(function(name) {
        var key = name.toLowerCase(), until = deadlines[name];
        if (!Number.isFinite(until) || until <= Date.now()) expired = true;
        disclosureExpires[key] = until;
        setTimeout(function() {
          if (disclosureExpires[key] !== until) return;
          sourceControls(node).forEach(function(el) {
            if (String(el.name).toLowerCase() === key) { el.disabled = true; el.value = ''; }
          });
          concealChanged();
        }, Math.max(0, until - Date.now()));
      });
      if (expired) { sourceControls(node).forEach(function(el) { el.disabled = true; el.value = ''; }); concealChanged(); state(node, 'denied'); return; }
      state(node, list.status);
      var header = node.querySelector('thead tr'), rows = node.querySelector('tbody');
      list.columns.forEach(function (text) { var th = document.createElement('th'); th.textContent = text; header.appendChild(th); });
      list.rows.forEach(function (row) {
        var tr = document.createElement('tr'); if (row.marked) tr.style.textDecoration = 'line-through';
        row.cells.forEach(function (text) {
          var td = document.createElement('td');
          if (typeof row.url === 'string' && /^\/ui\/(catalog|document)\/[^/]+\/[0-9a-f-]{36}$/.test(row.url)) {
            var a = document.createElement('a'); a.href = row.url; a.target = '_blank'; a.rel = 'noopener noreferrer'; a.textContent = text; td.appendChild(a);
          } else td.textContent = text;
          tr.appendChild(td);
        }); rows.appendChild(tr);
      });
  }
  function invalidate(node) { node._listRevision = (node._listRevision || 0) + 1; if (node._listAbort) node._listAbort.abort(); state(node, 'loading'); }
  var timer;
  function refreshAll(data, revision) {
    if (data && data.entityListState) token = data.entityListState;
    if (data && data.savedId) cfg.docId = data.savedId;
    var canApply = data && data.entityLists && revision === generation && form.isConnected && document.getElementById('main-form') === form;
    generation++;
    if (canApply) {
      clearTimeout(timer);
      nodes.forEach(function(node) { if (!node.isConnected) return; invalidate(node); paint(node, data.entityLists[node.getAttribute('data-ob-list-id')]); });
      return;
    }
    nodes.forEach(invalidate); clearTimeout(timer); timer = setTimeout(function () { nodes.forEach(refresh); }, 0);
  }
  function changed(event) {
    generation++;
    nodes.forEach(function (node) {
      var paths = JSON.parse(node.getAttribute('data-ob-list-sources') || '{}');
      if (Object.keys(paths).some(function (path) { return paths[path] === event.target.name; })) {
        invalidate(node); clearTimeout(node._listTimer); node._listTimer = setTimeout(function () { refresh(node); }, 0);
      }
    });
  }
  form.addEventListener('input', changed); form.addEventListener('change', changed);
  window.obEntityLists = {
    refresh: refreshAll,
    concealChanged: concealChanged,
    invalidate: function () { nodes.forEach(invalidate); },
    snapshot: function (body) { body.set('_entity_list_state', token); return generation; }
  };
})();
