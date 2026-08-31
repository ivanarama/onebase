'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '..', 'static', 'configurator.js'), 'utf8');
const start = source.indexOf('// Иконка по виду дескриптора');
const end = source.indexOf('// Темы кода', start);
assert.ok(start >= 0 && end > start, 'langref providers not found in configurator.js');

const providers = {};
const monaco = {
  languages: {
    CompletionItemKind: {Method: 1, Keyword: 2, Struct: 3, Function: 4},
    CompletionItemInsertTextRule: {InsertAsSnippet: 1},
    registerCompletionItemProvider(_lang, provider) { providers.completion = provider; },
    registerHoverProvider(_lang, provider) { providers.hover = provider; },
    registerSignatureHelpProvider(_lang, provider) { providers.signature = provider; },
  },
};

function method(object, name, alias, params = []) {
  return {
    kind: 'method',
    object,
    name: name.toLowerCase(),
    display: name,
    aliases: [alias],
    signature: `${object}.${name}(${params.map((p) => p.name).join(', ')})`,
    params,
    doc: `${object}: ${name}`,
  };
}

const data = [
  method('Массив', 'Добавить', 'Add', [{name: 'Значение'}]),
  method('Массив', 'Удалить', 'Delete', [{name: 'Индекс'}]),
  method('Массив', 'Количество', 'Count'),
  method('Массив', 'Найти', 'Find', [{name: 'Значение'}]),
  method('ТаблицаЗначений', 'Добавить', 'Add'),
  method('ТаблицаЗначений', 'Удалить', 'Delete', [{name: 'Индекс'}]),
  method('ТаблицаЗначений', 'Количество', 'Count'),
  method('ТаблицаЗначений', 'Найти', 'Find', [{name: 'Значение'}, {name: 'Колонка'}]),
  method('Страница.График', 'ДобавитьСерию', 'AddSeries', [{name: 'Имя'}, {name: 'Цвет'}]),
  {kind: 'func', name: 'сообщить', display: 'Сообщить', aliases: ['Message'], signature: 'Сообщить(Текст)', params: [{name: 'Текст'}], doc: 'Пишет сообщение.'},
];

const context = {
  monaco,
  window: {_langref: data},
  loadLangref() { return Promise.resolve(data); },
  String,
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context, {filename: 'configurator-langref.js'});

function wordAt(text, position) {
  let index = Math.max(0, position.column - 2);
  const isWord = (ch) => /[A-Za-zА-Яа-яЁё0-9_]/.test(ch || '');
  while (index > 0 && isWord(text[index - 1])) index--;
  let end = Math.max(index, position.column - 1);
  while (end < text.length && isWord(text[end])) end++;
  if (index === end) return null;
  return {word: text.slice(index, end), startColumn: index + 1, endColumn: end + 1};
}

function model(text) {
  return {
    getLineContent() { return text; },
    getWordAtPosition(position) { return wordAt(text, position); },
    getWordUntilPosition(position) {
      const end = position.column - 1;
      let start = end;
      while (start > 0 && /[A-Za-zА-Яа-яЁё0-9_]/.test(text[start - 1])) start--;
      return {word: text.slice(start, end), startColumn: start + 1, endColumn: end + 1};
    },
    getValueInRange(range) { return text.slice(0, range.endColumn - 1); },
  };
}

function positionAt(text, word) {
  const index = text.lastIndexOf(word);
  assert.notEqual(index, -1, `word ${word} not found in ${text}`);
  return {lineNumber: 1, column: index + 2};
}

function hover(text, word) {
  return providers.hover.provideHover(model(text), positionAt(text, word));
}

function signature(text) {
  return providers.signature.provideSignatureHelp(model(text), {lineNumber: 1, column: text.length + 1});
}

test('completion filters all common method names by the known receiver', () => {
  const text = 'ТаблицаЗначений.';
  const result = providers.completion.provideCompletionItems(model(text), {lineNumber: 1, column: text.length + 1});
  assert.deepEqual(
    Array.from(result.suggestions, (item) => `${item.label}:${item.detail}`),
    [
      'Добавить:ТаблицаЗначений.Добавить()',
      'Удалить:ТаблицаЗначений.Удалить(Индекс)',
      'Количество:ТаблицаЗначений.Количество()',
      'Найти:ТаблицаЗначений.Найти(Значение, Колонка)',
    ],
  );
});

test('hover resolves canonical names and aliases against the receiver', () => {
  assert.match(hover('ТаблицаЗначений.Добавить', 'Добавить').contents[0].value, /ТаблицаЗначений\.Добавить/);
  assert.match(hover('ТаблицаЗначений.Add', 'Add').contents[0].value, /ТаблицаЗначений\.Добавить/);
  assert.match(hover('Массив.Удалить', 'Удалить').contents[0].value, /Массив\.Удалить/);
  assert.match(hover('ТаблицаЗначений.Количество', 'Количество').contents[0].value, /ТаблицаЗначений\.Количество/);
  assert.equal(hover('НеизвестныйОбъект.Найти', 'Найти'), null);
  assert.match(hover('Сообщить', 'Сообщить').contents[0].value, /Сообщить\(Текст\)/);
});

test('signature handles aliases, compound objects and nested arguments', () => {
  const nested = signature('ТаблицаЗначений.Найти(Функция(1, 2), ');
  assert.equal(nested.value.signatures[0].label, 'ТаблицаЗначений.Найти(Значение, Колонка)');
  assert.equal(nested.value.activeParameter, 1);

  const alias = signature('Массив.Delete(');
  assert.equal(alias.value.signatures[0].label, 'Массив.Удалить(Индекс)');

  const compound = signature('Страница . График . ДобавитьСерию("A, B", ');
  assert.equal(compound.value.signatures[0].label, 'Страница.График.ДобавитьСерию(Имя, Цвет)');
  assert.equal(compound.value.activeParameter, 1);

  assert.equal(signature('НеизвестныйОбъект.Найти('), null);
});
