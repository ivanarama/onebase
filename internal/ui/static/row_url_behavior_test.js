'use strict';

// Адреса действий строки списка больше не приходят с сервера готовыми — их
// собирает obRowUrl() из опорных адресов контейнера. Пины на конкретные URL
// ушли из Go-тестов вместе с построчными data-*-атрибутами, поэтому точные
// строки проверяются здесь: перепутанные mark=1/mark=0 или active=1/active=0
// меняют смысл действия на противоположный, а сборка и вёрстка компилируются
// одинаково зелёными в обоих случаях.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const begin = source.indexOf('// BEGIN onebase-row-url');
const end = source.indexOf('// END onebase-row-url');
assert.ok(begin >= 0 && end > begin, 'row url markers must exist');

const ID = '11111111-1111-1111-1111-111111111111';
const BASE = '/ui/catalog/номенклатура';
const SUB = 'Продажи';
const SUB_Q = encodeURIComponent(SUB);
const ITEM = BASE + '/' + ID;

function slice() {
  const context = { URLSearchParams };
  vm.runInNewContext(source.slice(begin, end) + `
    this.api = {
      rowUrl: obRowUrl,
      paramURL: obRowParamURL,
      activityEnabled: obRowActivityEnabled,
      rowKey: obRowKey
    };`, context, { filename: 'ui-row-url-slice.js' });
  return context.api;
}

const api = slice();

// Контейнер списка: опорные адреса объявлены один раз на tbody/.tile-grid/table.
function container(over) {
  return {
    dataset: Object.assign({
      obRowBase: BASE,
      obRowSubsystem: SUB,
      obRowListUrl: BASE + '?parent=OLD&sort=code',
      obRowCopyUrl: BASE + '/new?subsystem=' + SUB_Q,
      obRowCanCopy: '1',
      obRowActivityEnabled: '1'
    }, over || {})
  };
}

// Строка, отрисованная сервером: несёт идентификатор и флаги состояния,
// ссылок в ней нет.
function row(box, over, openUrl) {
  return {
    dataset: Object.assign({ obId: ID }, over || {}),
    closest(selector) {
      return selector === '[data-ob-row-base]' ? (box || null) : null;
    },
    getAttribute(name) {
      return name === 'data-open-url' ? (openUrl || '') : '';
    }
  };
}

test('каждый вид действия даёт ровно тот адрес, что раньше приходил в строке', () => {
  const r = row(container());
  assert.equal(api.rowUrl(r, 'open'), ITEM + '?subsystem=' + SUB_Q);
  assert.equal(api.rowUrl(r, 'mark'), ITEM + '/delete?mark=1');
  assert.equal(api.rowUrl(r, 'unmark'), ITEM + '/delete?mark=0');
  assert.equal(api.rowUrl(r, 'del'), ITEM + '/delete');
  assert.equal(api.rowUrl(r, 'unpost'), ITEM + '/unpost');
  assert.equal(api.rowUrl(r, 'activityShow'), ITEM + '/activity?active=1');
  assert.equal(api.rowUrl(r, 'activityHide'), ITEM + '/activity?active=0');
  assert.equal(api.rowUrl(r, 'detail'), ITEM + '/detail-panel');
  assert.equal(api.rowUrl(r, 'folder'), BASE + '?parent=' + ID + '&sort=code');
  assert.equal(api.rowUrl(r, 'copy'), BASE + '/new?subsystem=' + SUB_Q + '&copy=' + ID);
});

// Пометка и её снятие, скрытие из выбора и возврат — противоположные действия
// с адресами, различающимися одним символом. Проверяем отдельно, что они не
// сошлись в одну строку.
test('парные действия не совпадают между собой', () => {
  const r = row(container());
  assert.notEqual(api.rowUrl(r, 'mark'), api.rowUrl(r, 'unmark'));
  assert.notEqual(api.rowUrl(r, 'activityShow'), api.rowUrl(r, 'activityHide'));
  assert.notEqual(api.rowUrl(r, 'del'), api.rowUrl(r, 'mark'));
});

test('без подсистемы адрес открытия идёт без параметра', () => {
  const r = row(container({ obRowSubsystem: '' }));
  assert.equal(api.rowUrl(r, 'open'), ITEM);
});

