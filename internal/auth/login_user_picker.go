package auth

import "html/template"

// Список пользователей на странице входа. Раньше это был <datalist>, и у него
// два неустранимых свойства. Браузер показывает только варианты, совпадающие с
// уже набранным текстом: когда он сам подставил сохранённый логин, по стрелке
// виден один пользователь, а весь список — лишь после ручной очистки поля. И
// смена пользователя не трогала пароль: оставался подставленный браузером
// пароль прежнего. Поэтому список свой: ▼ и стрелка вниз показывают всех,
// набор — отфильтрованных; ручная смена имени очищает пароль.
//
// Логика — чистыми функциями (их проверяет node-тест), привязка к странице —
// только при наличии document.
const loginUserPickerJS = `(function(){
  function norm(s){ return String(s == null ? '' : s).toLowerCase(); }
  // Пользователи для показа: все, когда список открыт стрелкой; иначе те, у
  // кого набранное входит в логин или имя.
  function filterUsers(users, query, showAll){
    var q = norm(query).trim();
    if (showAll || q === '') return users.slice();
    return users.filter(function(u){
      return norm(u.login).indexOf(q) >= 0 || norm(u.name).indexOf(q) >= 0;
    });
  }
  // Следующая активная строка при ↑/↓ — по кругу; -1 — ничего не выделено.
  function moveIndex(index, delta, count){
    if (count <= 0) return -1;
    if (index < 0) return delta > 0 ? 0 : count - 1;
    return (index + delta + count) % count;
  }
  // Пароль очищается, только когда имя сменил человек: набором или выбором из
  // списка. Подстановка браузером без события ручного редактирования пароль не трогает.
  function shouldClearPassword(before, after, byUser){
    return !!byUser && String(before) !== String(after);
  }
  var api = {filterUsers: filterUsers, moveIndex: moveIndex, shouldClearPassword: shouldClearPassword};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  if (typeof document === 'undefined') return;

  var input = document.getElementById('ob-login');
  var list = document.getElementById('ob-users');
  var toggle = document.getElementById('ob-users-toggle');
  var password = document.getElementById('ob-password');
  if (!input || !list) return;
  var users = Array.prototype.map.call(list.querySelectorAll('li[data-login]'), function(li){
    return {login: li.getAttribute('data-login'), name: li.getAttribute('data-name') || '', el: li};
  });
  var shown = [], active = -1, editing = false, composing = false, last = input.value;

  // Автозаполнение может отправить input с пустыми data/inputType либо
  // insertReplacementText. Ручной ввод определяем по самому редактированию,
  // включая меню буфера обмена, drag-and-drop, undo и IME без keydown.
  function manualEdit(e){
    var type = e.inputType || '';
    return e.isComposing || composing || /^(delete|history|insertFrom|insertComposition)/.test(type) ||
      (type === 'insertText' && e.data != null);
  }
  function markEditing(){
    // Менеджер паролей мог изменить value без input: снимок нужен до правки.
    last = input.value;
    editing = true;
    // Отменённая вставка/вырезание не должны влиять на позднее автозаполнение.
    setTimeout(function(){ editing = false; }, 0);
  }
  input.addEventListener('beforeinput', function(e){ if (manualEdit(e)) markEditing(); });
  ['cut', 'paste', 'drop'].forEach(function(type){ input.addEventListener(type, markEditing); });
  input.addEventListener('compositionstart', function(){ last = input.value; composing = true; });
  input.addEventListener('compositionend', function(){ composing = false; });

  function render(){
    users.forEach(function(u){ u.el.hidden = shown.indexOf(u) < 0; u.el.classList.remove('active'); u.el.setAttribute('aria-selected', 'false'); });
    if (active >= 0 && shown[active]) {
      shown[active].el.classList.add('active');
      shown[active].el.setAttribute('aria-selected', 'true');
      shown[active].el.scrollIntoView({block: 'nearest'});
    }
  }
  function open(showAll){
    shown = filterUsers(users, input.value, showAll);
    active = -1;
    shown.forEach(function(u, i){ if (u.login === input.value) active = i; });
    list.hidden = shown.length === 0;
    input.setAttribute('aria-expanded', list.hidden ? 'false' : 'true');
    render();
  }
  function close(){
    list.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    active = -1;
  }
  function choose(u){
    if (shouldClearPassword(input.value, u.login, true) && password) password.value = '';
    input.value = u.login;
    last = input.value;
    close();
    if (password) password.focus();
  }

  input.addEventListener('keydown', function(e){
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (list.hidden) { open(true); if (e.key === 'ArrowUp') active = moveIndex(-1, -1, shown.length); else if (active < 0) active = moveIndex(-1, 1, shown.length); render(); return; }
      active = moveIndex(active, e.key === 'ArrowDown' ? 1 : -1, shown.length);
      render();
      return;
    }
    if (e.key === 'Enter' && !list.hidden && active >= 0 && shown[active]) { e.preventDefault(); choose(shown[active]); return; }
    if (e.key === 'Escape' && !list.hidden) { e.preventDefault(); close(); return; }
    if (e.key === 'Tab') { close(); return; }
  });
  input.addEventListener('input', function(e){
    var byUser = editing || manualEdit(e);
    if (shouldClearPassword(last, input.value, byUser) && password) password.value = '';
    editing = false;
    last = input.value;
    if (byUser) open(false); else close();
  });
  input.addEventListener('blur', function(){ editing = false; composing = false; setTimeout(close, 150); });
  if (toggle) {
    // mousedown, а не click: поле не теряет фокус и список не закрывается.
    toggle.addEventListener('mousedown', function(e){
      e.preventDefault();
      if (list.hidden) open(true); else close();
      input.focus();
    });
  }
  list.addEventListener('mousedown', function(e){
    var li = e.target.closest ? e.target.closest('li[data-login]') : null;
    if (!li) return;
    e.preventDefault();
    for (var i = 0; i < users.length; i++) if (users[i].el === li) { choose(users[i]); return; }
  });
})();`

// loginUserPickerScript — тот же код для шаблона: html/template экранирует
// строку внутри <script>, а template.JS вставляет как есть (код постоянный).
var loginUserPickerScript = template.JS(loginUserPickerJS) //nolint:gosec // G203: константа пакета, не данные пользователя
