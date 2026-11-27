(function () {
  'use strict';
  function update(root) {
    root.querySelectorAll('[data-report-param-visible]').forEach(function (container) {
      var form = container.closest('form');
      var when;
      try { when = JSON.parse(container.getAttribute('data-report-param-visible')); }
      catch (_) { container.hidden = true; return; }
      var control = form && when && form.elements.namedItem(when.param);
      if (!control || control.type !== 'checkbox' || typeof when.equals !== 'boolean') {
        container.hidden = true;
        return;
      }
      // Hiding is presentation only: never disable, clear or rewrite either
      // period. All independent values remain in the submitted form.
      container.hidden = control.checked !== when.equals;
    });
  }
  if (!window.obReportParamVisibilityInstalled) {
    window.obReportParamVisibilityInstalled = true;
    document.addEventListener('change', function (event) {
      if (event.target && event.target.form) update(event.target.form);
    });
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { update(document); });
  } else {
    update(document);
  }
}());
