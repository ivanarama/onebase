package launcher

// ── Partial: переводы (titles-block) ─────────────────────────────────────────

// Сами поля перевода (по языку). Видимость — глобально через режим переводов
// (класс html.cfg-titles-on, кнопка 🌐 в топбаре); по умолчанию .titles-block
// скрыт, поэтому у каждого реквизита больше не висит строка-спойлер «Переводы».
const cfgTitlesBlock = `{{define "titles-block"}}
<div class="titles-block">
  {{range .Langs}}{{if ne .Code "ru"}}
  <div style="display:flex;gap:6px;margin-bottom:3px;align-items:center">
    <span style="width:78px;color:#888;font-size:12px">🌐 {{.Native}}</span>
    <input type="text" name="{{$.Prefix}}.{{.Code}}" value="{{index $.Values .Code}}"
           style="flex:1;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
  </div>
  {{end}}{{end}}
</div>
{{end}}
`

// ── Head / foot ───────────────────────────────────────────────────────────────

const cfgHead = `{{define "cfg-head"}}<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<script>
// Самохостинг Monaco: web-воркер грузится из встроенного /vendor/monaco/
// (тот же origin), иначе AMD-воркер не знает baseUrl и падает.
window.MonacoEnvironment = { getWorkerUrl: function () {
  return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(
    "self.MonacoEnvironment={baseUrl:'" + location.origin + "/vendor/monaco/'};" +
    "importScripts('" + location.origin + "/vendor/monaco/vs/base/worker/workerMain.js');");
}};
</script>
<script>
// Режим переводов запоминается между сессиями (одна кнопка 🌐 в топбаре вместо
// спойлера у каждого реквизита). Класс ставим до отрисовки — поля не «прыгают».
try{if(localStorage.getItem('cfgTitlesOn')==='1')document.documentElement.classList.add('cfg-titles-on');}catch(e){}
// Тема редактора кода — тем же приёмом и по той же причине: класс до отрисовки,
// иначе блоки кода мигнут тёмным, прежде чем применится светлая тема. Дефолт
// страницы (тёмная) действует, пока пользователь не выбрал тему явно.
window.cfgCodeThemeDefault='dark';
try{var _ct=localStorage.getItem('cfgCodeTheme');if(_ct?_ct==='light':window.cfgCodeThemeDefault==='light')document.documentElement.classList.add('cfg-code-light');}catch(e){}
</script>
<!-- ECharts: тот же движок, что рисует графики в пользовательском режиме —
     предпросмотр виджета выглядит как у пользователя. Грузим ДО AMD-загрузчика
     Monaco: иначе UMD-бандл ECharts увидит define.amd и зарегистрируется как
     AMD-модуль вместо window.echarts (тогда график «недоступен»). -->
<script src="/vendor/echarts/echarts.min.js" onerror="window._echartsLoadErr=1"></script>
<script src="/vendor/monaco/vs/loader.js" onerror="window._monacoLoadErr='loader.js failed'"></script>
<script src="/static/code-theme.js"></script>
<script>{{.InlineJSYaml}}</script>
<title>{{t $.Lang "Конфигуратор"}} — {{if .AppName}}{{.AppName}}{{else}}{{.Base.Name}}{{end}}</title>
<link rel="stylesheet" href="/static/configurator.css">
</head>
<body>
<div class="topbar">
  <div class="cfg-menu-wrap">
    <button class="cfg-menu-btn" onclick="cfgMenuToggle()">{{t $.Lang "Меню"}} &#9662;</button>
    <div class="cfg-menu-dropdown" id="cfg-menu">
      <a href="#" onclick="cfgAdmin('about');return false">{{t $.Lang "О программе"}}</a>
      <a href="#" onclick="cfgAdmin('users');return false">{{t $.Lang "Пользователи"}}</a>
      <a href="#" onclick="cfgAdmin('roles');return false">{{t $.Lang "Роли и права"}}</a>
      <a href="#" onclick="cfgAdmin('sessions');return false">{{t $.Lang "Активные пользователи"}}</a>
      <a href="#" onclick="cfgAdmin('audit');return false">{{t $.Lang "Журнал регистрации"}}</a>
      <a href="#" onclick="cfgAdmin('settings');return false">{{t $.Lang "Параметры базы"}}</a>
      <a href="#" onclick="cfgAdmin('config-history');return false">{{t $.Lang "История конфигурации"}}</a>
      <a href="#" onclick="cfgAdmin('rollup');return false">{{t $.Lang "Свёртка базы"}}</a>
      <a href="#" onclick="cfgAdmin('ai');return false">{{t $.Lang "ИИ-помощник"}}</a>
      <a href="#" onclick="cfgAdmin('ai-history');return false">{{t $.Lang "История ИИ"}}</a>
      <a href="#" onclick="toggleSyntaxRef();cfgMenuToggle();return false">{{t $.Lang "Справка по встроенному языку"}} (F1)</a>
      <a href="/report-problem?base={{.Base.ID}}" target="_top">{{t $.Lang "Сообщить об ошибке"}}</a>
      <a href="/bases/{{.Base.ID}}/configurator/logout" style="color:#c00;border-top:1px solid #e5e7eb;margin-top:2px">🚪 {{t $.Lang "Выйти"}}</a>
    </div>
  </div>
  <a href="/?sel={{.Base.ID}}">← {{t $.Lang "Лаунчер"}}</a>
  <h1>{{t $.Lang "Конфигуратор"}} — {{if .AppName}}{{.AppName}}{{else}}{{.Base.Name}}{{end}}</h1>
  <span style="font-size:11px;color:#7aa8d8">{{.DSNMasked}} · :{{.Base.Port}} · {{t $.Lang "платформа"}} {{.PlatformVer}}{{if .PlatformDate}} · {{.PlatformDate}}{{end}}</span>
  <button id="cfg-save-topbar" onclick="cfgSaveActive()" title="{{t $.Lang "Сохранить (Ctrl+S)"}}" class="cfg-save-topbar">&#128190; {{t $.Lang "Сохранить"}}</button>
  <button onclick="launchEnterprise()" title="{{t $.Lang "Запустить предприятие"}}" class="run-enterprise-btn"><svg viewBox="0 0 24 24" fill="#333"><polygon points="6,3 20,12 6,21"/></svg></button>
  {{if and (eq .Tab "tree") $.AvailableLangs}}<button id="cfg-titles-toggle" class="dbg-topbar-btn" onclick="cfgTitlesToggle()" title="{{t $.Lang "Показать/скрыть поля переводов у всех объектов"}}">&#127760; {{t $.Lang "Переводы"}}</button>{{end}}
  {{/* Тумблер темы кода — только на «Дереве»: редакторы модулей есть только там.
       Подпись показывает тему, в которую переключит клик (видимость — CSS). */}}
  {{if eq .Tab "tree"}}<button id="cfg-code-theme-toggle" class="dbg-topbar-btn" onclick="cfgCodeThemeToggle()" title="{{t $.Lang "Светлая или тёмная тема редактора кода"}}"><span class="cth-to-light">&#9728;&#65039; {{t $.Lang "Светлая тема"}}</span><span class="cth-to-dark">&#127769; {{t $.Lang "Тёмная тема"}}</span></button>{{end}}
  <button id="dbg-toggle" class="dbg-topbar-btn" onclick="dbgToggle()">&#128027; {{t $.Lang "Отладка: ВЫКЛ"}}</button>
  <button onclick="toggleSyntaxRef()" title="{{t $.Lang "Синтакс-помощник"}} (F1)" class="dbg-topbar-btn">&#10067; {{t $.Lang "Справка"}}</button>
  <span id="monaco-status" style="font-size:9px;color:#94a3b8">Monaco:...</span>
</div>
<div class="tabs">
  <a class="tab {{if eq .Tab "tree"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=tree">🌳 {{t $.Lang "Дерево"}}</a>
  <a class="tab {{if eq .Tab "convert"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=convert">🔄 {{t $.Lang "Импорт конфигурации"}}</a>
  <a class="tab {{if eq .Tab "files"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=files">📁 {{t $.Lang "Файлы"}}</a>
  <a class="tab {{if eq .Tab "backup"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=backup">💾 {{t $.Lang "Бэкапы"}}</a>
</div>
<div class="cfg-body">
{{if .Error}}<div class="err-box">{{.Error}}</div>{{end}}
{{if .SavedMessage}}<div class="success-box">{{.SavedMessage}}</div>
{{else if and .FieldsSaved (ne .FieldsSavedEntity "panel-backup")}}<div class="success-box">{{t $.Lang "✓ Типы полей для"}} «{{.FieldsSavedEntity}}» {{t $.Lang "сохранены. Перезапустите базу, чтобы изменения вступили в силу."}}</div>{{end}}
<div id="dbg-wrapper" style="display:flex;flex:1;overflow:hidden">
{{end}}`