test('строка из JSON-подгрузки несёт свои ссылки, и они главнее контейнера', () => {
  const own = {
    openUrl: '/json/open', folderUrl: '/json/folder', markUrl: '/json/mark',
    unmarkUrl: '/json/unmark', delUrl: '/json/del', unpostUrl: '/json/unpost',
    activityShowUrl: '/json/show', activityHideUrl: '/json/hide',
    obDetailUrl: '/json/detail', copyUrl: '/json/copy'
  };
  const r = row(container(), own);
  assert.equal(api.rowUrl(r, 'open'), '/json/open');
  assert.equal(api.rowUrl(r, 'folder'), '/json/folder');
  assert.equal(api.rowUrl(r, 'mark'), '/json/mark');
  assert.equal(api.rowUrl(r, 'unmark'), '/json/unmark');
  assert.equal(api.rowUrl(r, 'del'), '/json/del');
  assert.equal(api.rowUrl(r, 'unpost'), '/json/unpost');
  assert.equal(api.rowUrl(r, 'activityShow'), '/json/show');
  assert.equal(api.rowUrl(r, 'activityHide'), '/json/hide');
  assert.equal(api.rowUrl(r, 'detail'), '/json/detail');
  assert.equal(api.rowUrl(r, 'copy'), '/json/copy');
});

// Своя ссылка строки имеет приоритет даже там, где контейнер запрещает действие:
// у подгруженной строки сервер уже решил, что показывать.
test('своя ссылка копирования работает при запрете на контейнере', () => {
  const r = row(container({ obRowCanCopy: '0' }), { copyUrl: '/json/copy' });
  assert.equal(api.rowUrl(r, 'copy'), '/json/copy');
});

test('без права записи копирование недоступно', () => {
  assert.equal(api.rowUrl(row(container({ obRowCanCopy: '0' })), 'copy'), '');
  assert.equal(api.rowUrl(row(container({ obRowCanCopy: '' })), 'copy'), '');
});

test('без идентификатора строки не собирается ни один адрес', () => {
  const r = row(container(), { obId: '' });
  ['open', 'mark', 'unmark', 'del', 'unpost', 'activityShow', 'activityHide',
    'detail', 'folder', 'copy'].forEach(function (kind) {
    assert.equal(api.rowUrl(r, kind), '', kind + ' без id должен быть пустым');
  });
});

test('строка вне контейнера и отсутствие строки дают пустой адрес', () => {
  assert.equal(api.rowUrl(row(null), 'open'), '');
  assert.equal(api.rowUrl(null, 'open'), '');
});

test('parent в адресе группы заменяется, а не добавляется вторым', () => {
  const r = row(container());
  const url = api.rowUrl(r, 'folder');
  assert.equal(url.split('parent=').length - 1, 1);
  assert.equal(url, BASE + '?parent=' + ID + '&sort=code');
});

test('адрес группы собирается и когда parent в списке ещё не было', () => {
  const r = row(container({ obRowListUrl: BASE + '?sort=code' }));
  assert.equal(api.rowUrl(r, 'folder'), BASE + '?sort=code&parent=' + ID);
});

// Раньше идентификатор подставлялся заменой слота __ID__ в строке шаблона.
// Тогда тот же текст, введённый пользователем в поиск, попадал в query списка
// и подменял слот. Параметр добавляется через URLSearchParams — проверяем, что
// пользовательский ввод остаётся дословно и не участвует в сборке.
test('пользовательский ввод в query не подменяет идентификатор', () => {
  const r = row(container({ obRowListUrl: BASE + '?q=__ID__' }));
  assert.equal(api.rowUrl(r, 'folder'), BASE + '?q=__ID__&parent=' + ID);
});

test('адрес без параметров и с якорем собирается корректно', () => {
  assert.equal(api.paramURL('/ui/list', 'parent', ID), '/ui/list?parent=' + ID);
  assert.equal(api.paramURL('/ui/list?sort=code#top', 'parent', ID),
    '/ui/list?sort=code&parent=' + ID + '#top');
});

// Кириллица в пути percent-кодирование не переживает: адрес перестал бы
// совпадать с тем, что отдаёт сервер. Трогаем только строку запроса.
test('кириллический путь остаётся как есть', () => {
  assert.equal(api.paramURL(BASE, 'parent', ID), BASE + '?parent=' + ID);
});

test('признак активности берётся со строки, иначе с контейнера', () => {
  assert.equal(api.activityEnabled(row(container(), { activityEnabled: '1' })), true);
  assert.equal(api.activityEnabled(row(container({ obRowActivityEnabled: '0' }),
    { activityEnabled: '1' })), true);
  assert.equal(api.activityEnabled(row(container())), true);
  assert.equal(api.activityEnabled(row(container({ obRowActivityEnabled: '0' }))), false);
  assert.equal(api.activityEnabled(row(null)), false);
  assert.equal(api.activityEnabled(null), false);
});

test('ключ выделения — идентификатор, с откатом на собственную ссылку строки', () => {
  assert.equal(api.rowKey(row(container())), ID);
  assert.equal(api.rowKey(row(container(), { obId: '' }, '/json/open')), '/json/open');
  assert.equal(api.rowKey(row(container(), { obId: '' })), '');
  assert.equal(api.rowKey(null), '');
});
