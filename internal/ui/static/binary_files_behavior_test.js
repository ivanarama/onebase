// Поля файла параметров обработки type: binary в событиях управляемой формы —
// боевой код managed.js в поддельном DOM. Пустое поле не должно попадать в
// urlencoded-тело пустой строкой (сервер отклонил бы «двоичный файл текстом»),
// а выбранные файлы обязаны уйти multipart-ом вместе с прочими полями.

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'managed.js'), 'utf8');
function slice(from, to) {
  const start = source.indexOf(from);
  const end = source.indexOf(to, start);
  assert.ok(start >= 0 && end > start, 'production slice not found: ' + from);
  return source.slice(start, end);
}
const context = {FormData, Blob, File, URLSearchParams};
vm.runInNewContext(slice('  function obBinaryFileFields(form){', '  async function snapshotFormEvent(') +
  slice('  function obFormEventRequest(body, files, headers){', '  window.obFire = function'), context);

function fakeForm(inputs) {
  return {querySelectorAll(selector) {
    assert.equal(selector, 'input[type="file"][data-ob-binary-file]');
    return inputs;
  }};
}

test('binary fields: names always excluded, files only from enabled inputs', () => {
  const a = new File(['12345'], 'Заявка 1.jpg', {type: 'image/jpeg'});
  const b = new File(['123'], 'Заявка 2.jpg');
  const fields = context.obBinaryFileFields(fakeForm([
    {name: 'Сканы', disabled: false, files: [a, b]},
    {name: 'Пустое', disabled: false, files: []},
    {name: 'Выключено', disabled: true, files: [a]},
    {name: '', disabled: false, files: [a]},
  ]));
  assert.equal(JSON.stringify(Object.keys(fields.names).sort()), JSON.stringify(['Выключено', 'Пустое', 'Сканы']));
  assert.equal(JSON.stringify(fields.files.map(pair => [pair[0], pair[1].name])), JSON.stringify([['Сканы', 'Заявка 1.jpg'], ['Сканы', 'Заявка 2.jpg']]));
});

test('event request: urlencoded without files, multipart with files', async () => {
  const body = new URLSearchParams({_element: 'Загрузить', _event: 'Нажатие', Комментарий: 'пачка'});
  const headers = {'Content-Type': 'application/x-www-form-urlencoded; charset=utf-8', 'X-OneBase-Conceal-Context': 'ctx'};
  const plain = context.obFormEventRequest(body, [], headers);
  assert.equal(plain.body, body);
  assert.equal(plain.headers, headers);

  const file = new File(['12345'], 'Заявка 1.jpg', {type: 'image/jpeg'});
  const multi = context.obFormEventRequest(body, [['Сканы', file]], headers);
  assert.ok(multi.body instanceof FormData);
  assert.equal(multi.headers['Content-Type'], undefined, 'boundary must be set by the browser');
  assert.equal(multi.headers['X-OneBase-Conceal-Context'], 'ctx');
  assert.equal(headers['Content-Type'], 'application/x-www-form-urlencoded; charset=utf-8', 'caller headers untouched');
  assert.equal(multi.body.get('Комментарий'), 'пачка');
  assert.equal(multi.body.get('_event'), 'Нажатие');
  const sent = multi.body.get('Сканы');
  assert.equal(sent.name, 'Заявка 1.jpg');
  assert.equal(await sent.text(), '12345');
});
