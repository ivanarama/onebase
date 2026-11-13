/* Optional enhanced list controls. Loaded only by the configured UI role scope. */
(function () {
  var baseMakeTreeRow = window.makeTreeRow;
  if (typeof baseMakeTreeRow === 'function') window.makeTreeRow = function (row) {
    var tr = baseMakeTreeRow(row);
    if (obListConfig().hideRowActions && tr.lastElementChild) tr.removeChild(tr.lastElementChild);
    return tr;
  };
window.obInitReportBlocks = function() {
  try {
    document.querySelectorAll('details.report-block').forEach(function (el) {
      if (el.classList && el.classList.contains('ob-list-settings') && document.querySelector('main.ob-list-panels')) return;
      var key = 'rb-' + location.pathname + '-' + el.dataset.block;
      var saved = localStorage.getItem(key);
      if (saved === '1') el.open = true;
      else if (saved === '0') el.open = false;
      el.addEventListener('toggle', function () { localStorage.setItem(key, el.open ? '1' : '0'); });
    });
  } catch (e) {}
};
function obInitListPanels() {
  var root = document.querySelector('main.ob-list-panels');
  if (!root) return;
  var scope = root.getAttribute('data-ob-list-panel-scope') || 'open-access';
  root.querySelectorAll('details[data-ob-list-panel], details.ob-list-settings').forEach(function (panel) {
    if (panel._obListPanelInit) return;
    panel._obListPanelInit = true;
    var name = panel.getAttribute('data-ob-list-panel') || 'settings';
    var key = 'ob-list-panel:v1:' + encodeURIComponent(scope) + ':' + location.pathname + ':' + name;
    var saved = null;
    try { saved = localStorage.getItem(key); } catch (e) {}
    panel.open = saved === '1';
    panel.addEventListener('toggle', function () {
      try { localStorage.setItem(key, panel.open ? '1' : '0'); } catch (e) {}
    });
  });
}

  obReady(obInitListPanels);
function obFillDashboardLists() {
  document.querySelectorAll('.dash').forEach(function (dash) {
    if (window.ResizeObserver && !dash._obFillObserver) {
      dash._obFillObserver = new ResizeObserver(function () { requestAnimationFrame(obFillDashboardLists); });
      dash._obFillObserver.observe(dash);
    }
    var rows = dash.querySelectorAll('.dash-row');
    if (!rows.length) return;
    var row = rows[rows.length - 1];
    row.querySelectorAll('.w-card-list').forEach(function (card) {
      if (!card.getClientRects().length) return;
      // Its natural top also works when the home container has been scrolled.
      var container = card.closest('#ob-tabhome') || card.closest('main');
      var bottom = container && container.id === "ob-tabhome" ? Math.min(innerHeight, container.getBoundingClientRect().bottom) : innerHeight;
      var top = card.getBoundingClientRect().top + (container ? container.scrollTop : 0);
      card.classList.add('ob-widget-fill');
      var height = Math.max(180, Math.floor(bottom - top - 8)) + 'px';
      if (card.style.height !== height) card.style.height = height;
    });
  });
}
document.addEventListener('dblclick', function (ev) {
  var cell = ev.target.closest && ev.target.closest('[data-ob-widget-cell]');
  var row = cell && cell.closest('[data-ob-row-url]');
  if (!row) return;
  var url = row.getAttribute('data-ob-row-url');
  var title = row.querySelector('td').textContent.trim();
  if (typeof window.obOpenTab === 'function') window.obOpenTab(url, title);
  else if (!(window.obOpenInShell && window.obOpenInShell(url, title))) window.location.assign(url);
});
// END onebase-widget-row-nav

// Widget cells keep their original typed token, even when a reference is shown as a caption.
(function () {
  function cardOf(el) { return el && el.closest && el.closest('[data-ob-widget-card]'); }
  function change(card, value) {
    var input = card.querySelector('[data-ob-widget-cell-input]');
    if (!input) return;
    input.value = value;
    input.dispatchEvent(new Event('change', {bubbles:true}));
    var clear = card.querySelector('[data-ob-widget-cell-clear]');
    if (clear) clear.disabled = !value;
  }
  function select(cell, card) {
    card.querySelectorAll('.ob-widget-cell-selected').forEach(function (el) { el.classList.remove('ob-widget-cell-selected'); });
    cell.classList.add('ob-widget-cell-selected');
    card._obSelectedWidgetCell = cell;
    if (window.MutationObserver && !card._obCellSelectionObserver) {
      card._obCellSelectionObserver = new MutationObserver(function () {
        var previous = card._obSelectedWidgetCell;
        if (previous && !card.contains(previous)) {
          card._obSelectedWidgetCell = null;
          var apply = card.querySelector('[data-ob-widget-cell-apply]');
          if (apply) apply.disabled = true;
        }
      });
      card._obCellSelectionObserver.observe(card, {childList:true,subtree:true});
    }
    var button = card.querySelector('[data-ob-widget-cell-apply]');
    if (button) button.disabled = false;
  }
  document.addEventListener('click', function (ev) {
    var el = ev.target;
    var card = cardOf(el);
    if (!card || !el.closest) return;
    var cell = el.closest('[data-ob-widget-cell]');
    if (cell && !el.closest('a,button,input,select,textarea')) { select(cell,card); ev.stopPropagation(); return; }
    if (el.closest('[data-ob-widget-cell-clear]')) { change(card,''); return; }
    if (!el.closest('[data-ob-widget-cell-apply]')) return;
    cell = card._obSelectedWidgetCell;
    if (!cell || !card.contains(cell)) return;
    change(card,JSON.stringify({field:cell.getAttribute('data-ob-widget-cell'),value:cell.getAttribute('data-ob-widget-cell-value')}));
  }, true);
  document.addEventListener('focusin', function (ev) {
    var cell = ev.target.closest && ev.target.closest('[data-ob-widget-cell]');
    var card = cardOf(cell);
    if (cell && card) select(cell,card);
  });
})();


  window.addEventListener('resize', obFillDashboardLists);
  var baseDashboard = window.obInitDashboard;
  window.obInitDashboard = function () {
    if (typeof baseDashboard === 'function') baseDashboard();
    obFillDashboardLists();
  };
  obReady(obFillDashboardLists);

  // Enter completes a filter/settings field. Search keeps its existing submit.
  document.addEventListener('keydown', function (ev) {
    if (ev.defaultPrevented || ev.key !== 'Enter' || ev.isComposing || ev.ctrlKey || ev.altKey || ev.metaKey) return;
    var el = ev.target;
    if (!el || !el.closest || !el.closest('main.ob-list-panels') ||
        !el.matches('input,select') || el.matches('input[type=submit],input[type=button]') ||
        el.name === 'q' || el.closest('.ob-list-search,[data-ob-list-search]')) return;
    ev.preventDefault(); ev.stopPropagation();
    var form = el.form;
    var fields = form ? Array.prototype.filter.call(form.querySelectorAll('input,select,textarea'),function (field) {
      return !field.disabled && field.type !== 'hidden' && field.getClientRects().length;
    }) : [];
    var next = fields[fields.indexOf(el) + 1];
    if (next) next.focus(); else el.blur();
  }, true);
})();
