(function () {
  'use strict';
  const group = document.querySelector('colgroup[data-sheet-columns]');
  if (!group) return;

  const table = group.closest('table');
  const columns = Array.from(group.children);
  let drag;
  let layoutFrame = 0;

  function boundaryColumn(event) {
    const cell = event.target.closest('td[data-sheet-col]');
    if (!cell || cell.closest('table') !== table) return null;
    const distance = cell.getBoundingClientRect().right - event.clientX;
    if (distance < 0 || distance > 5) return null;
    const index = Number(cell.dataset.sheetCol) + cell.colSpan - 1;
    return columns[index] ? index : null;
  }

  function updateFrozenCells() {
    if (layoutFrame) return;
    layoutFrame = requestAnimationFrame(function () {
      layoutFrame = 0;
      table.dispatchEvent(new Event('sheet-column-resized'));
    });
  }

  table.addEventListener('pointerdown', function (event) {
    if (event.button !== 0 || event.pointerType === 'touch' || drag) return;
    const index = boundaryColumn(event);
    if (index === null) return;
    const widths = columns.map(function (column) {
      return column.getBoundingClientRect().width;
    });
    if (widths.some(function (width) { return width <= 0; })) return;
    drag = {
      id: event.pointerId,
      x: event.clientX,
      index: index,
      widths: widths,
      total: table.getBoundingClientRect().width,
      started: false
    };
    table.style.cursor = 'col-resize';
    table.setPointerCapture(event.pointerId);
    event.preventDefault();
  });

  table.addEventListener('pointermove', function (event) {
    if (!drag) {
      table.style.cursor = boundaryColumn(event) === null ? '' : 'col-resize';
      return;
    }
    if (event.pointerId !== drag.id) return;
    const delta = event.clientX - drag.x;
    if (!drag.started) {
      if (Math.abs(delta) < 2) return;
      columns.forEach(function (column, index) {
        column.style.width = drag.widths[index] + 'px';
      });
      table.style.tableLayout = 'fixed';
      table.classList.add('sheet-column-widths-set');
      drag.started = true;
    }
    const initialWidth = drag.widths[drag.index];
    const width = Math.max(24, initialWidth + delta);
    columns[drag.index].style.width = width + 'px';
    table.style.width = (drag.total + width - initialWidth) + 'px';
    updateFrozenCells();
    event.preventDefault();
  });

  function finish(event) {
    if (!drag || event.pointerId !== drag.id) return;
    drag = null;
    table.style.cursor = '';
  }

  table.addEventListener('pointerup', finish);
  table.addEventListener('pointercancel', finish);
  table.addEventListener('lostpointercapture', finish);
  table.addEventListener('pointerleave', function () {
    if (!drag) table.style.cursor = '';
  });
})();
