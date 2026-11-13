/* Optional compact-report keyboard behavior. Report generation stays explicit. */
(function () {
  'use strict';
  document.addEventListener('change', function (event) {
    var el = event.target;
    if (!el || !el.closest || !el.closest('main.ob-report-compact') || !el.closest('form[data-ob-report-params]')) return;
    var variant = el.closest('[data-ob-report-variant-submit]');
    if (!variant && !el.closest('[data-ob-report-preset-submit]')) return;
    if (typeof window.obReportRemoveSettingsInput === 'function') window.obReportRemoveSettingsInput(el.form);
    if (variant) {
      var preset = el.form.querySelector('select[name="__preset"]');
      if (preset) preset.value = '__standard';
    }
    // Keep the new selection in the parameter form until explicit Generate.
    event.stopPropagation();
  }, true);
  document.addEventListener('keydown', function (event) {
    if (event.defaultPrevented || event.key !== 'Enter' || event.isComposing || event.ctrlKey || event.altKey || event.metaKey || event.shiftKey) return;
    var el = event.target;
    if (!el || !el.closest || !el.closest('main.ob-report-compact') || !el.closest('form[data-ob-report-params]')) return;
    if (el.tagName !== 'INPUT' && el.tagName !== 'SELECT') return;
    if (['button','submit','reset','file','checkbox','radio','hidden'].indexOf(String(el.type || '').toLowerCase()) >= 0) return;
    event.preventDefault(); event.stopPropagation();
    var controls = Array.from(el.form.querySelectorAll('input,select,textarea')).filter(function (field) {
      return !field.disabled && !field.readOnly && field.type !== 'hidden' && field.getClientRects().length;
    });
    var next = controls[controls.indexOf(el) + 1];
    if (next) next.focus(); else el.blur();
  }, true);
})();
