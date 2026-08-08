// code-theme.js — тема редактора кода, общая для страниц лаунчера с Monaco:
// конфигуратора (модули, запросы, YAML) и конструктора управляемых форм.
// Выбор один на весь лаунчер: страницы живут на одном origin, значит и
// localStorage у них общий.
//
// Пока выбор не сделан, каждая страница остаётся в своём историческом виде —
// конфигуратор тёмный, конструктор форм светлый (window.cfgCodeThemeDefault
// задаётся страницей). Один общий дефолт молча перекрасил бы одну из них.
//
// Класс cfg-code-light ставится на <html> ещё в <head> каждой страницы: иначе
// сохранённая светлая тема применяется после отрисовки и блоки кода мигают
// тёмным.

// Текущая тема — по классу на <html>, чтобы CSS и Monaco не разъезжались.
function cfgCodeThemeName() {
  return document.documentElement.classList.contains('cfg-code-light') ? 'onebase-light' : 'onebase-dark';
}

// cfgCodeThemeDefine — объявить обе темы и применить текущую. Зовётся из
// require-колбэка страницы: defineTheme сам тему не активирует, а без setTheme
// до создания первого редактора активной остаётся встроенная vs.
function cfgCodeThemeDefine(monaco) {
  monaco.editor.defineTheme('onebase-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'keyword', foreground: 'c792ea', fontStyle: 'bold' },
      { token: 'type', foreground: '82aaff' },
      { token: 'variable.predefined', foreground: 'ff5370', fontStyle: 'bold' },
      { token: 'string', foreground: 'c3e88d' },
      { token: 'number', foreground: 'f78c6c' },
      { token: 'comment', foreground: '546e7a', fontStyle: 'italic' }
    ],
    colors: {
      'editor.background': '#1e1e2e',
      'editor.foreground': '#cdd6f4',
      'editor.lineHighlightBackground': '#2a2a3e',
      'editorLineNumber.foreground': '#6c7086',
      'editorLineNumber.activeForeground': '#cdd6f4',
      'editor.selectionBackground': '#45475a',
      'editorCursor.foreground': '#f5e0dc'
    }
  });
  // Светлая тема — те же роли токенов, но подобранные под белый фон: палитру
  // тёмной темы переносить нельзя (лиловый c792ea и серо-синий комментарий
  // 546e7a на белом уже нечитаемы). Цвета парные к переменным --cfg-hl-* в
  // configurator.css, которыми покрашен pre.os-code.
  monaco.editor.defineTheme('onebase-light', {
    base: 'vs',
    inherit: true,
    rules: [
      { token: 'keyword', foreground: '7c3aed', fontStyle: 'bold' },
      { token: 'type', foreground: '1d4ed8' },
      { token: 'variable.predefined', foreground: 'b91c1c', fontStyle: 'bold' },
      { token: 'string', foreground: '15803d' },
      { token: 'number', foreground: 'b45309' },
      { token: 'comment', foreground: '64748b', fontStyle: 'italic' }
    ],
    colors: {
      'editor.background': '#ffffff',
      'editor.foreground': '#1f2937',
      'editor.lineHighlightBackground': '#f3f6fb',
      'editorLineNumber.foreground': '#9aa4b2',
      'editorLineNumber.activeForeground': '#1f2937',
      'editor.selectionBackground': '#cfe2ff',
      'editorCursor.foreground': '#1a4a80'
    }
  });
  monaco.editor.setTheme(cfgCodeThemeName());
}

// cfgCodeThemeToggle — переключение из кнопки в шапке страницы. Тема Monaco
// глобальна на страницу: одного setTheme хватает на все открытые редакторы,
// перебирать их не нужно.
function cfgCodeThemeToggle() {
  var light = document.documentElement.classList.toggle('cfg-code-light');
  try { localStorage.setItem('cfgCodeTheme', light ? 'light' : 'dark'); } catch (e) {}
  if (typeof monaco !== 'undefined' && monaco.editor) monaco.editor.setTheme(cfgCodeThemeName());
}