const cfgFoot = `{{define "cfg-foot"}}
<!-- debug panel (right sidebar) -->
<div class="dbg-panel" id="dbg-panel" style="display:none">
  <div class="dbg-status" id="dbg-status"><span class="dot disabled"></span> {{t $.Lang "Отладка: ВЫКЛ"}}</div>
  <div class="dbg-controls" id="dbg-controls" style="display:none">
    <button onclick="dbgContinue()">&#9654; {{t $.Lang "Продолжить"}}</button>
    <button onclick="dbgStep('over')" style="font-weight:600">&#10145; {{t $.Lang "Шаг (F10)"}}</button>
    <button onclick="dbgStep('into')">&#11015; {{t $.Lang "Шаг с заходом (F11)"}}</button>
    <button onclick="dbgStep('out')">&#11014; {{t $.Lang "Шаг с выходом"}}</button>
    <button onclick="dbgStop()">&#9209; {{t $.Lang "Стоп"}}</button>
  </div>
  <div class="dbg-tabs">
    <button class="dbg-tab active" onclick="dbgTab('vars')">{{t $.Lang "Переменные"}}</button>
    <button class="dbg-tab" onclick="dbgTab('watch')">{{t $.Lang "Табло"}}</button>
    <button class="dbg-tab" onclick="dbgTab('bp')">{{t $.Lang "Точки ост."}}</button>
    <button class="dbg-tab" onclick="dbgTab('stack')">{{t $.Lang "Стек"}}</button>
    <button class="dbg-tab" onclick="dbgTab('console')">{{t $.Lang "Консоль"}}</button>
  </div>
  <div id="dbg-vars" class="dbg-content"><div class="dbg-empty">{{t $.Lang "Включите отладку для просмотра переменных"}}</div></div>
  <div id="dbg-watch" class="dbg-content" style="display:none;flex-direction:column;padding:0">
    <div style="padding:6px 10px;border-bottom:1px solid #eef0f5;display:flex;gap:4px;flex-shrink:0">
      <input id="dbg-watch-add" type="text" placeholder="Выражение..." onkeydown="if(event.key==='Enter')dbgWatchAdd()" style="flex:1;min-width:0;padding:3px 8px;border:1px solid #d0d7e3;border-radius:4px;font-size:12px;font-family:'Cascadia Code','Fira Code',monospace">
      <button onclick="dbgWatchAdd()" style="background:#1a4a80;color:#fff;border:none;padding:3px 10px;border-radius:4px;font-size:11px;cursor:pointer">+</button>
    </div>
    <div id="dbg-watch-debug" style="padding:2px 10px;font-size:9px;color:#f97316;background:#fffbeb;flex-shrink:0"></div>
    <div id="dbg-watch-list" style="flex:1;overflow-y:auto;padding:4px 10px;min-height:40px"></div>
  </div>
  <div id="dbg-bp" class="dbg-content" style="display:none">
    <div style="padding:6px 0;border-bottom:1px solid #eef;display:flex;gap:4px;flex-shrink:0">
      <input id="dbg-bp-file" type="text" placeholder="{{t $.Lang "Файл"}} (post-...)" style="flex:2;min-width:0;padding:3px 6px;border:1px solid #d0d7e3;border-radius:4px;font-size:11px">
      <input id="dbg-bp-line" type="number" placeholder="{{t $.Lang "Стр"}}" style="width:50px;padding:3px 6px;border:1px solid #d0d7e3;border-radius:4px;font-size:11px">
      <button onclick="dbgManualBP()" style="background:#1a4a80;color:#fff;border:none;padding:3px 8px;border-radius:4px;font-size:10px;cursor:pointer">+</button>
    </div>
    <div id="dbg-bp-list"><div class="dbg-empty">{{t $.Lang "Нет точек останова"}}</div></div>
  </div>
  <div id="dbg-stack" class="dbg-content" style="display:none"><div class="dbg-empty">{{t $.Lang "Стек вызовов пуст"}}</div></div>
  <div id="dbg-console" class="dbg-content" style="display:none;flex-direction:column;padding:0">
    <div id="dbg-diag" style="background:#1e1e2e;color:#a5b4fc;padding:6px 10px;font-size:10px;font-family:'Cascadia Code','Fira Code',monospace;border-bottom:1px solid #333;max-height:120px;overflow-y:auto"></div>
    <div id="dbg-console-out" class="dbg-console-out" style="background:#1e1e2e;color:#cdd6f4"></div>
    <div class="dbg-console-input">
      <input id="dbg-expr" type="text" placeholder="{{t $.Lang "Выражение DSL"}}..." onkeydown="if(event.key==='Enter')dbgEval()">
      <button onclick="dbgEval()">{{t $.Lang "Выполнить"}}</button>
    </div>
  </div>
</div>
</div>{{/* dbg-wrapper */}}
</div>{{/* cfg-body */}}

<!-- Debug value inspector modal. Закрытие — только крестиком: клик по фону
     закрывал окно, когда значение выделяли мышью и отпускали кнопку за его
     границей (см. комментарий в internal/ui/static/ui.js). -->
<div class="cfg-modal-overlay" id="dbg-val-modal">
  <div class="cfg-modal-box" style="max-width:780px">
    <div class="cfg-modal-hd">
      <h3 id="dbg-val-modal-title">{{t $.Lang "Значение"}}</h3>
      <div style="display:flex;gap:8px;align-items:center">
        <button onclick="dbgValModalCopy()" style="background:#1a4a80;color:#fff;border:none;padding:4px 12px;border-radius:4px;font-size:12px;cursor:pointer">{{t $.Lang "Копировать"}}</button>
        <button class="cfg-modal-close" onclick="dbgValModalClose()">&times;</button>
      </div>
    </div>
    <div class="cfg-modal-body" style="padding:0">
      <textarea id="dbg-val-modal-text" readonly spellcheck="false" style="display:block;width:100%;height:62vh;border:none;resize:none;padding:12px 16px;font-family:'Cascadia Code','Fira Code',monospace;font-size:12px;line-height:1.5;color:#1e293b;box-sizing:border-box;outline:none;white-space:pre-wrap;word-break:break-word"></textarea>
    </div>
  </div>
</div>

<!-- Admin modal. Закрытие — только крестиком: внутри iframe редактируют данные,
     а клик по фону прилетал от обычного выделения текста мышью. -->
<div class="cfg-modal-overlay" id="cfg-modal">
  <div class="cfg-modal-box">
    <div class="cfg-modal-hd">
      <h3 id="cfg-modal-title">—</h3>
      <button class="cfg-modal-close" onclick="cfgModalClose()">&times;</button>
    </div>
    <div class="cfg-modal-body">
      <div class="cfg-modal-loading" id="cfg-modal-loading">{{t $.Lang "Загрузка..."}}</div>
      <iframe id="cfg-modal-iframe" onload="document.getElementById('cfg-modal-loading').style.display='none'"></iframe>
    </div>
  </div>
</div>

<!-- Query builder modal -->
<div class="qb-overlay" id="qb-overlay">
<div class="qb-modal">
  <div class="qb-modal-hd">
    <h2>{{t $.Lang "Конструктор запроса"}}</h2>
    <div style="display:flex;gap:6px;align-items:center">
      <select id="qb-mode" style="font-size:12px;border:1px solid #c8d0de;border-radius:4px;padding:4px 6px;background:#fff">
        <option value="dsl">{{t $.Lang "Полный код"}}</option>
        <option value="query">{{t $.Lang "Только запрос"}}</option>
      </select>
      <button id="qb-insert" style="background:#1a4a80;color:#fff;border:none;padding:6px 16px;border-radius:4px;cursor:pointer;font-size:13px;font-weight:600">{{t $.Lang "Вставить"}}</button>
      <button id="qb-close" style="background:#e8ecf2;color:#333;border:1px solid #c8d0de;padding:6px 14px;border-radius:4px;cursor:pointer;font-size:13px">{{t $.Lang "Закрыть"}}</button>
    </div>
  </div>
  <div class="qb-modal-bd">
    <div style="display:flex;gap:6px;align-items:center;margin-bottom:8px">
      <input id="qb-ai-desc" type="text" placeholder="Опишите запрос словами, напр.: средний чек по менеджерам за месяц" style="flex:1;font-size:12px;border:1px solid #c8d0de;border-radius:4px;padding:5px 8px">
      <button id="qb-ai-gen" type="button" style="background:#7c3aed;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer;font-size:12px;white-space:nowrap">🤖 Сгенерировать</button>
    </div>
    <div class="qb-grid">
      <!-- LEFT -->
      <div>
        <div class="qb-card">
          <h3>{{t $.Lang "Источник данных"}}</h3>
          <select id="mqb-src" onchange="mqbSetSrc(this.value)" style="width:100%;margin-bottom:6px"><option value="">{{t $.Lang "— выбрать —"}}</option></select>
          <div style="display:flex;align-items:center;gap:6px;margin-bottom:4px">
            <span style="font-size:12px;color:#64748b;flex-shrink:0;width:68px">{{t $.Lang "Псевдоним"}}:</span>
            <input id="mqb-alias" type="text" placeholder="{{t $.Lang "напр. Т"}}" oninput="mqbRebuild()" style="width:100px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 5px">
          </div>
          <div id="mqb-vtp" style="display:none;margin-top:4px">
            <label style="font-size:12px;color:#64748b">{{t $.Lang "Параметры ВТ"}}</label>
            <input id="mqb-vtpv" type="text" style="width:100%;margin-top:2px" placeholder="&amp;НаДату" oninput="mqbGen()">
          </div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Соединения"}}</h3>
            <button onclick="mqbAddJoin()" style="background:#dbeafe;color:#1d4ed8;border:none;padding:2px 8px;font-size:12px;border-radius:4px;cursor:pointer">+ JOIN</button>
          </div>
          <div id="mqb-joins"><p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">{{t $.Lang "Нет"}}</p></div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Поля"}}</h3>
            <div style="display:flex;gap:3px">
              <button onclick="mqbAll(true)" style="background:#e2e8f0;color:#475569;border:none;padding:2px 6px;font-size:11px;border-radius:3px;cursor:pointer">{{t $.Lang "Все"}}</button>
              <button onclick="mqbAll(false)" style="background:#e2e8f0;color:#475569;border:none;padding:2px 6px;font-size:11px;border-radius:3px;cursor:pointer">{{t $.Lang "Сброс"}}</button>
            </div>
          </div>
          <div class="qb-fl" id="mqb-fields"></div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Условия (ГДЕ)"}}</h3>
            <button onclick="mqbAddCond()" style="background:#dbeafe;color:#1d4ed8;border:none;padding:2px 8px;font-size:12px;border-radius:4px;cursor:pointer">+</button>
          </div>
          <div id="mqb-conds"></div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Сортировка"}}</h3>
            <button onclick="mqbAddOrd()" style="background:#dbeafe;color:#1d4ed8;border:none;padding:2px 8px;font-size:12px;border-radius:4px;cursor:pointer">+</button>
          </div>
          <div id="mqb-ords"></div>
        </div>
      </div>
      <!-- RIGHT -->
      <div>
        <div class="qb-card">
          <h3>{{t $.Lang "DSL-фрагмент"}}</h3>
          <textarea id="mqb-dsl" rows="18" readonly style="width:100%;font-family:monospace;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:8px;background:#fff;resize:vertical"></textarea>
        </div>
        <div class="qb-card">
          <h3>{{t $.Lang "Текст запроса"}}</h3>
          <textarea id="mqb-qry" rows="10" readonly style="width:100%;font-family:monospace;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:8px;background:#fff;resize:vertical"></textarea>
        </div>
      </div>
    </div>
  </div>
</div>
</div>
{{/* Предпросмотр макета печатной формы (план 64, этап 5b, 6.6) */}}
<div class="qb-overlay" id="ld-preview-overlay">
  <div class="qb-modal" style="max-width:1100px;width:96%;height:90vh;display:flex;flex-direction:column">
    <div class="qb-modal-hd">
      <h2>{{t $.Lang "Предпросмотр печатной формы"}}</h2>
      <button onclick="ldClosePreview()" style="background:#e8ecf2;color:#333;border:1px solid #c8d0de;padding:6px 14px;border-radius:4px;cursor:pointer;font-size:13px">{{t $.Lang "Закрыть"}}</button>
    </div>
    <div style="flex:1;min-height:0;background:#fff;border-radius:0 0 8px 8px;overflow:hidden">
      {{/* sandbox: allow-same-origin (blob URL, конфигуратор same-origin) +
           allow-scripts (window.print в тулбаре HTML-предпросмотра) +
           allow-modals (window.print/alert).
           Текст ячеек экранирован escapeHTML() — sandbox здесь defense-in-depth. */}}
      <iframe id="ld-preview-frame" style="width:100%;height:100%;border:none"
              sandbox="allow-same-origin allow-scripts allow-modals"
              title="{{t $.Lang "Предпросмотр печатной формы"}}"></iframe>
    </div>
  </div>
</div>
<!-- bootstrap: данные и переводы для главного скрипта (план 55, фаза 2b-1).
     Должен идти ПЕРЕД главным <script>, чтобы window.__cfg существовал. -->
<script>
window.__cfg = {{if .Bootstrap}}{{.Bootstrap}}{{else}}{}{{end}};
window.__cfgI18n = {{if .I18n}}{{.I18n}}{{else}}null{{end}} || {};
window.__ldMeta = {{if .LayoutMeta}}{{.LayoutMeta}}{{else}}{}{{end}};
window.__qbSchema = {{if .QBSchema}}{{.QBSchema}}{{else}}null{{end}};
function T(k){ return (window.__cfgI18n[k] || k); }
</script>
<script src="/static/configurator.js"></script>
<!-- ИИ-инструменты конфигуратора: помощник (🤖, план 51) и генератор каркаса (🏗️, план 57) -->
<button id="cfgai-btn" title="ИИ-помощник" style="display:none;position:fixed;right:18px;bottom:18px;z-index:9000;width:48px;height:48px;border-radius:50%;background:#7c3aed;color:#fff;border:none;cursor:pointer;font-size:22px;box-shadow:0 4px 14px rgba(124,58,237,.4)">🤖</button>
<button id="cfggen-btn" title="Генерация каркаса" style="display:none;position:fixed;right:18px;bottom:76px;z-index:9000;width:48px;height:48px;border-radius:50%;background:#ea580c;color:#fff;border:none;cursor:pointer;font-size:22px;box-shadow:0 4px 14px rgba(234,88,12,.4)">🏗️</button>
<div id="cfgai-panel" style="display:none;position:fixed;right:18px;bottom:18px;z-index:9001;width:420px;max-width:calc(100vw - 24px);height:560px;max-height:calc(100vh - 40px);background:#fff;border:1px solid #cbd5e1;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,.22);flex-direction:column;overflow:hidden;font-family:system-ui,sans-serif">
  <div style="background:#7c3aed;color:#fff;padding:10px 14px;display:flex;align-items:center;gap:8px;font-weight:600;font-size:14px">🤖 ИИ-помощник разработчика<span style="flex:1"></span><button type="button" id="cfgai-close" style="background:none;border:none;color:#fff;cursor:pointer;font-size:18px">×</button></div>
  <textarea id="cfgai-prompt" placeholder="Опишите, что сгенерировать или объяснить. Напр.: обработчик ПриОткрытии, который ставит текущую дату в поле Дата" style="margin:10px;height:80px;resize:vertical;border:1px solid #cbd5e1;border-radius:8px;padding:8px;font-size:13px;font-family:inherit"></textarea>
  <label style="margin:0 10px;font-size:11px;color:#666"><input type="checkbox" id="cfgai-usecode"> добавить текущий код из активного редактора как контекст</label>
  <div style="margin:8px 10px;display:flex;gap:8px;align-items:center"><button id="cfgai-send" type="button" style="background:#7c3aed;color:#fff;border:none;border-radius:8px;padding:6px 16px;cursor:pointer;font-size:13px">Сгенерировать</button><button id="cfgai-copy" type="button" style="background:#e2e8f0;border:none;border-radius:8px;padding:6px 14px;cursor:pointer;font-size:13px;display:none">Копировать</button><span id="cfgai-msg" style="font-size:11px"></span></div>
  <pre id="cfgai-out" style="flex:1;overflow:auto;margin:0 10px 10px;background:#0f172a;color:#e2e8f0;border-radius:8px;padding:10px;font-size:12px;white-space:pre-wrap;word-break:break-word"></pre>
</div>
<div id="cfggen-panel" style="display:none;position:fixed;right:18px;bottom:18px;z-index:9001;width:420px;max-width:calc(100vw - 24px);height:560px;max-height:calc(100vh - 40px);background:#fff;border:1px solid #cbd5e1;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,.22);flex-direction:column;overflow:hidden;font-family:system-ui,sans-serif">
  <div style="background:#ea580c;color:#fff;padding:10px 14px;display:flex;align-items:center;gap:8px;font-weight:600;font-size:14px">🏗️ Генерация каркаса<span style="flex:1"></span><button type="button" id="cfggen-close" style="background:none;border:none;color:#fff;cursor:pointer;font-size:18px">×</button></div>
  <textarea id="cfggen-prompt" placeholder="Опишите, что сгенерировать. Напр.: справочник Клиенты и документ Заявка с табличной частью Товары" style="margin:10px;height:80px;resize:vertical;border:1px solid #cbd5e1;border-radius:8px;padding:8px;font-size:13px;font-family:inherit"></textarea>
  <div style="margin:0 10px 8px;display:flex;gap:8px;align-items:center;flex-wrap:wrap"><button id="cfggen-send" type="button" style="background:#ea580c;color:#fff;border:none;border-radius:8px;padding:6px 16px;cursor:pointer;font-size:13px">Сгенерировать</button><button id="cfggen-apply" type="button" style="background:#16a34a;color:#fff;border:none;border-radius:8px;padding:6px 16px;cursor:pointer;font-size:13px;display:none">Применить</button><span id="cfggen-msg" style="font-size:11px"></span></div>
  <div id="cfggen-out" style="flex:1;overflow:auto;margin:0 10px 10px;background:#fff;border:1px solid #e2e8f0;border-radius:8px;padding:10px;font-size:12px"></div>
</div>
<!-- План 72: подсказка имён иконок (datalist) и живое превью рядом с полем «Иконка». -->
<datalist id="lucide-icons">{{range lucideNames}}<option value="{{.}}"></option>{{end}}</datalist>
<style>
.icon-field{display:flex;align-items:center;gap:8px}
.icon-field input{flex:1}
.icon-preview{display:inline-flex;align-items:center;justify-content:center;width:34px;height:34px;flex-shrink:0;border:1px solid #e2e8f0;border-radius:6px;color:#334155;font-size:20px;background:#fff}
.icon-preview:empty{opacity:.35}
.icon-help{font-weight:400;font-size:11px;color:#7c3aed;text-decoration:none;margin-left:6px}
.icon-help:hover{text-decoration:underline}
</style>
<script>
(function(){
  // План 73: превью собирает <use> на общий спрайт. Разметку иконок сюда больше
  // не вшиваем — множество валидных имён берём из того же datalist, что рисует
  // подсказку, а синонимы приходят отдельной маленькой картой.
  var ALIASES = {{lucideAliasesJSON}};
  var SPRITE = {{lucideSpriteURL}};
  var known = null;
  function knownNames(){
    if(known) return known;
    known = Object.create(null);
    var list = document.getElementById('lucide-icons');
    var opts = list ? list.getElementsByTagName('option') : [];
    for(var i=0;i<opts.length;i++) known[opts[i].value] = true;
    return known;
  }
  function normalizeIconName(v){
    return String(v || '').trim().toLowerCase().replace(/[ _-]+/g, '-').replace(/^-+|-+$/g, '');
  }
  function iconMarkup(name){
    var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('class', 'lucide ob-icon');
    svg.setAttribute('width', '1em');
    svg.setAttribute('height', '1em');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('fill', 'none');
    svg.setAttribute('stroke', 'currentColor');
    svg.setAttribute('stroke-width', '2');
    svg.setAttribute('stroke-linecap', 'round');
    svg.setAttribute('stroke-linejoin', 'round');
    svg.setAttribute('aria-hidden', 'true');
    var use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
    use.setAttribute('href', SPRITE + '#' + name);
    svg.appendChild(use);
    return svg;
  }
  function setPreview(inp){
    var box = inp.parentNode && inp.parentNode.querySelector('.icon-preview');
    if(!box) return;
    var key = normalizeIconName(inp.value);
    if(ALIASES[key]) key = ALIASES[key];
    box.textContent = '';
    if(!key) return;
    // Неизвестное имя показываем квадратом — как и сервер при рендере навигации,
    // иначе битая ссылка на символ молча рисовала бы пустоту.
    if(!knownNames()[key]) key = 'square';
    box.appendChild(iconMarkup(key));
  }
  function wireAll(){
    var list = document.querySelectorAll('input[data-icon-preview]');
    for(var i=0;i<list.length;i++){
      var inp = list[i];
      if(inp.__iconWired) continue;
      inp.__iconWired = true;
      inp.addEventListener('input', (function(el){ return function(){ setPreview(el); }; })(inp));
    }
  }
  if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', wireAll);
  else wireAll();
})();
</script>
</body></html>
{{end}}`

