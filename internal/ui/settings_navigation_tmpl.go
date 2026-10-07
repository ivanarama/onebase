package ui

const tplNavigationSettings = `
{{define "page-navigation-settings"}}
{{template "head" .}}{{template "nav" .}}
<main id="navigation-settings" style="padding:20px;max-width:1500px;margin:auto">
<h1>{{t .Lang .NavigationTitle}} → {{t .Lang "Навигация"}}</h1>
<p>{{t .Lang .NavigationDescription}}</p>
<form method="GET" action="{{.NavigationPath}}">
<label>{{t .Lang "Раздел"}} <select name="subsystem">
<option value="">{{t .Lang "Главная"}}</option>
{{range .Subsystems}}<option value="{{.Name}}" {{if eq .Name $.NavigationSubsystem}}selected{{end}}>{{.DisplayName $.Lang}}</option>{{end}}
</select></label><button type="submit">{{t .Lang "Открыть"}}</button>
</form>
{{if .NavigationSaved}}<p role="status">{{t .Lang "Меню сохранено"}}</p>{{end}}
{{if .NavigationMessage}}<p role="alert" class="error">{{.NavigationMessage}}</p><a href="{{.NavigationPath}}?subsystem={{.NavigationSubsystem}}">{{t .Lang "Загрузить актуальную версию"}}</a>{{end}}
{{if .NavigationDiagnostics}}<div role="alert" class="error">
<p>{{t .Lang "Есть предупреждения настройки меню"}}: {{len .NavigationDiagnostics}}</p>
{{range .NavigationDiagnostics}}{{if eq .Code "invalid-layer"}}<p>{{if $.NavigationPersonal}}{{if eq .Node "user"}}{{t $.Lang "Повреждённая личная настройка пропущена. Показано общее меню; сохранение или сброс исправит этот слой."}}{{else}}{{t $.Lang "Повреждённая общая настройка пропущена. Обратитесь к администратору базы."}}{{end}}{{else}}{{t $.Lang "Повреждённая общая настройка пропущена. Показано меню конфигурации; сохранение или сброс исправит этот слой."}}{{end}}</p>{{else}}<p>{{t $.Lang "Конфигурация меню изменилась. Сохраните актуальную раскладку, чтобы убрать устаревшие правила."}}</p>{{end}}{{end}}
</div>{{end}}
<div style="display:flex;gap:12px;flex-wrap:wrap;margin:16px 0">
<form id="navigation-save" method="POST" action="{{.NavigationPath}}/save">
<input type="hidden" name="subsystem" value="{{.NavigationSubsystem}}">
<input type="hidden" name="revision" value="{{.NavigationRevision}}">
<input id="navigation-desired" type="hidden" name="desired">
{{if .NavigationPersonal}}<input type="hidden" name="base_revision" value="{{.NavigationBaseRevision}}"><input id="navigation-renamed" type="hidden" name="renamed" value="[]">{{end}}
<button type="submit" class="btn btn-primary">{{t .Lang "Сохранить"}}</button>
</form>
<button id="navigation-add-section" type="button">{{t .Lang "Добавить раздел"}}</button>
<button id="navigation-add-group" type="button">{{t .Lang "Добавить папку"}}</button>
<form id="navigation-reset" method="POST" action="{{.NavigationPath}}/reset" data-ob-confirm="{{t .Lang .NavigationResetConfirm}}">
<input type="hidden" name="subsystem" value="{{.NavigationSubsystem}}">
<input type="hidden" name="revision" value="{{.NavigationRevision}}">
<button type="submit">{{t .Lang .NavigationResetLabel}}</button>
</form>
</div>
<div id="navigation-status" role="alert"></div><div id="navigation-live" aria-live="polite" style="min-height:1.5em"></div>
<div class="navigation-editor-layout">
<section class="card"><h2>{{t .Lang "Структура меню"}}</h2><div id="navigation-tree"></div>
<div id="navigation-properties"></div>
<p>{{t .Lang "Перемещайте узлы кнопками или перетаскиванием. Alt + стрелки: выше, ниже, внутрь папки, из папки."}}</p>
<h2>{{t .Lang "Предпросмотр меню"}}</h2><div id="navigation-preview"></div></section>
<aside class="card"><h2>{{t .Lang "Доступные объекты"}}</h2>
<label>{{t .Lang "Поиск"}} <input id="navigation-filter" type="search"></label><div id="navigation-palette"></div></aside>
</div>
</main></div>
<style>
.navigation-editor-layout{display:grid;grid-template-columns:minmax(0,1fr)minmax(240px,320px);gap:16px;margin:16px 0}
.navigation-editor-layout .card{padding:16px}.navigation-row{display:flex;align-items:center;gap:6px;padding:6px;flex-wrap:wrap;border-bottom:1px solid #ddd}
.navigation-row button:first-child{flex:1;text-align:left;min-width:120px}.navigation-row[data-kind=group]{margin-left:18px}.navigation-row[data-kind=item]{margin-left:36px}
.navigation-row.selected{background:#e8f0fe}.navigation-row:focus-within{outline:2px solid #2563eb}
#navigation-properties label{display:flex;gap:6px;margin:10px 0;align-items:center}#navigation-palette>div{display:flex;gap:6px;padding:6px;align-items:center}#navigation-palette span{flex:1;overflow-wrap:anywhere}
@media(max-width:850px){.navigation-editor-layout{grid-template-columns:1fr}}
</style>
<script type="application/json" id="navigation-editor-data">{{jsJSON .NavigationEditor}}</script>
<script src="/static/settings-navigation.js"></script>
</body></html>
{{end}}`
