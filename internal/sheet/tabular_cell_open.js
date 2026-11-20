(function () {
  'use strict';
  const table = document.querySelector('.doc-content > table');
  if (!table) return;

  // The first native click still follows the link. Its second click must not
  // open another object tab or run the managed-form handler a second time.
  table.addEventListener('click', function (event) {
    if (event.detail < 2 || !event.target.closest('a[data-sheet-drilldown]')) return;
    event.preventDefault();
    event.stopPropagation();
  }, true);

  table.addEventListener('dblclick', function (event) {
    if (event.defaultPrevented || event.button !== 0 ||
        event.target.closest('a, button, input, select, textarea')) return;
    const cell = event.target.closest('td');
    if (!cell || cell.closest('table') !== table) return;
    const link = cell.querySelector(':scope > a[data-sheet-drilldown]');
    if (!link) return;
    event.preventDefault();
    link.click();
  });
})();
