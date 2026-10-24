/* Managed reference inputs (plan 187/A). The select is a bounded no-JS
   fallback. Enhancement replaces it; exactly one hidden UUID is submitted. */
(function () {
  'use strict';
  var nextID = 0;
  function message(key) { return window.obUIMessage(key, key); }
  function init(sel) {
    // System accounts have a separate, existing selection contract and no
    // catalog search endpoint. Keep their native control.
    if (sel.getAttribute('data-ref-entity') === '_users') return;
    var value = document.createElement('input');
    Array.prototype.forEach.call(sel.attributes, function (a) { value.setAttribute(a.name, a.value); });
    value.type = 'hidden';
    value.value = sel.getAttribute('data-ob-ref-value') || sel.value;
    value.disabled = false;
    value.readOnly = sel.disabled;
    value.removeAttribute('required');
    value.removeAttribute('accesskey');
    var box = document.createElement('div');
    box.className = 'ob-ref-combobox managed-fill-control';
    box.style.cssText = 'position:relative;flex:1;min-width:0';
    var text = document.createElement('input');
    text.type = 'text';
    text.setAttribute('data-ob-ref-search', '1');
    text.id = 'ob-ref-search-' + (++nextID);
    text.autocomplete = 'off';
    text.maxLength = 256;
    text.style.cssText = 'width:100%';
    text.setAttribute('role', 'combobox');
    text.setAttribute('aria-autocomplete', 'list');
    text.setAttribute('aria-expanded', 'false');
    text.required = sel.hasAttribute('data-ob-ref-required');
    if (sel.hasAttribute('accesskey')) text.setAttribute('accesskey', sel.getAttribute('accesskey'));
    var list = document.createElement('div');
    list.id = text.id + '-list';
    list.setAttribute('role', 'listbox');
    list.hidden = true;
    list.style.cssText = 'position:absolute;top:100%;left:0;right:0;max-height:260px;overflow:auto;background:white;border:1px solid #cbd5e1;border-radius:6px;z-index:100';
    text.setAttribute('aria-controls', list.id);
    var status = document.createElement('div');
    status.id = text.id + '-status';
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    status.style.cssText = 'font-size:12px;color:#64748b';
    text.setAttribute('aria-describedby', status.id);
    var chosen = sel.options[sel.selectedIndex];
    text.value = value.value ? (chosen && chosen.value === value.value ? chosen.textContent : value.value) : '';
    var group = sel.closest('[data-ob-el]');
    var label = group && group.querySelector('label');
    if (label) label.setAttribute('for', text.id);
    sel.parentNode.replaceChild(box, sel);
    box.appendChild(value);
    box.appendChild(text);
    box.appendChild(list);
    box.appendChild(status);
    // Unlike a disabled select, the hidden UUID is always successful. Remove
    // its old readonly mirror so the name never submits two values.
    var mirror = document.getElementById('ro-mirror-' + value.name);
    if (mirror) mirror.remove();
    var seq = 0, controller = null, timer = null, rows = [], active = -1;
    var applied = fingerprint(), pending = null, pendingFingerprint = '', searchError = false;
    var selectedID = value.value;
    var collapsed = value.getAttribute('data-ref-choice-dropdown') === 'false';
    function fingerprint() {
      var choice = obRefChoiceSnapshot(value);
      if (value.hasAttribute('data-ref-choice-context') && !choice) return null;
      return JSON.stringify([value.getAttribute('data-ref-entity'), choice && choice.fingerprint, obRefFilterParam(value)]);
    }
    function live() {
      return value.isConnected && !text.readOnly && !value.disabled &&
        text.getClientRects().length > 0;
    }
    function validate() {
      text.setCustomValidity(text.readOnly ? '' : searchError ? message('refSearchError') :
        text.value && !value.value ? message('refChoose') : '');
    }
    function close() {
      list.hidden = true;
      text.setAttribute('aria-expanded', 'false');
      text.removeAttribute('aria-activedescendant');
      active = -1;
    }
    function cancel() {
      seq++;
      if (controller) controller.abort();
      controller = null;
      if (timer) clearTimeout(timer);
      timer = null;
      pending = null;
      pendingFingerprint = '';
      rows = [];
      text.removeAttribute('aria-busy');
      close();
    }
    function setValue(id, caption, change) {
      cancel();
      value.value = id;
      text.value = id ? String(caption || id) : '';
      selectedID = id;
      searchError = false;
      status.textContent = '';
      validate();
      if (change) obChoiceDispatchChange(value);
    }
    function updateState() {
      text.readOnly = value.readOnly || value.disabled || value.hasAttribute('readonly') || collapsed;
      if (text.readOnly || !text.getClientRects().length) cancel();
      validate();
    }
    function highlight(index) {
      var options = list.children;
      active = index;
      for (var i = 0; i < options.length; i++) {
        options[i].setAttribute('aria-selected', i === index ? 'true' : 'false');
        options[i].style.background = i === index ? '#dbeafe' : '';
      }
      if (options[index]) {
        text.setAttribute('aria-activedescendant', options[index].id);
        options[index].scrollIntoView({ block: 'nearest' });
      } else text.removeAttribute('aria-activedescendant');
    }
    function render(items) {
      rows = items;
      list.replaceChildren();
      rows.forEach(function (row, i) {
        var option = document.createElement('div');
        option.id = list.id + '-' + i;
        option.setAttribute('role', 'option');
        option.setAttribute('aria-selected', 'false');
        option.style.cssText = 'padding:8px;cursor:pointer';
        option.textContent = row._label == null ? row.id : String(row._label);
        option.addEventListener('mousedown', function (e) { e.preventDefault(); });
        option.addEventListener('click', function () { choose(i); });
        list.appendChild(option);
      });
      active = -1; // Enter requires an explicit highlighted choice, even for one row.
      list.hidden = rows.length === 0;
      text.setAttribute('aria-expanded', rows.length ? 'true' : 'false');
      status.textContent = rows.length ? String(rows.length) : message('refSearchEmpty');
    }
    function choose(index) {
      if (!live() || list.hidden || !rows[index]) return;
      setValue(String(rows[index].id), rows[index]._label, true);
      text.focus();
    }
    function request(search, refresh) {
      cancel();
      var token = seq, fp = fingerprint(), query = text.value, selected = value.value;
      if (fp === null) { fail(); return Promise.resolve(); }
      var choice = obRefChoiceQuery(value);
      var url = '/ui/_ref-options/' + encodeURIComponent(value.getAttribute('data-ref-entity')) +
        '?limit=50&q=' + encodeURIComponent(search) + (choice || obRefFilterParam(value)) +
        (selected ? '&check_selected_id=' + encodeURIComponent(selected) : '');
      controller = window.AbortController ? new window.AbortController() : null;
      var options = { credentials: 'same-origin', headers: { Accept: 'application/json' } };
      if (controller) options.signal = controller.signal;
      text.setAttribute('aria-busy', 'true');
      status.textContent = message('refSearchLoading');
      function current() { return token === seq && value.isConnected && fp === fingerprint() && query === text.value && selected === value.value; }
      function fail() {
        searchError = true;
        close();
        status.textContent = message('refSearchError');
        validate();
      }
      pendingFingerprint = fp;
      pending = window.fetch(url, options).then(function (r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      }).then(function (data) {
        if (!current()) return;
        if (!data || !Array.isArray(data.items) || data.items.length > 50 ||
            data.items.some(function (r) { return !r || typeof r.id !== 'string'; })) throw new Error('Invalid reference page');
        if (selected && typeof data.selected_allowed !== 'boolean') throw new Error('Missing selected_allowed');
        applied = fp;
        searchError = false;
        if (data.selected_allowed === false && selected) {
          setValue('', '', true);
          return;
        }
        if (refresh) { status.textContent = ''; validate(); return; }
        if (live() && document.activeElement === text && !collapsed) render(data.items);
        validate();
      }).catch(function (e) {
        if (!current() || (e && e.name === 'AbortError')) return;
        fail();
      }).finally(function () {
        if (token === seq) { text.removeAttribute('aria-busy'); pending = null; pendingFingerprint = ''; controller = null; }
      });
      return pending;
    }
    function refresh(force) {
      var fp = fingerprint();
      if (!force && fp === applied) return Promise.resolve();
      if (!force && fp === pendingFingerprint) return pending || Promise.resolve();
      return request('', true);
    }
    value._obRefCombo = { setValue: setValue, updateState: updateState, close: cancel, refresh: refresh, isEditing: function () { return !!text.value && !value.value; }, text: text };
    text.addEventListener('input', function () {
      if (text.readOnly) return;
      cancel();
      var hadValue = !!value.value;
      value.value = '';
      selectedID = '';
      searchError = false;
      status.textContent = '';
      validate();
      // The authoritative control alone owns ПриИзменении and dependent refresh.
      if (hadValue || !text.value) obChoiceDispatchChange(value);
      if (text.value.trim().length >= 2 && !collapsed) timer = setTimeout(function () { request(text.value.trim(), false); }, 200);
    });
    text.addEventListener('keydown', function (e) {
      if (text.readOnly) return;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        if (!rows.length || list.hidden) return;
        e.preventDefault(); e.stopPropagation();
        highlight(active < 0 ? (e.key === 'ArrowDown' ? 0 : rows.length - 1) :
          (active + (e.key === 'ArrowDown' ? 1 : -1) + rows.length) % rows.length);
      } else if (e.key === 'Enter') {
        e.preventDefault(); e.stopPropagation();
        if (active >= 0) choose(active);
      } else if (e.key === 'Escape') {
        e.preventDefault(); e.stopPropagation(); cancel();
      } else if (e.key === 'Tab') cancel();
    });
    text.addEventListener('blur', cancel);
    value.addEventListener('change', function () {
      if (value.value !== selectedID) setValue(value.value, value.value, false);
    });
    document.addEventListener('change', function () {
      // Check all declared sources, including owner-only filters. Equality of
      // the fingerprint avoids extra requests after unrelated form changes.
      if (fingerprint() !== applied) refresh(false);
    });
    window.addEventListener('pagehide', cancel);
    updateState();
  }
  obReady(function () {
    document.querySelectorAll('select[data-ob-ref-combobox]').forEach(init);
  });
})();
