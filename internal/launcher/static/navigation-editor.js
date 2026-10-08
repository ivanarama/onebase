(function () {
  'use strict';
  function start() {
    var data = window.OB_MENU_EDITOR;
    if (!data || !document.getElementById('menu-editor')) return;
    var labels = data.labels, menu = data.menu, selected = '', dirty = false, revision = 0, saving = false;
    var endpoint = '/bases/' + encodeURIComponent(data.base_id) + '/configurator/navigation';
    var palette = data.palette || [], language = data.lang;
    function el(id) { return document.getElementById('menu-' + id); }
    function make(tag, text) {
      var node = document.createElement(tag);
      if (text !== undefined) node.textContent = text;
      return node;
    }
    function decorate(node, name) {
      if (!name) return;
      var key = name.trim().toLowerCase().replace(/[\s_]+/g, '-').replace(/-+/g, '-');
      key = (data.icon_aliases || {})[key] || key;
      if (!(data.icon_names || []).includes(key)) key = 'square';
      var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      svg.setAttribute('width', '16'); svg.setAttribute('height', '16'); svg.setAttribute('aria-hidden', 'true');
      svg.setAttribute('viewBox', '0 0 24 24'); svg.setAttribute('fill', 'none'); svg.setAttribute('stroke', 'currentColor');
      svg.setAttribute('stroke-width', '2'); svg.setAttribute('stroke-linecap', 'round'); svg.setAttribute('stroke-linejoin', 'round');
      svg.setAttribute('style', 'vertical-align:middle;margin-right:5px');
      var use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
      use.setAttribute('href', data.icon_sprite + '#' + key); svg.appendChild(use); node.appendChild(svg);
    }
    function clear(node) { node.replaceChildren(); }
    function status(text, error) {
      el('status').textContent = text || '';
      el('status').className = 'notice ' + (error ? 'error' : 'ok');
      el('live').textContent = text || '';
    }
    function entries() {
      var result = [];
      (menu && menu.sections || []).forEach(function (section) {
        result.push({node: section, kind: 'section', list: menu.sections, parent: null});
        (section.items || []).forEach(function (item) { result.push({node: item, kind: 'item', list: section.items, parent: section}); });
        (section.groups || []).forEach(function (group) {
          result.push({node: group, kind: 'group', list: section.groups, parent: section});
          (group.items || []).forEach(function (item) { result.push({node: item, kind: 'item', list: group.items, parent: group}); });
        });
      });
      return result;
    }
    function find(id) { return entries().find(function (entry) { return entry.node.id === id; }); }
    function title(node) {
      var object = palette.find(function (item) { return item.target === node.target; });
      return node.title || (object && object.label) || node.target || node.id;
    }
    function id(prefix) { return prefix + '-' + window.crypto.randomUUID(); }
    function button(text, action) {
      var node = make('button', text); node.type = 'button'; node.addEventListener('click', action); return node;
    }
    function focus(id, action) {
      var row = Array.from(el('tree').children).find(function (node) { return node.dataset.id === id; });
      if (row) {
        var control = row.querySelector('[data-action="' + (action || 'select') + '"]');
        if (!control || control.disabled) control = row.querySelector('button');
        control.focus();
      }
    }
    function changed(action) {
      dirty = true; revision++; status(labels.changed); render(); focus(selected, action); preview();
    }
    function move(id, action) {
      var entry = find(id); if (!entry) return;
      var at = entry.list.indexOf(entry.node), to = action === 'up' ? at - 1 : at + 1;
      if (action === 'up' || action === 'down') {
        if (to < 0 || to >= entry.list.length) return;
        entry.list.splice(at, 1); entry.list.splice(to, 0, entry.node);
      } else if (entry.kind === 'item' && action === 'in') {
        var section = find(entry.parent.id);
        if (!section || section.kind !== 'section' || !(section.node.groups || []).length) return;
        entry.list.splice(at, 1);
        var group = section.node.groups[0]; (group.items || (group.items = [])).push(entry.node);
      } else if (entry.kind === 'item' && action === 'out') {
        var parent = find(entry.parent.id);
        if (!parent || parent.kind !== 'group') return;
        entry.list.splice(at, 1); (parent.parent.items || (parent.parent.items = [])).push(entry.node);
      } else return;
      selected = id; changed(action);
    }
    // Dropping on a container appends; dropping on a sibling inserts before it.
    function relocate(sourceID, targetID) {
      var source = find(sourceID), target = find(targetID);
      if (!source || !target || sourceID === targetID) return false;
      var destination;
      if (source.kind === target.kind) destination = target.list;
      else if (source.kind === 'item' && (target.kind === 'section' || target.kind === 'group')) destination = target.node.items || (target.node.items = []);
      else if (source.kind === 'group' && target.kind === 'section') destination = target.node.groups || (target.node.groups = []);
      else return false;
      source.list.splice(source.list.indexOf(source.node), 1);
      var index = source.kind === target.kind ? destination.indexOf(target.node) : destination.length;
      destination.splice(index, 0, source.node); selected = sourceID; changed(); return true;
    }
    function properties() {
      var root = el('properties'); clear(root); var entry = find(selected); if (!entry) return;
      function input(label, value, update) {
        var field = make('label', label), control = make('input'); control.value = value || '';
        control.addEventListener('change', function () { update(control.value); changed(); });
        field.appendChild(control); root.appendChild(field); return control;
      }
      input(labels.primaryTitle, entry.node.title, function (value) { if (value) entry.node.title = value; else delete entry.node.title; });
      input(labels.icon, entry.node.icon, function (value) { if (value) entry.node.icon = value; else delete entry.node.icon; });
      ['ru', 'en'].forEach(function (lang) {
        input(labels.translations + ' (' + lang + ')', entry.node.titles && entry.node.titles[lang], function (value) {
          if (value) (entry.node.titles || (entry.node.titles = {}))[lang] = value;
          else if (entry.node.titles) delete entry.node.titles[lang];
        });
      });
      root.appendChild(make('p', labels.translationHint));
      if (entry.kind !== 'section') {
        var field = make('label', labels.parent), select = make('select');
        entries().filter(function (candidate) { return candidate.kind === 'section' || (entry.kind === 'item' && candidate.kind === 'group'); }).forEach(function (candidate) {
          var option = make('option', title(candidate.node)); option.value = candidate.node.id; option.selected = entry.parent === candidate.node; select.appendChild(option);
        });
        select.value = entry.parent.id;
        select.addEventListener('change', function () { relocate(entry.node.id, select.value); });
        field.appendChild(select); root.appendChild(field);
      }
    }
    function render() {
      var tree = el('tree'); clear(tree);
      if (!menu) tree.appendChild(make('p', labels.empty));
      entries().forEach(function (entry) {
        var row = make('div'); row.className = 'menu-row' + (entry.node.id === selected ? ' selected' : '');
        row.dataset.id = entry.node.id; row.dataset.kind = entry.kind; row.draggable = true;
        var select = button(title(entry.node), function () { selected = entry.node.id; render(); focus(selected); });
        select.className = 'menu-name'; select.dataset.action = 'select'; select.setAttribute('aria-pressed', String(selected === entry.node.id)); row.appendChild(select);
        ['up', 'down', 'in', 'out'].forEach(function (action) {
          if ((action === 'in' || action === 'out') && entry.kind !== 'item') return;
          var control = button(labels[action], function () { move(entry.node.id, action); }); control.dataset.action = action;
          var index = entry.list.indexOf(entry.node);
          control.disabled = (action === 'up' && index === 0) || (action === 'down' && index === entry.list.length - 1) ||
            (action === 'in' && (!entry.parent || !(entry.parent.groups || []).length)) ||
            (action === 'out' && (!entry.parent || find(entry.parent.id).kind !== 'group'));
          row.appendChild(control);
        });
        row.appendChild(button(labels.remove, function () {
          if (!window.confirm(labels.removeConfirm)) return;
          entry.list.splice(entry.list.indexOf(entry.node), 1); selected = entry.parent ? entry.parent.id : ''; changed();
        }));
        row.addEventListener('keydown', function (event) {
          var actions = {ArrowUp: 'up', ArrowDown: 'down', ArrowRight: 'in', ArrowLeft: 'out'};
          if (event.altKey && actions[event.key]) { event.preventDefault(); move(entry.node.id, actions[event.key]); }
        });
        row.addEventListener('dragstart', function (event) { event.dataTransfer.setData('text/plain', entry.node.id); event.dataTransfer.effectAllowed = 'move'; });
        row.addEventListener('dragover', function (event) { event.preventDefault(); row.classList.add('drop-target'); });
        row.addEventListener('dragleave', function () { row.classList.remove('drop-target'); });
        row.addEventListener('drop', function (event) { event.preventDefault(); row.classList.remove('drop-target'); relocate(event.dataTransfer.getData('text/plain'), entry.node.id); });
        tree.appendChild(row);
      });
      el('save').disabled = !menu || saving; el('add-group').disabled = !selectedContainer(); properties(); renderPalette();
    }
    function selectedContainer() {
      var entry = find(selected);
      if (entry && entry.kind === 'item') entry = find(entry.parent.id);
      return entry;
    }
    function renderPalette() {
      var root = el('palette'); clear(root); var filter = el('filter').value.toLocaleLowerCase();
      palette.filter(function (item) { return (item.label + ' ' + item.target).toLocaleLowerCase().includes(filter); }).forEach(function (item) {
        var row = make('div'); row.className = 'palette-item'; row.appendChild(make('span', item.label));
        var control = button(labels.add, function () {
          var container = selectedContainer(); if (!container) return;
          var node = {id: id('i'), target: item.target}; (container.node.items || (container.node.items = [])).push(node); selected = node.id; changed();
        });
        control.disabled = !selectedContainer(); control.dataset.target = item.target; row.appendChild(control); root.appendChild(row);
      });
    }
    function renderPreview(sections) {
      var root = el('preview'); clear(root);
      function items(parent, values) {
        if (!(values || []).length) return;
        var list = make('ul'); values.forEach(function (item) { var li = make('li', item.label); decorate(li, item.icon); li.dataset.id = item.id; list.appendChild(li); }); parent.appendChild(list);
      }
      (sections || []).forEach(function (section) {
        var block = make('div'); block.dataset.id = section.id; var heading = make('h3', section.title); decorate(heading, section.icon); block.appendChild(heading); items(block, section.items);
        (section.groups || []).forEach(function (group) { var detail = make('details'); detail.open = true; var summary = make('summary', group.title); decorate(summary, group.icon); detail.appendChild(summary); items(detail, group.items); block.appendChild(detail); }); root.appendChild(block);
      });
    }
    async function request(url, options) {
      var response = await window.fetch(url, options);
      if (!(response.headers.get('Content-Type') || '').includes('application/json')) throw new Error(labels.unexpected);
      var result = await response.json(); if (!response.ok || result.error) throw new Error(result.error || labels.error); return result;
    }
    function payload() { return JSON.stringify({subsystem: data.subsystem, menu: menu, lang: language}); }
    function options(body) { return {method: 'POST', headers: {'Content-Type': 'application/json', Accept: 'application/json'}, body: body}; }
    async function preview() {
      var current = revision;
      try {
        var result = await request(endpoint + '/preview', options(payload()));
        if (current !== revision) return;
        palette = result.palette || palette; renderPalette(); renderPreview(result.preview);
        if ((result.warnings || []).length) status(result.warnings.join('\n'));
      } catch (error) { if (current === revision) status(labels.error + ': ' + error.message, true); }
    }
    el('add-section').addEventListener('click', function () {
      if (!menu) menu = {};
      if (!Array.isArray(menu.sections)) menu.sections = [];
      var node = {id: id('s'), title: labels.newSection}; menu.sections.push(node); selected = node.id; changed();
    });
    el('add-group').addEventListener('click', function () {
      var container = selectedContainer(); if (!container) return;
      if (container.kind === 'group') container = find(container.parent.id);
      var node = {id: id('g'), title: labels.newGroup}; (container.node.groups || (container.node.groups = [])).push(node); selected = node.id; changed();
    });
    async function importMenu(kind) {
      if ((menu || dirty) && !window.confirm(labels.replace)) return;
      var current = ++revision;
      try {
        var query = new URLSearchParams({subsystem: data.subsystem, import: kind});
        var result = await request(endpoint + '?' + query, {headers: {Accept: 'application/json', 'Accept-Language': language}});
        if (current !== revision) return;
        menu = result.menu; palette = result.palette; selected = ''; changed();
      } catch (error) { if (current === revision) status(labels.error + ': ' + error.message, true); }
    }
    el('import-legacy').addEventListener('click', function () { importMenu('legacy'); });
    el('import-tree').addEventListener('click', function () { importMenu('tree-order'); });
    el('save').addEventListener('click', async function () {
      if (!menu || saving) return; var current = revision; saving = true; el('save').disabled = true;
      try {
        var result = await request(endpoint + '/save', options(payload()));
        if (current !== revision) return;
        dirty = false; renderPreview(result.preview); status(labels.saved);
      } catch (error) { status(labels.error + ': ' + error.message, true); }
      finally { saving = false; el('save').disabled = !menu; }
    });
    el('filter').addEventListener('input', renderPalette);
    el('lang').value = language;
    el('lang').addEventListener('change', function () { language = el('lang').value; revision++; preview(); });
    window.addEventListener('beforeunload', function (event) { if (dirty) { event.preventDefault(); event.returnValue = labels.unsaved; } });
    render(); renderPreview(data.preview); if (data.error) status(data.error, true);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start); else start();
})();
