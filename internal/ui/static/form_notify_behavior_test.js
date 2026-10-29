const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync('static/managed.js', 'utf8');
const start = source.indexOf('function obManagedNotifyListener(cfg, fire)');
const end = source.indexOf('obManagedReady(function', start);
if (start < 0 || end < 0) throw new Error('obManagedNotifyListener is absent from managed.js');
const ctx = vm.createContext({});
vm.runInContext(source.slice(start, end) + '\nthis.listener = obManagedNotifyListener;', ctx);

// Оповестить на сервере → SSE ui.оповестить {id, событие}: форма с
// обработчиком ОбработкаОповещения отправляет обычное событие формы уровня
// формы (без элемента) с идентификатором оповещения.
test('form with notify handler fires ОбработкаОповещения with the notification id', () => {
  const fired = [];
  const listener = ctx.listener({notify: true}, (...args) => fired.push(args));
  listener({detail: {id: 'abc123', 'событие': 'ВозвратВФормуЗвонка'}});
  assert.equal(fired.length, 1);
  assert.equal(fired[0][0], '');
  assert.equal(fired[0][1], 'ОбработкаОповещения');
  assert.equal(fired[0][2]._notify_id, 'abc123');
});

test('form without notify handler and malformed events fire nothing', () => {
  const fired = [];
  ctx.listener({notify: false}, (...args) => fired.push(args))({detail: {id: 'abc'}});
  const listener = ctx.listener({notify: true}, (...args) => fired.push(args));
  listener({detail: null});
  listener({detail: {}});
  listener({});
  assert.equal(fired.length, 0);
});
