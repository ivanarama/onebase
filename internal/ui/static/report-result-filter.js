(function () {
  'use strict';
  var bootstrap = document.getElementById('report-result-filter-fields');
  var rows = document.getElementById('rs-filter-rows');
  if (!bootstrap || !rows || rows.dataset.typedFilters) return;
  rows.dataset.typedFilters = '1';
  var fields;
  try { fields = JSON.parse(bootstrap.textContent) || []; } catch (_) { fields = []; }
  function descriptor(name) {
    return fields.find(function (f) { return f.name.toLowerCase() === String(name).toLowerCase(); });
  }
  function localDate(raw) {
    if (!raw) return '';
    if (/^\d{2}\.\d{2}\.\d{4}$/.test(raw)) return raw.slice(6) + '-' + raw.slice(3, 5) + '-' + raw.slice(0, 2) + 'T00:00:00';
    if (/^\d{4}-\d{2}-\d{2}$/.test(raw)) return raw + 'T00:00:00';
    if (!/(Z|[+-]\d\d:\d\d)$/.test(raw)) return raw;
    var d = new Date(raw);
    if (!isFinite(d.getTime())) return raw;
    var pad = function (v) { return String(v).padStart(2, '0'); };
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  }
  function sync(row) {
    var field = row.querySelector('.rs-f-field'), op = row.querySelector('.rs-f-op'), old = row.querySelector('.rs-f-value');
    if (!field || !op || !old) return;
    var spec = descriptor(field.value);
    Array.prototype.forEach.call(op.options, function (option) {
      option.disabled = !spec || spec.operators.indexOf(option.value) < 0;
      option.hidden = option.disabled;
    });
    if (!op.selectedOptions.length || op.selectedOptions[0].disabled) op.value = spec ? spec.operators[0] : '';
    var raw = old.value, control;
    if (spec && (spec.type === 'bool' || spec.enum_name)) {
      control = document.createElement('select');
      var values = spec.enum_name ? (spec.values || []) : ['true', 'false'];
      values.forEach(function (value) { var option = document.createElement('option'); option.value = value; option.textContent = value; control.appendChild(option); });
    } else {
      control = document.createElement('input'); control.type = 'text'; control.maxLength = 4096;
      if (spec && spec.type === 'number') control.inputMode = 'decimal';
      if (spec && (spec.type === 'date' || spec.type === 'datetime')) { control.type = 'datetime-local'; control.step = '1'; raw = localDate(raw); }
      if (spec && spec.ref_entity) { control.placeholder = 'UUID'; control.pattern = '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}'; }
    }
    control.className = 'rs-f-value'; control.value = raw;
    if (control.tagName === 'SELECT' && !control.value && control.options.length) control.selectedIndex = 0;
    var noValue = op.value === 'is_null' || op.value === 'not_null';
    control.disabled = !spec || noValue; control.required = !!spec && !noValue && spec.type !== 'string';
    if (noValue) control.value = '';
    old.replaceWith(control);
  }
  Array.prototype.forEach.call(rows.querySelectorAll('.rs-filter-row'), sync);
  rows.addEventListener('change', function (event) {
    if (event.target.matches('.rs-f-field, .rs-f-op')) sync(event.target.closest('.rs-filter-row'));
  });
  new MutationObserver(function (records) {
    records.forEach(function (record) { record.addedNodes.forEach(function (node) { if (node.nodeType === 1 && node.matches('.rs-filter-row')) sync(node); }); });
  }).observe(rows, { childList: true });
})();