// ── Main dispatcher ───────────────────────────────────────────────────────────

const cfgMain = `{{define "cfg-main"}}
{{template "cfg-head" .}}
{{if eq .Tab "tree"}}{{template "tab-tree" .}}{{end}}
{{if eq .Tab "convert"}}{{template "tab-convert" .}}{{end}}
{{if eq .Tab "files"}}{{template "tab-files" .}}{{end}}
{{if eq .Tab "backup"}}{{template "tab-backup" .}}{{end}}
{{template "cfg-foot" .}}
<!-- Закрытие — только крестиком в шапке (её рисует cfgAdmin сразу, ещё до ответа
     сервера): клик по фону закрывал панель прямо во время заполнения форм. -->
<div id="admin-overlay" style="display:none;position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;align-items:center;justify-content:center;padding:20px"></div>
{{template "syntax-ref" .}}
{{end}}`

// ── Syntax reference window (Синтакс-помощник) ─────────────────────────────────

const cfgSyntaxRef = `{{define "syntax-ref"}}
<button id="syntax-ref-toggle" type="button" onclick="toggleSyntaxRef()" title="{{t .Lang "Синтакс-помощник"}} (F1)"
  style="position:fixed;right:0;top:120px;z-index:40;background:#1a4a80;color:#fff;border:none;border-radius:4px 0 0 4px;padding:8px 6px;cursor:pointer;writing-mode:vertical-rl">{{t .Lang "Синтакс-помощник"}}</button>
<div id="syntax-ref-panel" style="display:none;position:fixed;right:0;top:0;bottom:0;width:520px;z-index:41;background:#fff;border-left:1px solid #cbd5e1;box-shadow:-4px 0 16px rgba(0,0,0,.12);font-size:13px;flex-direction:column">
  <div style="display:flex;align-items:center;gap:8px;padding:8px 10px;border-bottom:1px solid #e2e8f0;background:#f8fafc">
    <strong style="flex:0 0 auto">{{t .Lang "Синтакс-помощник"}}</strong>
    <input id="syntax-ref-search" oninput="renderSyntaxRefTree()" placeholder="{{t .Lang "поиск"}}…" style="flex:1;padding:4px 6px;border:1px solid #cbd5e1;border-radius:4px">
    <button type="button" onclick="toggleSyntaxRef(false)" style="border:none;background:none;font-size:16px;cursor:pointer">✕</button>
  </div>
  <div style="flex:1;display:flex;min-height:0">
    <div id="syntax-ref-tree" style="width:230px;overflow:auto;border-right:1px solid #e2e8f0;padding:6px"></div>
    <div id="syntax-ref-detail" style="flex:1;overflow:auto;padding:10px"></div>
  </div>
</div>
<script>
function toggleSyntaxRef(show){
  var p=document.getElementById('syntax-ref-panel');
  var open=(typeof show==='boolean')?show:(p.style.display==='none');
  p.style.display=open?'flex':'none';
  if(open){ if(typeof loadLangref==='function'){ loadLangref().then(renderSyntaxRefTree); } else { renderSyntaxRefTree(); } }
}
function _lrFiltered(){
  var q=(document.getElementById('syntax-ref-search').value||'').toLowerCase();
  var data=window._langref||[];
  if(!q) return data;
  return data.filter(function(d){
    return (d.display&&d.display.toLowerCase().indexOf(q)>=0)
      || (d.name&&d.name.toLowerCase().indexOf(q)>=0)
      || (d.aliases&&d.aliases.some(function(a){return a.toLowerCase().indexOf(q)>=0;}))
      || (d.doc&&d.doc.toLowerCase().indexOf(q)>=0);
  });
}
function renderSyntaxRefTree(){
  var data=_lrFiltered(), tree=document.getElementById('syntax-ref-tree');
  var groups={'{{t .Lang "Глобальные функции"}}':{}, '{{t .Lang "Методы объектов"}}':{}, '{{t .Lang "Конструкции языка"}}':{'_':[]}, '{{t .Lang "Язык запросов"}}':{'_':[]}};
  var GF='{{t .Lang "Глобальные функции"}}', MO='{{t .Lang "Методы объектов"}}', KL='{{t .Lang "Конструкции языка"}}', QL='{{t .Lang "Язык запросов"}}';
  data.forEach(function(d){
    if(d.kind==='func'){ var g=d.group||'—'; (groups[GF][g]=groups[GF][g]||[]).push(d); }
    else if(d.kind==='method'){ var o=d.object||'—'; (groups[MO][o]=groups[MO][o]||[]).push(d); }
    else if(d.kind==='keyword'){ groups[KL]['_'].push(d); }
    else if(d.kind==='query'){ groups[QL]['_'].push(d); }
  });
  var html='';
  [GF,MO,KL,QL].forEach(function(top){
    var sub=groups[top], subKeys=Object.keys(sub).filter(function(k){return sub[k].length;});
    if(!subKeys.length) return;
    html+='<div style="font-weight:600;margin:6px 0 2px">'+top+'</div>';
    subKeys.sort().forEach(function(sk){
      if(sk!=='_'){ html+='<div style="color:#64748b;margin:3px 0 1px;padding-left:6px">'+sk+'</div>'; }
      sub[sk].slice().sort(function(a,b){return a.display.localeCompare(b.display);}).forEach(function(d){
        var idx=(window._langref||[]).indexOf(d);
        html+='<div style="padding:2px 6px 2px 16px;cursor:pointer;border-radius:3px" onmouseover="this.style.background=\'#eef2ff\'" onmouseout="this.style.background=\'\'" onclick="showSyntaxRefDetail('+idx+')">'+d.display+'</div>';
      });
    });
  });
  tree.innerHTML=html||('<div style="color:#94a3b8">'+'{{t .Lang "ничего не найдено"}}'+'</div>');
}
function showSyntaxRefDetail(idx){
  var d=(window._langref||[])[idx]; if(!d) return;
  var esc=function(s){ return (s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); };
  var h='<div style="font-family:monospace;font-size:14px;font-weight:600;margin-bottom:6px">'+esc(d.signature||d.display)+'</div>';
  if(d.returns) h+='<div style="color:#475569;margin-bottom:6px">'+'{{t .Lang "Возвращает"}}'+': '+esc(d.returns)+'</div>';
  h+='<div style="margin-bottom:10px">'+esc(d.doc)+'</div>';
  if(d.params&&d.params.length){
    h+='<div style="font-weight:600;margin-bottom:3px">'+'{{t .Lang "Параметры"}}'+':</div><ul style="margin:0 0 10px 16px;padding:0">';
    d.params.forEach(function(p){ h+='<li><code>'+esc(p.name)+'</code> : '+esc(p.type)+(p.doc?' — '+esc(p.doc):'')+(p.optional?' <em>('+'{{t .Lang "необяз."}}'+')</em>':'')+'</li>'; });
    h+='</ul>';
  }
  if(d.example) h+='<pre style="background:#f1f5f9;padding:8px;border-radius:4px;white-space:pre-wrap">'+esc(d.example)+'</pre>';
  h+='<button type="button" onclick="insertLangrefSignature('+idx+')" style="margin-top:8px;background:#1a4a80;color:#fff;border:none;border-radius:4px;padding:6px 12px;cursor:pointer">'+'{{t .Lang "Вставить в редактор"}}'+'</button>';
  document.getElementById('syntax-ref-detail').innerHTML=h;
}
function insertLangrefSignature(idx){
  var d=(window._langref||[])[idx]; if(!d) return;
  var id=window._lastFocusedEditorId, ed=id&&window.monacoEditors&&monacoEditors[id];
  if(!ed){ alert('{{t .Lang "Откройте редактор модуля и поставьте курсор"}}'); return; }
  ed.executeEdits('syntax-ref',[{range: ed.getSelection(), text: (d.signature||d.display)}]);
  ed.focus();
}
document.addEventListener('keydown',function(e){
  if(e.key==='F1'){ e.preventDefault(); toggleSyntaxRef(); }
  else if(e.key==='Escape'){ var p=document.getElementById('syntax-ref-panel'); if(p&&p.style.display!=='none') toggleSyntaxRef(false); }
});
</script>
{{end}}`
