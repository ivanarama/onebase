const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// Заголовок вкладки по ссылке внутри вкладки оболочки: подсказка горячей
// клавиши (title="Insert" aria-keyshortcuts="Insert" у кнопки «Создать») не
// становится заголовком — берётся текст ссылки.
const uiSource = fs.readFileSync(path.join(__dirname, 'ui.js'), 'utf8');
const begin = uiSource.indexOf('// BEGIN onebase-link-tab-title');
const end = uiSource.indexOf('// END onebase-link-tab-title', begin);
if (begin < 0 || end < 0) throw new Error('link tab title source markers not found');
const context = {};
vm.runInNewContext(uiSource.slice(begin, end), context);

function link(attrs, text) {
  return {
    textContent: text,
    getAttribute: (name) => (Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null),
  };
}

test('подсказка горячей клавиши не становится заголовком вкладки', () => {
  const create = link({ title: 'Insert', 'aria-keyshortcuts': 'Insert' }, '\n  + Создать  ');
  assert.equal(context.obLinkTabTitle(create), '+ Создать');
});

test('обычный title ссылки остаётся заголовком', () => {
  assert.equal(context.obLinkTabTitle(link({ title: 'Звонок 000123' }, 'открыть')), 'Звонок 000123');
  assert.equal(context.obLinkTabTitle(link({ title: 'Карточка', 'aria-keyshortcuts': 'F2' }, 'x')), 'Карточка');
});

test('без title и текста — «Форма»', () => {
  assert.equal(context.obLinkTabTitle(link({}, '  ')), 'Форма');
  assert.equal(context.obLinkTabTitle(link({ title: 'Insert', 'aria-keyshortcuts': 'Insert' }, '')), 'Форма');
});
