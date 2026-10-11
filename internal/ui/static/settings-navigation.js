(function () {
  'use strict';
  function start() {
    var bootstrap = document.getElementById('navigation-editor-data');
    if (!bootstrap || bootstrap.dataset.initialized) return;
    bootstrap.dataset.initialized = '1';
    var data = JSON.parse(bootstrap.textContent), labels = data.labels;
    var desired = clone(data.desired), selected = '', counter = 0, version = 0;
    var path = data.path || '/ui/admin/navigation', ownedPrefix = data.ownedPrefix || 'adm:';
    var renamed = new Set(data.renamed || []);
    var dirty = false, submitting = false, timer, dragged = '';
    var tree = document.getElementById('navigation-tree');
    var properties = document.getElementById('navigation-properties');
    var palette = document.getElementById('navigation-palette');
    var preview = document.getElementById('navigation-preview');
    var status = document.getElementById('navigation-status');
    var live = document.getElementById('navigation-live');
    var hidden = document.getElementById('navigation-desired');
    var save = document.getElementById('navigation-save');
    function clone(value) { return JSON.parse(JSON.stringify(value)); }
    function element(tag, text) {
      var el = document.createElement(tag);
      if (text !== undefined) el.textContent = text;
      return el;
    }
    function walk(value, fn) {
      (value.sections || []).forEach(function (section, index) {
        fn({node:section, list:value.sections, index:index, parent:null, section:section, kind:'section'});
        (section.items || []).forEach(function (item, i) { fn({node:item, list:section.items, index:i, parent:section, section:section, kind:'item'}); });
        (section.groups || []).forEach(function (group, i) {
          fn({node:group, list:section.groups, index:i, parent:section, section:section, kind:'group'});
          (group.items || []).forEach(function (item, j) { fn({node:item, list:group.items, index:j, parent:group, section:section, kind:'item'}); });
        });
      });
    }
    function find(id, value) {
      var result;
      walk(value || desired, function (entry) { if (entry.node.id === id) result = entry; });
      return result;
    }
    function label(node) {
      if (node.title) return node.title;
      return node.object && node.object.title || node.target || node.id;
    }
    function button(text, action) {
      var el = element('button', text); el.type = 'button';
      el.addEventListener('click', action); return el;
    }
    function synchronize() {
      hidden.value = JSON.stringify(desired);
      if (data.personal) document.getElementById('navigation-renamed').value = JSON.stringify(renameIntent());
    }
    function renameIntent() {
      return Array.from(renamed).filter(function(id) { return find(id) && inherited(id); }).sort();
    }
    function inherited(id) {
      return id.indexOf('cfg:') === 0 || id.indexOf('adm:') === 0;
    }
    function requestBody() {
      var body = new URLSearchParams({subsystem:data.subsystem, revision:data.revision, desired:JSON.stringify(desired)});
      if (data.personal) { body.set('base_revision', data.baseRevision); body.set('renamed', JSON.stringify(renameIntent())); }
      return body;
    }
    function changed(id, preserveInput) {
      if (submitting) return;
      selected = id || selected; dirty = true; version++;
      synchronize(); renderTree(!preserveInput); renderPalette();
      live.textContent = labels.changed; status.textContent = '';
      clearTimeout(timer); timer = setTimeout(requestPreview, 200);
      if (!preserveInput) {
        var focus = tree.querySelector('[data-node-id="' + selected + '"]');
        if (focus) focus.focus();
      }
    }
    function move(id, direction) {
      if (submitting) return;
      var entry = find(id); if (!entry) return;
      if (direction === 'up' || direction === 'down') {
        var index = entry.index + (direction === 'up' ? -1 : 1);
        if (index < 0 || index >= entry.list.length) return;
        entry.list.splice(entry.index, 1); entry.list.splice(index, 0, entry.node);
      } else if (direction === 'in') {
        if (entry.kind !== 'item' || entry.parent !== entry.section || !(entry.section.groups || []).length) return;
        entry.list.splice(entry.index, 1);
        var group = entry.section.groups[0]; group.items = group.items || []; group.items.push(entry.node);
      } else if (direction === 'out') {
        if (entry.kind !== 'item' || entry.parent === entry.section) return;
        entry.list.splice(entry.index, 1); entry.section.items = entry.section.items || []; entry.section.items.push(entry.node);
      }
      changed(id);
    }
    function relocate(id, parentID) {
      var entry = find(id), parent = find(parentID);
      if (submitting || !entry || !parent || entry.kind === 'section' || parent.kind === 'item' || entry.kind === 'group' && parent.kind !== 'section') return;
      entry.list.splice(entry.index, 1);
      var field = entry.kind === 'group' ? 'groups' : 'items';
      parent.node[field] = parent.node[field] || []; parent.node[field].push(entry.node);
      changed(id);
    }
    function drop(id, targetID) {
      if (submitting || id === targetID) return;
      var entry = find(id), target = find(targetID); if (!entry || !target) return;
      if (entry.kind === target.kind) {
        entry.list.splice(entry.index, 1); target = find(targetID);
        target.list.splice(target.index, 0, entry.node); changed(id);
      } else if (entry.kind === 'item' && target.kind !== 'item' || entry.kind === 'group' && target.kind === 'section') {
        relocate(id, targetID);
      }
    }
    function renderTree(showProperties) {
      tree.replaceChildren();
      walk(desired, function (entry) {
        var row = element('div'); row.className = 'navigation-row' + (entry.node.id === selected ? ' selected' : '');
        row.dataset.kind = entry.kind; row.draggable = true;
        var choose = button(label(entry.node), function () { selected = entry.node.id; renderTree(true); tree.querySelector('[data-node-id="'+selected+'"]').focus(); });
        choose.dataset.nodeId = entry.node.id;
        row.appendChild(choose);
        if (data.personal) row.appendChild(element('small', labels[(data.origins || {})[entry.node.id] || 'personal']));
        ['up','down','in','out'].forEach(function (direction) {
          var control = button(labels[direction], function () { move(entry.node.id, direction); });
          control.dataset.action = direction;
          control.disabled = submitting || (direction === 'up' && entry.index === 0) || (direction === 'down' && entry.index === entry.list.length-1) ||
            (direction === 'in' && (entry.kind !== 'item' || entry.parent !== entry.section || !(entry.section.groups || []).length)) ||
            (direction === 'out' && (entry.kind !== 'item' || entry.parent === entry.section));
          row.appendChild(control);
        });
        row.appendChild(button(entry.node.id.indexOf(ownedPrefix) === 0 || entry.node.id.indexOf('new:') === 0 ? labels.remove : labels.hide, function () {
          if (submitting) return;
          entry.list.splice(entry.index, 1);
          selected = entry.parent ? entry.parent.id : (desired.sections[0] || {}).id || '';
          changed(selected);
        }));
        row.addEventListener('keydown', function (event) {
          var directions = {ArrowUp:'up', ArrowDown:'down', ArrowRight:'in', ArrowLeft:'out'};
          if (!event.altKey || !directions[event.key]) return;
          event.preventDefault(); move(entry.node.id, directions[event.key]);
        });
        row.addEventListener('dragstart', function (event) { dragged = entry.node.id; if(event.dataTransfer)event.dataTransfer.setData('text/plain', dragged); });
        row.addEventListener('dragover', function (event) { event.preventDefault(); });
        row.addEventListener('drop', function (event) { event.preventDefault(); drop(dragged, entry.node.id); dragged = ''; });
        row.addEventListener('dragend', function () { dragged = ''; });
        tree.appendChild(row);
      });
      if (!(desired.sections || []).length) tree.appendChild(element('p', labels.empty));
      if (showProperties) renderProperties();
    }
    function renderProperties() {
      properties.replaceChildren();
      var entry = find(selected); if (!entry) return;
      function field(name, input) { var container = element('label', name); container.appendChild(input); properties.appendChild(container); }
      var title = element('input'); title.type = 'text'; title.value = entry.node.title || ''; title.placeholder = label(entry.node);
      title.addEventListener('input', function () { if(submitting)return; entry.node.title = title.value; delete entry.node.titles; if (entry.kind === 'section') entry.node.title_explicit = true; if (data.personal && inherited(entry.node.id)) renamed.add(entry.node.id); changed(entry.node.id, true); });
      field(labels.title, title);
      var icon = element('select');
      [''].concat(data.icons || []).forEach(function (name) { var option = element('option', name || '—'); option.value = name; icon.appendChild(option); });
      icon.value = entry.node.icon || '';
      icon.addEventListener('change', function () { if(submitting)return; entry.node.icon = icon.value; changed(entry.node.id, true); });
      field(labels.icon, icon);
      if (entry.kind !== 'section') {
        var parent = element('select');
        walk(desired, function (destination) {
          if (destination.kind === 'section' || entry.kind === 'item' && destination.kind === 'group') {
            var option = element('option', label(destination.node)); option.value = destination.node.id; parent.appendChild(option);
          }
        });
        parent.value = entry.parent.id;
        parent.addEventListener('change', function () { relocate(entry.node.id, parent.value); });
        field(labels.parent, parent);
      }
    }
    function restore(id) {
      if (submitting || find(id)) return;
      var original = find(id, data.base); if (!original) return;
      function ensureParent(parentID) {
        if (find(parentID)) return;
        var ancestor = find(parentID, data.base);
        if (ancestor.parent) ensureParent(ancestor.parent.id);
        var container = clone(ancestor.node); container.items = [];
        if (ancestor.kind === 'section') {container.groups = []; desired.sections.push(container);}
        else {var section = find(ancestor.parent.id).node; section.groups = section.groups || []; section.groups.push(container);}
      }
      if (original.parent) ensureParent(original.parent.id);
      var node = clone(original.node);
      function remaining(items) { return (items || []).filter(function(item){return !find(item.id);}); }
      if (original.kind !== 'item') node.items = remaining(node.items);
      if (original.kind === 'section') {
        node.groups = (node.groups || []).filter(function(group){return !find(group.id);}).map(function(group){group.items = remaining(group.items);return group;});
        desired.sections.push(node);
      } else {
        var parent = find(original.parent.id).node, field = original.kind === 'group' ? 'groups' : 'items';
        parent[field] = parent[field] || []; parent[field].push(node);
      }
      changed(id);
    }
    function renderPalette() {
      palette.replaceChildren();
      var filter = document.getElementById('navigation-filter').value.toLowerCase();
      walk(data.base, function (entry) {
        if (find(entry.node.id) || filter && label(entry.node).toLowerCase().indexOf(filter) < 0) return;
        var row = element('div'); row.appendChild(element('span', label(entry.node)));
        var control = button(labels.restore, function () { restore(entry.node.id); }); control.dataset.restoreId = entry.node.id; row.appendChild(control); palette.appendChild(row);
      });
    }
    function renderPreview(sections) {
      preview.replaceChildren();
      function decorated(tag, text, name) {
        var node = element(tag, text);
        // Match NormalizeIconName and LucideIcon; only server-listed symbols
        // may become fragments of the versioned, same-origin sprite URL.
        var key = (name || '').trim().toLowerCase().replace(/[ _-]+/g, '-').replace(/^-|-$/g, '');
        if (!key) return node;
        if (Object.prototype.hasOwnProperty.call(data.iconAliases || {}, key)) key = data.iconAliases[key];
        if (!(data.icons || []).includes(key)) key = data.iconFallback;
        var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        svg.setAttribute('class', 'lucide ob-icon');
        svg.setAttribute('style', 'vertical-align:middle;margin-right:5px');
        svg.setAttribute('width', '1em'); svg.setAttribute('height', '1em');
        svg.setAttribute('viewBox', '0 0 24 24'); svg.setAttribute('fill', 'none'); svg.setAttribute('stroke', 'currentColor');
        svg.setAttribute('stroke-width', '2'); svg.setAttribute('stroke-linecap', 'round'); svg.setAttribute('stroke-linejoin', 'round');
        svg.setAttribute('aria-hidden', 'true'); svg.setAttribute('focusable', 'false');
        var use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
        use.setAttribute('href', data.iconSprite + '#' + key); svg.appendChild(use); node.prepend(svg);
        return node;
      }
      (sections || []).forEach(function (section) {
        preview.appendChild(decorated('h3', section.title, section.icon));
        function items(value, container) {
          var list = element('ul');
          (value || []).forEach(function(item){var li = element('li'), link = decorated('a', item.label, item.icon); link.href = item.url; li.appendChild(link); list.appendChild(li);});
          container.appendChild(list);
        }
        items(section.items, preview);
        (section.groups || []).forEach(function(group){var folder = element('details'); folder.open = true; folder.appendChild(decorated('summary', group.title, group.icon)); items(group.items, folder); preview.appendChild(folder);});
      });
    }
    function requestPreview() {
      var requested = version;
      var body = requestBody();
      return fetch(path+'/preview', {method:'POST', credentials:'same-origin', headers:{Accept:'application/json'}, body:body})
        .then(function(response){if(!response.ok)throw new Error(labels.error);return response.json();})
        .then(function(result){if(requested === version && !submitting)renderPreview(result.preview);})
        .catch(function(){if(requested === version && !submitting)status.textContent = labels.error;});
    }
    document.getElementById('navigation-add-section').addEventListener('click', function () {
      if (submitting) return;
      var node = {id:'new:'+ (++counter), title:labels.newSection, items:[], groups:[]};
      desired.sections = desired.sections || []; desired.sections.push(node); changed(node.id);
    });
    document.getElementById('navigation-add-group').addEventListener('click', function () {
      if (submitting) return;
      var entry = find(selected), section = entry ? entry.section : (desired.sections || [])[0]; if (!section) return;
      var node = {id:'new:'+ (++counter), title:labels.newGroup, items:[]};
      section.groups = section.groups || []; section.groups.push(node); changed(node.id);
    });
    document.getElementById('navigation-filter').addEventListener('input', renderPalette);
    save.addEventListener('submit', function (event) {
      event.preventDefault();
      if (submitting) return;
      synchronize(); submitting = true; version++; clearTimeout(timer);
      var body = requestBody();
      fetch(path+'/save', {method:'POST', credentials:'same-origin', headers:{Accept:'application/json'}, body:body})
        .then(function(response) {
          if (response.status === 409) {
            return response.json().then(function(result) {
              submitting = false; version++;
              status.textContent = labels.conflict;
              status.appendChild(button(labels.reload, function () {
                if (!window.confirm(labels.reloadConfirm)) return;
                submitting = true;
                window.location.assign(path+'?subsystem='+encodeURIComponent(data.subsystem));
              }));
              // Show the winner's current preview while keeping this tab's draft
              // and old revision. Reload is an explicit choice, never overwrite.
              renderPreview(result.preview);
            });
          }
          if (!response.ok) throw new Error(labels.saveError);
          window.location.assign(response.url); // follows the server's 303 to GET
        })
        .catch(function() {submitting = false; status.textContent = labels.saveError;});
    });
    document.getElementById('navigation-reset').addEventListener('submit', function (event) {
      // The shared UI confirmation runs in the document's capture phase.
      if (!event.defaultPrevented) {submitting = true; clearTimeout(timer);}
    });
    window.addEventListener('beforeunload', function(event){if(dirty && !submitting){event.preventDefault();event.returnValue = labels.unsaved;}});
    synchronize(); renderTree(true); renderPalette(); renderPreview(data.preview);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
