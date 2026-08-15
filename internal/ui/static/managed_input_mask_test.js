// Поведение маски ввода (#763, п. 3) — на настоящем коде из managed.js.
//
// Функции вырезаются из файла, а не копируются в тест: копия разошлась бы с
// оригиналом, и тест продолжал бы утверждать про код, которого нет.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync('static/managed.js', 'utf8');

function extract(name) {
  const start = source.indexOf('function ' + name);
  if (start < 0) throw new Error('в managed.js нет функции ' + name);
  let depth = 0;
  for (let i = source.indexOf('{', start); i < source.length; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') {
      depth--;
      if (depth === 0) return source.slice(start, i + 1);
    }
  }
  throw new Error('не закрыта функция ' + name);
}

const runtime = extract('obInputMaskFits') + '\n' + extract('obApplyInputMask') + '\n' + extract('obInputMaskCaret');
const applyInputMask = new Function(runtime + '\nreturn obApplyInputMask;')();
const inputMaskCaret = new Function(runtime + '\nreturn obInputMaskCaret;')();

test('цифровой шаблон расставляет разделители сам', () => {
  assert.equal(applyInputMask('00.00.00', '123456'), '12.34.56');
});

test('повторный ввод уже отформатированного значения ничего не удваивает', () => {
  // Событие input приходит на каждый символ, и значение прогоняется через маску
  // снова. Без съедания литералов «12.34.56» превратилось бы в «12..34..56».
  assert.equal(applyInputMask('00.00.00', '12.34.56'), '12.34.56');
});

test('висячий разделитель не дописывается', () => {
  // Иначе набравший половину и ушедший с поля сохранил бы «12.» — значение с
  // мусорным хвостом, которое ещё и не пройдёт pattern.
  assert.equal(applyInputMask('00.00.00', '12'), '12');
  assert.equal(applyInputMask('00.00.00', '1'), '1');
});

test('лишние символы отсекаются по длине шаблона', () => {
  assert.equal(applyInputMask('00.00.00', '12345678'), '12.34.56');
});

test('символы не по типу заполнителя пропускаются', () => {
  // Вставка из буфера с чужими разделителями не должна ломать раскладку.
  assert.equal(applyInputMask('00.00.00', '12ab34'), '12.34');
  assert.equal(applyInputMask('00.00.00', '12/34/56'), '12.34.56');
});

test('литералы перед первым заполнителем ставятся сразу', () => {
  assert.equal(applyInputMask('+7 (000) 000-00-00', '9161234567'), '+7 (916) 123-45-67');
});

test('цифра-литерал шаблона не съедает цифру пользователя', () => {
  // Вставленный номер с кодом страны: «7» из ввода совпадает с литералом «7»
  // шаблона и обязана быть им поглощена. Иначе она встанет в первый заполнитель
  // и весь номер съедет на разряд: «+7 (791) 612-34-56».
  assert.equal(applyInputMask('+7 (000) 000-00-00', '79161234567'), '+7 (916) 123-45-67');
});

test('буквенные и смешанные заполнители', () => {
  assert.equal(applyInputMask('XX-000', 'ab123'), 'ab-123');
  assert.equal(applyInputMask('XX-000', '12ab34'), 'ab-34');
  assert.equal(applyInputMask('*000', 'a123'), 'a123');
  assert.equal(applyInputMask('*000', '1234'), '1234');
});

test('пустой ввод остаётся пустым', () => {
  assert.equal(applyInputMask('00.00.00', ''), '');
});

test('каретка держится на месте при правке в середине поля', () => {
  // Присваивание el.value само отправляет каретку в конец, поэтому позицию
  // надо считать: часть ввода до каретки, прогнанная через маску, и даёт её
  // новое положение. Иначе каждое нажатие в середине выбрасывало бы курсор.
  //
  // Набрано «1234», каретка после «12» — пользователь допечатывает в середину.
  assert.equal(inputMaskCaret('00.00.00', '1234', 2), 2);
  // Каретка сразу за разделителем: «12.34», позиция 3 — после точки.
  assert.equal(inputMaskCaret('00.00.00', '12.34', 3), 3);
  // Каретка в конце — позиция совпадает с длиной отформатированного значения.
  assert.equal(inputMaskCaret('00.00.00', '123456', 6), applyInputMask('00.00.00', '123456').length);
});

test('каретка не считается, если позиции нет', () => {
  // selectionStart недоступен на некоторых типах полей — тогда позицию не
  // трогаем вовсе, а не ставим её в 0.
  assert.equal(inputMaskCaret('00.00.00', '1234', null), null);
  assert.equal(inputMaskCaret('00.00.00', '1234', undefined), null);
});
