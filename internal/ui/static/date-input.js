/* Optional short calendar-date entry; no layout replacement or network work. */
(function () {
  'use strict';
  var editing = new WeakMap();
  var labels = {};
  try { labels = JSON.parse(document.getElementById('ob-date-input-i18n').textContent); } catch (_) {}
  function dateInput(el) {
    return el && el.tagName === 'INPUT' && !el.disabled && !el.readOnly &&
      (editing.has(el) || el.type === 'date' || el.type === 'datetime-local');
  }
  function begin(el, deleting) {
    if (editing.has(el)) return;
    editing.set(el, {type: el.type, value: el.value});
    // Let the browser perform the following key/paste edit on this same input:
    // it generates a real input event, including the form's native dirty state.
    el.type = 'text';
    el.value = deleting ? editing.get(el).value : '';
    if (deleting && el.select) el.select();
  }
  function pad(value, n) { return String(value).padStart(n || 2, '0'); }
  function normalized(text, state) {
    var now = new Date(), raw = String(text || '').trim();
    if (!raw) return '';
    var suffix = raw.match(/^(.*?)[ T](\d{1,2}):(\d{2})(?::(\d{2}))?$/);
    var clock = '';
    if (suffix) {
      if (state.type !== 'datetime-local' || +suffix[2] > 23 || +suffix[3] > 59 || +(suffix[4] || 0) > 59) return null;
      raw = suffix[1]; clock = pad(+suffix[2]) + ':' + suffix[3] + (suffix[4] ? ':' + suffix[4] : '');
    }
    var y = now.getFullYear(), m = now.getMonth() + 1, d;
    var match = raw.match(/^(\d{4})-(\d{2})-(\d{2})$/);
    if (match) { y = +match[1]; m = +match[2]; d = +match[3]; }
    else if ((match = raw.match(/^(\d{1,2})[./-](\d{1,2})(?:[./-](\d{4}))?$/))) {
      d = +match[1]; m = +match[2]; if (match[3]) y = +match[3];
    } else if (/^\d{1,2}$/.test(raw)) d = +raw;
    else if (/^\d{4}$/.test(raw)) { d = +raw.slice(0,2); m = +raw.slice(2); }
    else if (/^\d{8}$/.test(raw)) { d = +raw.slice(0,2); m = +raw.slice(2,4); y = +raw.slice(4); }
    else return null;
    if (y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 || d > 31) return null;
    var check = new Date(0); check.setHours(12,0,0,0); check.setFullYear(y,m-1,d);
    if (check.getFullYear() !== y || check.getMonth() !== m-1 || check.getDate() !== d) return null;
    var value = pad(y,4) + '-' + pad(m) + '-' + pad(d);
    if (state.type === 'datetime-local') value += 'T' + (clock || (state.value.split('T')[1] || '00:00'));
    return value;
  }
  function commit(el) {
    var state = editing.get(el);
    if (!state) return true;
    var value = normalized(el.value, state);
    if (value === null) {
      el.setCustomValidity(labels.invalid || 'Invalid date');
      return false;
    }
    el.setCustomValidity('');
    el.type = state.type; el.value = value; editing.delete(el);
    if (value !== state.value) {
      el.dispatchEvent(new Event('input', {bubbles:true}));
      el.dispatchEvent(new Event('change', {bubbles:true}));
    }
    return true;
  }
  document.addEventListener('keydown', function (event) {
    var el = event.target;
    if (!dateInput(el) || event.isComposing || event.ctrlKey || event.altKey || event.metaKey) return;
    if (event.key === 'Escape' && editing.has(el)) {
      var state = editing.get(el); el.setCustomValidity(''); el.type = state.type; el.value = state.value; editing.delete(el);
      event.preventDefault(); event.stopPropagation(); return;
    }
    if (event.key === 'Enter') {
      event.preventDefault(); event.stopPropagation();
      if (!commit(el)) { if (el.reportValidity) el.reportValidity(); return; }
      if (!(typeof window.obEnterNavigateFrom === 'function' && window.obEnterNavigateFrom(el))) el.blur();
      return;
    }
    if (!editing.has(el) && (/^\d$/.test(event.key) || event.key === 'Backspace' || event.key === 'Delete')) begin(el, event.key === 'Backspace' || event.key === 'Delete');
  }, true);
  document.addEventListener('paste', function (event) {
    if (dateInput(event.target) && event.clipboardData && event.clipboardData.getData('text').trim()) begin(event.target);
  }, true);
  document.addEventListener('input', function (event) {
    if (editing.has(event.target)) event.target.setCustomValidity('');
  }, true);
  document.addEventListener('blur', function (event) {
    if (dateInput(event.target)) commit(event.target);
  }, true);
})();
