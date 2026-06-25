package launcher

// ── Tree tab ──────────────────────────────────────────────────────────────────

const cfgTabTree = `{{define "tab-tree"}}
<div class="cfg-split">

{{/* ── Left panel ── */}}
<div class="cfg-left" id="cfg-sidebar">
<button class="sidebar-toggle" id="sidebar-toggle" onclick="toggleSidebar()" title="{{t $.Lang "Свернуть дерево"}}">◀</button>
  <div style="padding:4px 8px 6px 8px">
    <input id="cfg-tree-search" type="search" placeholder="{{t $.Lang "Поиск метаданных…"}}" autocomplete="off" oninput="filterTree(this.value)"
      style="width:100%;box-sizing:border-box;padding:5px 8px;border:1px solid #ccd0d8;border-radius:4px;font-size:12px;background:#fff;color:#333">
  </div>
  <div class="cfg-group">{{t $.Lang "Конфигурация"}}{{if .ConfigDirty}}<span class="cfg-dirty" title="{{t $.Lang "Конфигурация на диске изменилась с момента запуска базы. Перезапустите базу, чтобы изменения применились."}}">*</span>{{end}}</div>
  <div class="cfg-item" data-id="panel-app" onclick="selItem(this)">
    <span class="ic">⚙</span>{{if .AppName}}{{.AppName}}{{else}}{{t $.Lang "Без названия"}}{{end}}{{if .ConfigDirty}}<span class="cfg-dirty" title="{{t $.Lang "Конфигурация на диске изменилась с момента запуска базы. Перезапустите базу, чтобы изменения применились."}}">*</span>{{end}}
  </div>
  <div class="cfg-item" data-id="home-page" onclick="selItem(this)">
    <span class="ic">🏠</span>Главная страница
  </div>

  <details class="cfg-tree" data-group="modules"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Общие модули"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('module')" title="{{t $.Lang "Добавить общий модуль"}}">+</span></summary>
  {{range .Modules}}
  <div class="cfg-item" data-id="mod-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📦</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="subsystems"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Подсистемы"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('subsystem')" title="{{t $.Lang "Добавить подсистему"}}">+</span></summary>
  {{range .Subsystems}}
  <div class="cfg-item" data-id="sub-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🗂</span>{{.Title}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="catalogs"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Справочники"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('catalog')" title="{{t $.Lang "Добавить справочник"}}">+</span></summary>
  {{range .Catalogs}}
  <div class="cfg-item" data-id="e-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📕</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="documents"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Документы"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('document')" title="{{t $.Lang "Добавить документ"}}">+</span></summary>
  {{range .Docs}}
  <div class="cfg-item" data-id="e-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📄</span>{{.Name}}{{if .Posting}}<span class="bp">✓</span>{{end}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="registers"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Регистры"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('register')" title="{{t $.Lang "Добавить регистр"}}">+</span></summary>
  {{range .Registers}}
  <div class="cfg-item" data-id="r-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📊</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="inforegisters"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Регистры сведений"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('inforeg')" title="{{t $.Lang "Добавить регистр сведений"}}">+</span></summary>
  {{range .InfoRegisters}}
  <div class="cfg-item" data-id="ir-{{.Name}}" onclick="selItem(this)">
    <span class="ic">{{if .Periodic}}⏱{{else}}📋{{end}}</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="accountregisters"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Регистры бухгалтерии"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('accountreg')" title="{{t $.Lang "Добавить регистр бухгалтерии"}}">+</span></summary>
  {{range .AccountRegisters}}
  <div class="cfg-item" data-id="ar-{{.Name}}" onclick="selItem(this)">
    <span class="ic">⚖</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="enums"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Перечисления"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('enum')" title="{{t $.Lang "Добавить перечисление"}}">+</span></summary>
  {{range .Enums}}
  <div class="cfg-item" data-id="en-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🔢</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="constants"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Константы"}}</span></summary>
  {{range .Constants}}
  <div class="cfg-item" data-id="cn-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🔒</span>{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="reports"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Отчёты"}}</span></summary>
  {{range .Reports}}
  <div class="cfg-item" data-id="rep-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📈</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="processors"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Обработки"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('processor')" title="{{t $.Lang "Добавить обработку"}}">+</span></summary>
  {{range .Processors}}
  <div class="cfg-item" data-id="proc-{{.Name}}" onclick="selItem(this)">
    <span class="ic">⚙</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-gid="printforms"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Печатные формы"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('printform')" title="{{t $.Lang "Добавить печатную форму"}}">+</span></summary>
  {{range .PrintForms}}
  <div class="cfg-item{{if .Shadowed}} cfg-item-shadowed{{end}}" data-id="pf-{{.Name}}" onclick="selItem(this)"{{if .Shadowed}} title="Эту YAML-форму перебивает одноимённая .os — в runtime используется DSL-вариант (см. замечание #10)"{{end}}>
    <span class="ic">🖨</span>{{if .Shadowed}}<span style="color:#d97706" title="Перебивается .os">⚠️ </span>{{end}}{{.Name}}<span style="color:#aaa;font-size:10px;margin-left:4px">→{{.Document}}{{if .Shadowed}} (скрыта .os){{end}}</span>
  </div>
  {{end}}
  {{range .DSLPrintForms}}
  <div class="cfg-item" data-id="dpf-{{.Name}}" onclick="selItem(this)"{{if .Overrides}} title="Перебивает одноимённую YAML-форму у этого документа"{{end}}>
    <span class="ic">📋</span>{{.Name}}<span style="color:#aaa;font-size:10px;margin-left:4px">→{{.Document}} (DSL{{if .Overrides}}, перебивает YAML{{end}})</span>
  </div>
  {{if .HasLayout}}
  <div class="cfg-item cfg-sub" data-id="mkt-{{.Name}}" onclick="selItem(this)" style="padding-left:32px">
    <span class="ic" style="font-size:12px">&#x1F4D0;</span>{{t $.Lang "Макет"}} {{.Name}}
  </div>
  {{end}}
  {{end}}
  </details>

  <details class="cfg-tree" data-gid="managedforms">
    <summary class="cfg-group cfg-group-hd">
      <span class="tree-toggle">▸</span><span><a href="/bases/{{.Base.ID}}/configurator/forms" style="color:inherit;text-decoration:none" title="{{t $.Lang "Все управляемые формы"}}">◇ {{t $.Lang "Управляемые формы"}}</a></span>
    </summary>
    {{range .ManagedForms}}
    <div class="cfg-item">
      <a href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}&from=e-{{.Entity}}" style="color:inherit;text-decoration:none;display:block">
        <span class="ic">◇</span>{{.Entity}} · {{formLabel .Name}}<span style="color:#aaa;font-size:10px;margin-left:4px">{{.Kind}}</span>
      </a>
    </div>
    {{end}}
  </details>

  <details class="cfg-tree" data-group="widgets"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Виджеты"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('widget')" title="{{t $.Lang "Добавить виджет"}}">+</span></summary>
  {{range .Widgets}}
  <div class="cfg-item" data-id="wdg-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🧩</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}<span style="color:#aaa;font-size:10px;margin-left:4px">[{{.Type}}]</span>
  </div>
  {{end}}
  </details>

  <details class="cfg-tree" data-group="pages"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▸</span><span>{{t $.Lang "Страницы"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('page')" title="{{t $.Lang "Добавить страницу"}}">+</span></summary>
  {{range .Pages}}
  <div class="cfg-item" data-id="page-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📄</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <div id="cfg-new-form" class="cfg-new-form" style="display:none">
    <div id="cfg-new-title" style="font-size:11px;font-weight:700;color:#555;margin-bottom:6px;text-transform:uppercase;letter-spacing:.3px"></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/new">
      <input type="hidden" name="kind" id="cfg-new-kind-inp" value="">
      <input type="text" name="name" id="cfg-new-name" placeholder="{{t $.Lang "Имя объекта"}}" autocomplete="off">
      <div class="row">
        <button type="submit" class="btn-create">{{t $.Lang "Создать"}}</button>
        <button type="button" class="btn-cancel" onclick="cfgHideNew()">✕</button>
      </div>
    </form>
  </div>
  <div id="cfg-new-form-pf" class="cfg-new-form" style="display:none">
    <div style="font-size:11px;font-weight:700;color:#555;margin-bottom:6px;text-transform:uppercase;letter-spacing:.3px">{{t $.Lang "Новая печатная форма"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/new-printform">
      <input type="text" name="name" id="cfg-new-pf-name" placeholder="{{t $.Lang "Имя формы"}} (напр. СчётНаОплату)" autocomplete="off">
      <select name="document" style="width:100%;padding:5px 6px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px;margin-bottom:6px">
        <option value="">{{t $.Lang "— документ/справочник —"}}</option>
        {{range $.AllEntityNames}}<option value="{{.}}">{{.}}</option>{{end}}
      </select>
      <div class="row">
        <button type="submit" class="btn-create">{{t $.Lang "Создать"}}</button>
        <button type="button" class="btn-cancel" onclick="cfgHideNew()">✕</button>
      </div>
    </form>
  </div>
  <div style="margin-top:12px;padding:8px 12px;border-top:1px solid #d8dde8">
    <button onclick="runCheckAll()" id="btn-check-all" style="width:100%;padding:7px 10px;background:#fff;border:1px solid #1a4a80;color:#1a4a80;border-radius:4px;cursor:pointer;font-size:12px;margin-bottom:6px">{{t $.Lang "Проверить конфигурацию"}}</button>
    <button onclick="runMigrate()" id="btn-migrate" style="width:100%;padding:7px 10px;background:#1a4a80;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:12px">{{t $.Lang "Обновить БД"}}</button>
    <div id="migrate-result" style="display:none;margin-top:6px;font-size:11px;padding:6px;border-radius:3px;max-height:120px;overflow-y:auto"></div>
  </div>
</div>

<div id="check-all-panel">
  <header>
    <span>{{t $.Lang "Проверка конфигурации"}}</span>
    <span style="flex:1"></span>
    <button type="button" id="check-all-explain-btn" onclick="explainCheckErrors(this)" title="Объяснить ошибки с помощью ИИ" style="display:none">🤖 Объяснить</button>
    <button type="button" onclick="closeCheckAll()" title="{{t $.Lang "Закрыть"}}">✕</button>
  </header>
  <div id="check-all-body"></div>
  <div id="check-all-explain-out" style="display:none;padding:8px 10px;border-top:1px solid #eef1f6;font-size:12px;white-space:pre-wrap;max-height:220px;overflow:auto;color:#1e293b"></div>
</div>

{{/* ── Right panel ── */}}
<div class="cfg-right">

  {{/* Admin panel (loaded via AJAX) */}}
  <div class="cfg-panel" id="panel-admin" style="overflow-y:auto"></div>

  {{/* App config */}}
  <div class="cfg-panel" id="panel-app">
    <div class="panel-title">⚙ {{t $.Lang "Конфигурация"}}</div>
    <div class="panel-kind">{{t $.Lang "Общие параметры приложения"}}</div>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/app" enctype="multipart/form-data" style="margin-top:12px">
      <div class="fg">
        <label>{{t $.Lang "Название конфигурации"}}</label>
        <input type="text" name="app_name" value="{{.AppName}}" placeholder="{{t $.Lang "Моя конфигурация"}}" autofocus>
        <div class="hint">{{t $.Lang "Отображается в заголовке окна и навигации пользовательского режима"}}</div>
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Версия"}}</label>
        <input type="text" name="app_version" value="{{.AppVersion}}" placeholder="1.0">
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Автор"}}</label>
        <input type="text" name="app_author" value="{{.AppAuthor}}" placeholder="{{t $.Lang "ФИО или организация"}}">
        <div class="hint">{{t $.Lang "Указывается на экране «О программе» в пользовательском режиме"}}</div>
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Правообладатель"}}</label>
        <input type="text" name="app_copyright" value="{{.AppCopyright}}" placeholder="© 2026 ...">
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Лицензия конфигурации"}}</label>
        <input type="text" name="app_license" value="{{.AppLicense}}" placeholder="{{t $.Lang "MIT / проприетарная / ..."}}">
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Язык интерфейса"}}</label>
        <select name="app_lang" style="padding:6px 8px;border:1px solid #d0d7e3;border-radius:4px;font-size:13px">
          <option value="">{{t $.Lang "По умолчанию (русский)"}}</option>
          {{range .AvailableLangs}}<option value="{{.Code}}"{{selIf $.AppLang .Code}}>{{.Native}}</option>{{end}}
        </select>
        <div class="hint">{{t $.Lang "Язык по умолчанию для пользователей этой базы"}}</div>
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Логотип"}}</label>
        <div style="display:flex;align-items:center;gap:12px;margin-bottom:6px">
          <img id="logo-preview" src="{{if .AppLogo}}/bases/{{.Base.ID}}/configurator/logo{{end}}" style="max-height:48px;max-width:120px;border:1px solid #e2e8f0;border-radius:4px;padding:2px;{{if not .AppLogo}}display:none{{end}}">
          <div>
            <label class="btn-save" style="cursor:pointer;display:inline-block;padding:4px 12px;font-size:12px">
              {{t $.Lang "Загрузить файл"}}
              <input type="file" name="app_logo_file" accept="image/*" style="display:none" onchange="previewLogo(this)">
            </label>
            {{if .AppLogo}}<button type="button" onclick="removeLogo()" style="margin-left:6px;padding:4px 8px;font-size:12px;background:none;border:1px solid #e2e8f0;border-radius:4px;cursor:pointer;color:#ef4444">{{t $.Lang "Удалить"}}</button>{{end}}
          </div>
        </div>
        <input type="hidden" name="app_logo_existing" value="{{.AppLogo}}">
        <input type="hidden" name="app_logo_remove" id="logo-remove" value="0">
        <div class="hint">{{t $.Lang "PNG, SVG, JPG — не более 2 МБ"}}</div>
      </div>
      <div class="module-save-row" style="margin-top:12px">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and .FieldsSaved (eq .FieldsSavedEntity "__app__")}}<span class="save-ok">{{t $.Lang "✓ Сохранено — перезапустите базу"}}</span>{{end}}
      </div>
    </form>
  </div>

  {{if not (or .Catalogs .Docs .Registers .InfoRegisters .Enums .Constants .Reports)}}
  <div style="color:#aaa;padding:60px 20px;text-align:center">
    <div style="font-size:36px;margin-bottom:10px">📭</div>
    <div>{{t $.Lang "Используйте «+» слева для добавления объектов конфигурации."}}</div>
  </div>
  {{end}}

  {{/* Catalogs */}}
  {{range .Catalogs}}
  <div class="cfg-panel" id="e-{{.Name}}">
    <div class="panel-title">📄 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Справочник"}}</div>
    {{template "entity-detail" (dict "Entity" . "BaseID" $.Base.ID "ConfigSource" $.Base.ConfigSource "ModuleSaved" $.ModuleSaved "ModuleSavedEntity" $.ModuleSavedEntity "AllEntityNames" $.AllEntityNames "AllEnumNames" $.AllEnumNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity "ManagedForms" $.ManagedForms "Lang" $.Lang "AvailableLangs" $.AvailableLangs)}}
  </div>
  {{end}}

  {{/* Documents */}}
  {{range .Docs}}
  <div class="cfg-panel" id="e-{{.Name}}">
    <div class="panel-title">
      📃 {{.Name}}
      {{if .Posting}}<span style="background:#dbeafe;color:#1d4ed8;font-size:11px;font-weight:600;padding:2px 8px;border-radius:10px">{{t $.Lang "проводится"}}</span>{{end}}
    </div>
    <div class="panel-kind">{{t $.Lang "Документ"}}</div>
    {{template "entity-detail" (dict "Entity" . "BaseID" $.Base.ID "ConfigSource" $.Base.ConfigSource "ModuleSaved" $.ModuleSaved "ModuleSavedEntity" $.ModuleSavedEntity "AllEntityNames" $.AllEntityNames "AllEnumNames" $.AllEnumNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity "ManagedForms" $.ManagedForms "Lang" $.Lang "AvailableLangs" $.AvailableLangs)}}
  </div>
  {{end}}

  {{/* Registers */}}
  {{range .Registers}}
  <div class="cfg-panel" id="r-{{.Name}}">
    <div class="panel-title">📊 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Регистр накопления"}}</div>
    {{template "register-detail" (dict "Register" . "BaseID" $.Base.ID "AllEntityNames" $.AllEntityNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity "Lang" $.Lang "AvailableLangs" $.AvailableLangs)}}
  </div>
  {{end}}

  {{/* InfoRegisters */}}
  {{range .InfoRegisters}}
  {{$ir := .}}
  <div class="cfg-panel" id="ir-{{.Name}}">
    <div class="panel-title">{{if .Periodic}}⏱{{else}}📋{{end}} {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Регистр сведений"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/inforeg-fields">
    <input type="hidden" name="inforeg" value="{{.Name}}">
    <div style="margin:10px 0 12px">
      <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
        <input type="radio" name="periodic" value="true" {{if .Periodic}}checked{{end}}> {{t $.Lang "Периодический (ключ включает период)"}}
      </label>
      <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer;margin-top:4px">
        <input type="radio" name="periodic" value="false" {{if not .Periodic}}checked{{end}}> {{t $.Lang "Непериодический"}}
      </label>
    </div>
    {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" .Titles)}}{{end}}
    {{$allEntities := $.AllEntityNames}}
    {{if .Dimensions}}
    <details open><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Измерения"}} ({{len .Dimensions}})</summary>
    <table class="fields-tbl" id="ir-dim-{{.Name}}">
    <tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
    {{range $i, $f := .Dimensions}}
    <input type="hidden" name="dim.{{$i}}.name" value="{{$f.Name}}">
    <tr>
      <td>{{$f.Name}}</td>
      <td>
        <select name="dim.{{$i}}.type" onchange="cfgToggleRef(this,'irdr-{{$ir.Name}}-{{$i}}');cfgToggleNum(this,'irdn-{{$ir.Name}}-{{$i}}')">
          <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
          <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
          <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
          <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
          <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
        </select>
        <span id="irdn-{{$ir.Name}}-{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
          <input type="number" min="1" name="dim.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
          , <input type="number" min="0" name="dim.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
        </span>
      </td>
      <td>
        <select name="dim.{{$i}}.ref" id="irdr-{{$ir.Name}}-{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
          <option value="">{{t $.Lang "— выбрать —"}}</option>
          {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
        </select>
      </td>
    </tr>
    {{if $.AvailableLangs}}<tr><td colspan="3" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "dim.%d.titles" $i) "Values" $f.Titles)}}</td></tr>{{end}}
    {{end}}
    </table>
    <button type="button" onclick="cfgAddField('ir-dim-{{.Name}}','new_dim','')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить измерение"}}</button>
    </details>
    {{end}}
    {{if .Resources}}
    <details open><summary class="section-hd" style="cursor:pointer;margin-top:8px">{{t $.Lang "Ресурсы"}} ({{len .Resources}})</summary>
    <table class="fields-tbl" id="ir-res-{{.Name}}">
    <tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
    {{range $i, $f := .Resources}}
    <input type="hidden" name="res.{{$i}}.name" value="{{$f.Name}}">
    <tr>
      <td>{{$f.Name}}</td>
      <td>
        <select name="res.{{$i}}.type" onchange="cfgToggleRef(this,'irrr-{{$ir.Name}}-{{$i}}');cfgToggleNum(this,'irrn-{{$ir.Name}}-{{$i}}')">
          <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
          <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
          <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
          <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
          <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
        </select>
        <span id="irrn-{{$ir.Name}}-{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
          <input type="number" min="1" name="res.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
          , <input type="number" min="0" name="res.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
        </span>
      </td>
      <td>
        <select name="res.{{$i}}.ref" id="irrr-{{$ir.Name}}-{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
          <option value="">{{t $.Lang "— выбрать —"}}</option>
          {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
        </select>
      </td>
    </tr>
    {{if $.AvailableLangs}}<tr><td colspan="3" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "res.%d.titles" $i) "Values" $f.Titles)}}</td></tr>{{end}}
    {{end}}
    </table>
    <button type="button" onclick="cfgAddField('ir-res-{{.Name}}','new_res','')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить ресурс"}}</button>
    </details>
    {{end}}
    <div class="module-save-row" style="margin-bottom:14px;margin-top:10px">
      <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
      {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
    </div>
    </form>
  </div>
  {{end}}

  {{/* AccountRegisters */}}
  {{range .AccountRegisters}}
  {{$ar := .}}
  <div class="cfg-panel" id="ar-{{.Name}}">
    <div class="panel-title">⚖ {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Регистр бухгалтерии"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/account-register">
    <input type="hidden" name="accountreg" value="{{.Name}}">
    <div class="fg" style="margin-bottom:10px">
      <label>{{t $.Lang "Заголовок"}}</label>
      <input type="text" name="title" value="{{.Title}}" placeholder="{{t $.Lang "Отображаемое имя"}}">
    </div>
    {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" .Titles)}}{{end}}
    <div class="fg" style="margin-bottom:12px">
      <label>{{t $.Lang "План счетов (имя объекта)"}}</label>
      <input type="text" name="accounts" value="{{.Accounts}}" placeholder="{{t $.Lang "ПланСчетов"}}">
    </div>
    {{$allEntities := $.AllEntityNames}}
    {{if .Resources}}
    <details open><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Ресурсы"}} ({{len .Resources}})</summary>
    <table class="fields-tbl" id="ar-res-{{.Name}}">
    <tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th></tr>
    {{range $i, $f := .Resources}}
    <input type="hidden" name="res.{{$i}}.name" value="{{$f.Name}}">
    <tr>
      <td>{{$f.Name}}</td>
      <td>
        <select name="res.{{$i}}.type" onchange="cfgToggleNum(this,'arn-{{$ar.Name}}-{{$i}}')">
          <option value="number" {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
          <option value="string" {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
          <option value="bool"   {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
        </select>
        <span id="arn-{{$ar.Name}}-{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
          <input type="number" min="1" name="res.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
          , <input type="number" min="0" name="res.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
        </span>
      </td>
    </tr>
    {{if $.AvailableLangs}}<tr><td colspan="2" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "res.%d.titles" $i) "Values" $f.Titles)}}</td></tr>{{end}}
    {{end}}
    </table>
    <button type="button" onclick="cfgAddARField('ar-res-{{.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить ресурс"}}</button>
    </details>
    {{else}}
    <div id="ar-res-{{.Name}}-wrap">
    <table class="fields-tbl" id="ar-res-{{.Name}}" style="display:none"><tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th></tr></table>
    </div>
    <button type="button" onclick="cfgAddARField('ar-res-{{.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить ресурс"}}</button>
    {{end}}
    <div class="module-save-row" style="margin-bottom:14px;margin-top:10px">
      <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
      {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
    </div>
    </form>
  </div>
  {{end}}

  {{/* Enums */}}
  {{range .Enums}}
  {{$en := .}}
  <div class="cfg-panel" id="en-{{.Name}}">
    <div class="panel-title">🔢 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Перечисление"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/enum">
      <input type="hidden" name="enum_name" value="{{.Name}}">
      <div class="section-hd" style="margin-bottom:6px">{{t $.Lang "Значения"}}</div>
      <div id="enum-vals-{{.Name}}">
        {{range $i, $v := .EnumValues}}
        <div class="enum-val-row" style="display:flex;gap:6px;align-items:flex-start;margin-bottom:4px">
          <div style="flex:1">
            <input type="text" name="value.{{$i}}.name" value="{{$v.Name}}"
                   style="width:100%;padding:4px 6px;border:1px solid #cbd5e1;border-radius:4px;font-size:13px">
            {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "value.%d.titles" $i) "Values" $v.Titles)}}{{end}}
          </div>
          <button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:16px;padding:2px 4px;flex-shrink:0"
                  onclick="enumRemoveVal(this,'enum-vals-{{$en.Name}}')">✕</button>
        </div>
        {{end}}
      </div>
      <button type="button" onclick="enumAddVal('enum-vals-{{.Name}}')"
              style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0 10px">
        + {{t $.Lang "Добавить значение"}}
      </button>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Constants */}}
  {{range .Constants}}
  {{$cn := .}}
  <div class="cfg-panel" id="cn-{{.Name}}">
    <div class="panel-title">🔒 {{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Константа"}} · <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/constant" style="margin-top:12px">
      <input type="hidden" name="const_name" value="{{.Name}}">
      <div class="fg">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="label" value="{{.Label}}" placeholder="{{t $.Lang "Отображаемое имя"}}">
      </div>
      {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "labels" "Values" .Labels)}}{{end}}
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Тип"}}</label>
        <select name="type" onchange="cfgToggleRef(this,'cnref-{{.Name}}');cfgToggleNum(this,'cnnum-{{.Name}}')">
          <option value="string" {{if eq .Type "string"}}selected{{end}}>{{t $.Lang "Строка"}}</option>
          <option value="number" {{if eq .Type "number"}}selected{{end}}>{{t $.Lang "Число"}}</option>
          <option value="date" {{if eq .Type "date"}}selected{{end}}>{{t $.Lang "Дата"}}</option>
          <option value="boolean" {{if eq .Type "boolean"}}selected{{end}}>{{t $.Lang "Булево"}}</option>
          <option value="reference" {{if eq .Type "reference"}}selected{{end}}>{{t $.Lang "Ссылка"}}</option>
        </select>
        <span id="cnnum-{{.Name}}"{{if ne .Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
          <input type="number" min="1" name="length" value="{{if .Length}}{{.Length}}{{end}}" placeholder="дл" style="width:54px;padding:3px 5px;border:1px solid #cbd5e1;border-radius:4px;font-size:12px">
          , <input type="number" min="0" name="scale" value="{{if .Length}}{{.Scale}}{{end}}" placeholder="точн" style="width:54px;padding:3px 5px;border:1px solid #cbd5e1;border-radius:4px;font-size:12px">
        </span>
      </div>
      <div id="cnref-{{.Name}}" class="fg" style="margin-top:8px;{{if ne .Type "reference"}}display:none{{end}}">
        <label>{{t $.Lang "Объект"}}</label>
        <select name="ref">
          <option value="">{{t $.Lang "— выбрать —"}}</option>
          {{range $.AllEntityNames}}<option value="{{.}}" {{if eq . $cn.RefEntity}}selected{{end}}>{{.}}</option>{{end}}
        </select>
      </div>
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "По умолчанию"}}</label>
        <input type="text" name="default" value="{{.Default}}" placeholder="{{t $.Lang "Значение по умолчанию"}}">
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Reports */}}
  {{range .Reports}}
  {{$rn := .Name}}
  <div class="cfg-panel" id="rep-{{.Name}}">
    <div class="panel-title">📈 {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Отчёт"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/report">
      <input type="hidden" name="report_name" value="{{.Name}}">
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="title" value="{{.Title}}" placeholder="{{t $.Lang "Название отчёта"}}">
      </div>
      {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" .Titles)}}{{end}}
      <div class="obj-editor">
        <div class="obj-tabs">
          <div class="obj-tab active" onclick="cfgObjTab(this,'ot-rep-params-{{$rn}}')">{{t $.Lang "Параметры"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-query-{{$rn}}')">{{t $.Lang "Запрос"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-chart-{{$rn}}')">{{t $.Lang "Диаграмма"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-struct-{{$rn}}')">{{t $.Lang "Структура"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-cond-{{$rn}}')">{{t $.Lang "Оформление"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-cchart-{{$rn}}')">{{t $.Lang "График"}}</div>
        </div>
        <div class="obj-pane active" id="ot-rep-params-{{$rn}}">
          <div class="section-hd" style="margin-top:12px">
            Параметры
            <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="repAddParam('params-{{$rn}}')">+</button>
          </div>
          <table class="fields-tbl" id="params-{{$rn}}">
            <tr><th>{{t $.Lang "Имя"}} (&amp;{{t $.Lang "Параметр"}})</th><th>{{t $.Lang "Тип"}}</th><th>{{t $.Lang "Заголовок"}}</th><th></th></tr>
            {{range $i, $p := .Params}}
            <tr>
              <td><input type="text" name="param.{{$i}}.name" value="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td>
                <select name="param.{{$i}}.type" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
                  <option value="string" {{if eq $p.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
                  <option value="date"   {{if eq $p.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
                  <option value="number" {{if eq $p.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
                  <option value="select" {{if eq $p.Type "select"}}selected{{end}}>{{t $.Lang "список"}}</option>
                  {{range $.AllEntityNames}}<option value="reference:{{.}}" {{if eq $p.Type (print "reference:" .)}}selected{{end}}>ссылка: {{.}}</option>
                  {{end}}
                </select>
              </td>
              <td><input type="text" name="param.{{$i}}.label" value="{{$p.Label}}" placeholder="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();repReindex('params-{{$rn}}')">✕</button></td>
            </tr>
            {{if $.AvailableLangs}}<tr><td colspan="4" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "param.%d.labels" $i) "Values" $p.Labels)}}</td></tr>{{end}}
            {{end}}
          </table>
        </div>
        <div class="obj-pane" id="ot-rep-query-{{$rn}}">
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Запрос"}}</div>
          <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
            <pre class="os-code clickable-code" id="pre-rep-{{.Name}}"
                 onclick="startEdit('rep-{{.Name}}')">{{if .Query}}{{.Query}}{{else}}ВЫБРАТЬ&#10;  *&#10;ИЗ РегистрНакопления.ИмяРегистра{{end}}</pre>
            <textarea class="os-edit" id="ta-rep-{{.Name}}" name="query"
                      style="display:none"
                      onblur="endEdit('rep-{{.Name}}')">{{.Query}}</textarea>
          </div>
        </div>
        <div class="obj-pane" id="ot-rep-chart-{{$rn}}">
          <div class="fg" style="margin-top:12px">
            <label>{{t $.Lang "Процедура диаграммы"}} (chart_proc)</label>
            <input type="text" name="chart_proc" value="{{.ChartProc}}" placeholder="СформироватьДиаграмму">
          </div>
          <div class="section-hd" style="margin-top:8px">{{t $.Lang "Код диаграммы"}} (.rep.os) <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
          <div class="code-wrap">
            <pre class="os-code clickable-code" id="pre-repchart-{{.Name}}"
                 onclick="startEdit('repchart-{{.Name}}')">{{.ChartSource}}</pre>
            <textarea class="os-edit" id="ta-repchart-{{.Name}}" name="chart_source"
                      style="display:none"
                      onblur="endEdit('repchart-{{.Name}}')">{{.ChartSource}}</textarea>
          </div>
        </div>
        <div class="obj-pane" id="ot-rep-struct-{{$rn}}">
          <input type="hidden" name="comp.present" value="1">
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Группировки"}}
            <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="compAddRow('cg-{{$rn}}','comp.grouping.')">+</button></div>
          <table class="fields-tbl" id="cg-{{$rn}}">
            {{with .Composition}}{{range $i, $g := .Groupings}}
            <tr><td><input type="text" name="comp.grouping.{{$i}}" value="{{$g}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();compReindex('cg-{{$rn}}','comp.grouping.')">✕</button></td></tr>
            {{end}}{{end}}
          </table>
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Колонки (кросс-таблица)"}}
            <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="compAddRow('cc-{{$rn}}','comp.column.')">+</button>
            <span style="font-weight:normal;color:#888;font-size:11px;margin-left:8px">{{t $.Lang "если заполнено — отчёт строится кросс-таблицей"}}</span></div>
          <table class="fields-tbl" id="cc-{{$rn}}">
            {{with .Composition}}{{range $i, $c := .Columns}}
            <tr><td><input type="text" name="comp.column.{{$i}}" value="{{$c}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();compReindex('cc-{{$rn}}','comp.column.')">✕</button></td></tr>
            {{end}}{{end}}
          </table>
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Показатели"}}
            <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="compAddMeasure('cm-{{$rn}}')">+</button></div>
          <table class="fields-tbl" id="cm-{{$rn}}">
            <tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Агрегат"}}</th><th>{{t $.Lang "Подпись"}}</th><th>{{t $.Lang "Выравн."}}</th><th>{{t $.Lang "Формат"}}</th><th>{{t $.Lang "Выражение"}}</th><th></th></tr>
            {{with .Composition}}{{range $i, $m := .Measures}}
            <tr>
              <td><input type="text" name="comp.measure.{{$i}}.field" value="{{$m.Field}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><select name="comp.measure.{{$i}}.agg" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
                <option value="sum" {{if eq $m.Agg "sum"}}selected{{end}}>sum</option>
                <option value="count" {{if eq $m.Agg "count"}}selected{{end}}>count</option>
                <option value="avg" {{if eq $m.Agg "avg"}}selected{{end}}>avg</option>
                <option value="min" {{if eq $m.Agg "min"}}selected{{end}}>min</option>
                <option value="max" {{if eq $m.Agg "max"}}selected{{end}}>max</option></select></td>
              <td><input type="text" name="comp.measure.{{$i}}.title" value="{{$m.Title}}" placeholder="{{$m.Field}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><select name="comp.measure.{{$i}}.align" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
                <option value="" {{if eq $m.Align ""}}selected{{end}}>—</option>
                <option value="left" {{if eq $m.Align "left"}}selected{{end}}>влево</option>
                <option value="right" {{if eq $m.Align "right"}}selected{{end}}>вправо</option>
                <option value="center" {{if eq $m.Align "center"}}selected{{end}}>по центру</option></select></td>
              <td><input type="text" name="comp.measure.{{$i}}.format" value="{{$m.Format}}" placeholder="#,##0.00" style="width:80px;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><input type="text" name="comp.measure.{{$i}}.expr" value="{{$m.Expr}}" placeholder="ВаловаяПрибыль / Выручка * 100" title="выражение по другим показателям, напр. ВаловаяПрибыль / Выручка * 100" style="width:160px;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();compReindexMeasure('cm-{{$rn}}')">✕</button></td>
            </tr>
            {{end}}{{end}}
          </table>
          <div style="margin-top:10px">
            <label><input type="checkbox" name="comp.totals.grand" {{if .Composition}}{{if .Composition.Totals.Grand}}checked{{end}}{{end}}> {{t $.Lang "Общий итог"}}</label>
            <label style="margin-left:12px"><input type="checkbox" name="comp.totals.subtotals" {{if .Composition}}{{if .Composition.Totals.Subtotals}}checked{{end}}{{end}}> {{t $.Lang "Промежуточные итоги"}}</label>
            <label style="margin-left:12px"><input type="checkbox" name="comp.detail" {{if .Composition}}{{if .Composition.Detail}}checked{{end}}{{end}}> {{t $.Lang "Детальные строки"}}</label>
          </div>
          <div style="margin-top:8px;display:flex;gap:12px;align-items:center">
            <div class="fg" style="flex:1"><label>{{t $.Lang "Поле-ссылка"}}</label>
              <input type="text" name="comp.detail_link" value="{{if .Composition}}{{.Composition.DetailLink}}{{end}}" placeholder="{{t $.Lang "колонка запроса с UUID документа"}}" title="{{t $.Lang "колонка запроса с UUID документа"}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></div>
            <div class="fg" style="flex:1"><label>{{t $.Lang "Сущность (расшифровка)"}}</label>
              <input type="text" name="comp.detail_entity" value="{{if .Composition}}{{.Composition.DetailEntity}}{{end}}" placeholder="{{t $.Lang "имя документа для перехода"}}" title="{{t $.Lang "имя документа для перехода"}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></div>
          </div>
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Сортировка"}}
            <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="compAddSort('cs-{{$rn}}')">+</button></div>
          <table class="fields-tbl" id="cs-{{$rn}}">
            {{with .Composition}}{{range $i, $s := .Sort}}
            <tr><td><input type="text" name="comp.sort.{{$i}}.field" value="{{$s.Field}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><select name="comp.sort.{{$i}}.dir" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"><option value="asc" {{if eq $s.Dir "asc"}}selected{{end}}>asc</option><option value="desc" {{if eq $s.Dir "desc"}}selected{{end}}>desc</option></select></td>
              <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();compReindexSort('cs-{{$rn}}')">✕</button></td></tr>
            {{end}}{{end}}
          </table>
        </div>
        <div class="obj-pane" id="ot-rep-cond-{{$rn}}">
          <div class="section-hd">{{t $.Lang "Оформление вывода"}}</div>
          {{$al := ""}}{{with .Composition}}{{$al = .Appearance.Lines}}{{end}}
          <div style="margin-top:6px;display:flex;gap:16px;align-items:center;flex-wrap:wrap">
            <div class="fg"><label>{{t $.Lang "Линии сетки"}}</label>
              <select name="comp.appearance.lines" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
                <option value="" {{if or (eq $al "") (eq $al "horizontal")}}selected{{end}}>{{t $.Lang "горизонтальные (как сейчас)"}}</option>
                <option value="vertical" {{if eq $al "vertical"}}selected{{end}}>{{t $.Lang "вертикальные"}}</option>
                <option value="both" {{if eq $al "both"}}selected{{end}}>{{t $.Lang "и те и те"}}</option>
                <option value="none" {{if eq $al "none"}}selected{{end}}>{{t $.Lang "без линий"}}</option>
              </select></div>
            <label><input type="checkbox" name="comp.appearance.zebra" {{with .Composition}}{{if .Appearance.Zebra}}checked{{end}}{{end}}> {{t $.Lang "Чередование строк (зебра)"}}</label>
          </div>
          <div class="section-hd" style="margin-top:14px">{{t $.Lang "Условное оформление"}}
            <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="compAddCond('cc-{{$rn}}')">+</button></div>
          <table class="fields-tbl" id="cc-{{$rn}}">
            <tr><th>{{t $.Lang "Когда"}} (DSL)</th><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Цвет"}}</th><th>{{t $.Lang "Фон"}}</th><th>{{t $.Lang "Ж"}}</th><th>{{t $.Lang "К"}}</th><th></th></tr>
            {{with .Composition}}{{range $i, $r := .Conditional}}
            <tr>
              <td><input type="text" name="comp.cond.{{$i}}.when" value="{{$r.When}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><input type="text" name="comp.cond.{{$i}}.field" value="{{$r.Field}}" placeholder="{{t $.Lang "вся строка"}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><input type="color" name="comp.cond.{{$i}}.color" value="{{if $r.Style.Color}}{{$r.Style.Color}}{{else}}#000000{{end}}"></td>
              <td><input type="color" name="comp.cond.{{$i}}.background" value="{{if $r.Style.Background}}{{$r.Style.Background}}{{else}}#ffffff{{end}}"></td>
              <td><input type="checkbox" name="comp.cond.{{$i}}.bold" {{if $r.Style.Bold}}checked{{end}}></td>
              <td><input type="checkbox" name="comp.cond.{{$i}}.italic" {{if $r.Style.Italic}}checked{{end}}></td>
              <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();compReindexCond('cc-{{$rn}}')">&#x2715;</button></td>
            </tr>
            {{end}}{{end}}
          </table>
          <div class="edit-hint" style="margin-top:6px">{{t $.Lang "Чёрный текст / белый фон трактуются как «не задано»."}}</div>
        </div>
        <div class="obj-pane" id="ot-rep-cchart-{{$rn}}">
          {{$ch := ""}}{{$cc := ""}}
          {{with .Composition}}{{with .Chart}}{{$ch = .Type}}{{$cc = .Category}}{{end}}{{end}}
          <div class="fg" style="margin-top:12px"><label>{{t $.Lang "Тип графика"}}</label>
            <select name="comp.chart.type" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
              <option value="" {{if eq $ch ""}}selected{{end}}>{{t $.Lang "нет"}}</option>
              <option value="bar" {{if eq $ch "bar"}}selected{{end}}>{{t $.Lang "столбцы"}}</option>
              <option value="line" {{if eq $ch "line"}}selected{{end}}>{{t $.Lang "линия"}}</option>
              <option value="pie" {{if eq $ch "pie"}}selected{{end}}>{{t $.Lang "круг"}}</option>
            </select></div>
          <div class="fg" style="margin-top:8px"><label>{{t $.Lang "Категория (поле группировки)"}}</label>
            <input type="text" name="comp.chart.category" value="{{$cc}}" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></div>
          <div class="fg" style="margin-top:8px"><label>{{t $.Lang "Ряды (показатели через запятую)"}}</label>
            <input type="text" name="comp.chart.series" value="{{with .Composition}}{{with .Chart}}{{join .Series ","}}{{end}}{{end}}" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></div>
        </div>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','repchart-{{.Name}}','{{.Name}}')">{{t $.Lang "Проверить"}}</button>
        <button type="button" class="btn-check" onclick="previewReport('{{lower .Name}}')">{{t $.Lang "Предпросмотр"}}</button>
        <span class="check-result" id="check-repchart-{{.Name}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Modules */}}
  {{range .Modules}}
  {{$mn := .Name}}
  <div class="cfg-panel" id="mod-{{.Name}}">
    <div class="panel-title">📦 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Общий модуль"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/common-module">
      <input type="hidden" name="module_name" value="{{.Name}}">
      <div class="section-hd">Исходный код <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
      <div class="module-editor-wrap">
        <div class="code-wrap">
          <pre class="os-code" id="pre-mod-{{$mn}}" onclick="startEdit('mod-{{$mn}}')">{{if .Source}}{{.Source}}{{else}}Функция ИмяФункции(Параметр)&#10;    Возврат Параметр&#10;КонецФункции{{end}}</pre>
          <textarea class="os-edit" id="ta-mod-{{$mn}}" name="source"
                    style="display:none"
                    onblur="endEdit('mod-{{$mn}}')">{{.Source}}</textarea>
        </div>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','mod-{{$mn}}','{{$mn}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-mod-{{$mn}}"></span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Processors */}}
  {{range .Processors}}
  {{$pn := .Name}}
  <div class="cfg-panel" id="proc-{{.Name}}">
    <div class="panel-title">⚙ {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Обработка"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/processor">
      <input type="hidden" name="processor_name" value="{{.Name}}">
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="title" value="{{.Title}}" placeholder="{{t $.Lang "Название обработки"}}">
      </div>
      {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" .Titles)}}{{end}}
      <div class="obj-editor">
        <div class="obj-tabs">
          <div class="obj-tab active" onclick="cfgObjTab(this,'ot-proc-params-{{$pn}}')">{{t $.Lang "Параметры"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-proc-code-{{$pn}}')">{{t $.Lang "Код"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-proc-form-{{$pn}}')">{{t $.Lang "Форма"}}</div>
        </div>
        <div class="obj-pane active" id="ot-proc-params-{{$pn}}">
          <div class="section-hd" style="margin-top:12px">
            Параметры
            <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="repAddParam('pparams-{{$pn}}')">+</button>
          </div>
          <table class="fields-tbl" id="pparams-{{$pn}}">
            <tr><th>{{t $.Lang "Имя"}} (&amp;{{t $.Lang "Параметры"}}.*)</th><th>{{t $.Lang "Тип"}}</th><th>{{t $.Lang "Заголовок"}}</th><th></th></tr>
            {{range $i, $p := .Params}}
            <tr>
              <td><input type="text" name="param.{{$i}}.name" value="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td>
                <select name="param.{{$i}}.type" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
                  <option value="string" {{if eq $p.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
                  <option value="date"   {{if eq $p.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
                  <option value="number" {{if eq $p.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
                </select>
              </td>
              <td><input type="text" name="param.{{$i}}.label" value="{{$p.Label}}" placeholder="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
              <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();repReindex('pparams-{{$pn}}')">✕</button></td>
            </tr>
            {{if $.AvailableLangs}}<tr><td colspan="4" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "param.%d.labels" $i) "Values" $p.Labels)}}</td></tr>{{end}}
            {{end}}
          </table>
        </div>
        <div class="obj-pane" id="ot-proc-code-{{$pn}}">
          <details open><summary class="section-hd" style="cursor:pointer;margin-top:12px">{{t $.Lang "Исходный код"}} ({{t $.Lang "Процедура Выполнить()"}}) <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></summary>
          <div class="code-wrap">
            <pre class="os-code" id="pre-proc-{{$pn}}" onclick="startEdit('proc-{{$pn}}')">{{if .Source}}{{.Source}}{{else}}Процедура Выполнить()&#10;    Сообщить("Привет!")&#10;КонецПроцедуры{{end}}</pre>
            <textarea class="os-edit" id="ta-proc-{{$pn}}" name="source"
                      style="display:none"
                      onblur="endEdit('proc-{{$pn}}')">{{.Source}}</textarea>
          </div>
          </details>
        </div>
        <div class="obj-pane" id="ot-proc-form-{{$pn}}">
          {{$procForms := filterFormsByEntity $.ManagedForms .Name}}
          <div style="background:#f8fafc;border:1px dashed #c8d4f0;border-radius:6px;padding:12px 14px;font-size:12px;color:#475569;line-height:1.5">
            {{if $procForms}}
            <table style="width:100%;border-collapse:collapse;margin:8px 0;font-size:12px">
              <thead><tr style="background:#fff;border-bottom:1px solid #e2e8f0">
                <th style="text-align:left;padding:4px 8px">{{t $.Lang "Имя"}}</th>
                <th style="text-align:left;padding:4px 8px">{{t $.Lang "Тип"}}</th>
                <th style="text-align:left;padding:4px 8px">{{t $.Lang "Модуль"}}</th>
                <th></th>
              </tr></thead>
              <tbody>
              {{range $procForms}}
              <tr style="border-bottom:1px solid #eef0f5">
                <td style="padding:6px 8px">◇ {{formLabel .Name}}</td>
                <td style="padding:6px 8px">{{if .Kind}}{{.Kind}}{{else}}—{{end}}</td>
                <td style="padding:6px 8px">{{if .HasOS}}{{t $.Lang "есть"}}{{else}}—{{end}}</td>
                <td style="text-align:right;padding:6px 8px">
                  <a href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}&from=proc-{{$pn}}"
                     style="display:inline-block;padding:3px 10px;background:#1a4a80;color:#fff;text-decoration:none;border-radius:4px;font-size:11px">
                    {{t $.Lang "Редактировать"}}
                  </a>
                </td>
              </tr>
              {{end}}
              </tbody>
            </table>
            {{else}}
            <p style="margin:0 0 10px">{{t $.Lang "У обработки"}} <b>{{.Name}}</b> {{t $.Lang "нет управляемых форм."}}</p>
            {{end}}
            <div style="margin-top:10px;display:flex;gap:6px;flex-wrap:wrap;align-items:center">
              <a href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Name}}&name=ФормаОбъекта&from=proc-{{$pn}}"
                 style="display:inline-block;padding:5px 12px;background:#16a34a;color:#fff;text-decoration:none;border-radius:4px;font-size:12px">
                + {{t $.Lang "Форма объекта"}}
              </a>
              <a href="/bases/{{$.Base.ID}}/configurator/forms"
                 style="display:inline-block;padding:5px 12px;background:#e2e8f0;color:#334155;text-decoration:none;border-radius:4px;font-size:12px">
                {{t $.Lang "Все формы"}} / {{t $.Lang "Импорт из 1С"}}
              </a>
            </div>
          </div>
        </div>
        <div class="module-save-row">
          <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
          <button type="button" class="btn-check" onclick="runCheck('dsl','proc-{{$pn}}','{{$pn}}')">{{t $.Lang "Проверить"}}</button>
          <span class="check-result" id="check-proc-{{$pn}}"></span>
          {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
        </div>
      </div>
    </form>
  </div>
  {{end}}

  {{/* Print forms */}}
  {{range .PrintForms}}
  <div class="cfg-panel" id="pf-{{.Name}}">
    <div class="panel-title">🖨 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Печатная форма"}} · {{t $.Lang "документ"}}: {{.Document}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/printform">
      <input type="hidden" name="printform_filename" value="{{.FileName}}">
      <div class="section-hd">YAML-описание <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
      <div class="code-wrap">
        <pre class="os-code" id="pre-pf-{{.Name}}" onclick="startEdit('pf-{{.Name}}')">{{.Source}}</pre>
        <textarea class="os-edit" id="ta-pf-{{.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('pf-{{.Name}}')">{{.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* DSL Print forms (.os) */}}
  {{range .DSLPrintForms}}
  <div class="cfg-panel" id="dpf-{{.Name}}">
    <div class="panel-title">📋 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "DSL печатная форма"}} · {{t $.Lang "документ"}}: {{.Document}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/printform">
      <input type="hidden" name="printform_filename" value="{{.FileName}}">
      <input type="hidden" name="printform_dsl" value="1">
      {{if .HasLayout}}<div class="section-hd" style="color:#4a9">{{t $.Lang "Макет привязан"}} (<code>{{.Name}}.layout.yaml</code>)</div>
      {{else}}<div style="margin:6px 0">
        <button type="button" onclick="cfgCreateOSLayout('/bases/{{$.Base.ID}}/configurator/new-layout','{{.Name}}')"
                style="font-size:12px;padding:5px 12px;background:#16a34a;color:#fff;border:none;border-radius:4px;cursor:pointer">
          &#x1F4D0; {{t $.Lang "Создать макет"}}
        </button>
      </div>{{end}}
      <div class="section-hd">{{t $.Lang "Код формы"}} (.os) <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
      <div class="code-wrap">
        <pre class="os-code" id="pre-dpf-{{.Name}}" onclick="startEdit('dpf-{{.Name}}')">{{.Source}}</pre>
        <textarea class="os-edit" id="ta-dpf-{{.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('dpf-{{.Name}}')">{{.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','dpf-{{.Name}}','{{.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-dpf-{{.Name}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Layout panels */}}
  {{range .DSLPrintForms}}
  {{if .HasLayout}}
  <div class="cfg-panel" id="mkt-{{.Name}}">
    <div class="panel-title">&#x1F4D0; {{t $.Lang "Макет"}}: {{.Name}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/layout" onsubmit="return saveLayoutEditor('{{.Name}}')">
      <input type="hidden" name="layout_name" value="{{.Name}}">

      {{/* Toolbar: structural operations */}}
      <div style="display:flex;gap:6px;margin:4px 0 8px;flex-wrap:wrap;align-items:center">
        <button type="button" class="btn-save" onclick="addLayoutArea('{{.Name}}')" style="font-size:12px;padding:4px 10px">+ {{t $.Lang "Область"}}</button>
        <span style="width:1px;background:#d1d5db;align-self:stretch"></span>
        <button type="button" onclick="ldAddRow('{{.Name}}')"     style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">+ {{t $.Lang "Строка"}}</button>
        <button type="button" onclick="ldAddColumn('{{.Name}}')"  style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">+ {{t $.Lang "Колонка"}}</button>
        <button type="button" onclick="ldDelRow('{{.Name}}')"     style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #fcc;border-radius:4px;cursor:pointer;color:#c33">{{t $.Lang "Удалить строку"}}</button>
        <button type="button" onclick="ldDelColumn('{{.Name}}')"  style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #fcc;border-radius:4px;cursor:pointer;color:#c33">{{t $.Lang "Удалить колонку"}}</button>
        <span style="width:1px;background:#d1d5db;align-self:stretch"></span>
        <button type="button" onclick="ldMerge('{{.Name}}')"      style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">{{t $.Lang "Объединить"}} →</button>
        <button type="button" onclick="ldMergeDown('{{.Name}}')"  style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">{{t $.Lang "Объединить вниз"}} ↓</button>
        <button type="button" onclick="ldSplit('{{.Name}}')"      style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">{{t $.Lang "Разъединить"}} →</button>
        <button type="button" onclick="ldUnmergeVertical('{{.Name}}')" style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">{{t $.Lang "Разъединить вниз"}} ↓</button>
        <span style="width:1px;background:#d1d5db;align-self:stretch"></span>
        <button type="button" onclick="ldPreview('{{.Name}}','{{.Document}}','html')" style="font-size:12px;padding:4px 10px;background:#0ea5e9;color:#fff;border:none;border-radius:4px;cursor:pointer">{{t $.Lang "Предпросмотр"}}</button>
        <button type="button" onclick="ldPreview('{{.Name}}','{{.Document}}','pdf')"  title="{{t $.Lang "Откроется во внешнем приложении (системный просмотрщик PDF)"}}" style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">{{t $.Lang "Предпросмотр PDF"}}</button>
        <span style="width:1px;background:#d1d5db;align-self:stretch"></span>
        <button type="button" onclick="cfgImportPdfLayout('/bases/{{$.Base.ID}}/configurator/layout/import-pdf')" title="{{t $.Lang "Извлечь черновик макета из PDF (выгрузка 1С/Excel с текстовым слоем)"}}" style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">&#x1F4C4; {{t $.Lang "Из PDF"}}</button>
      </div>

      {{/* Параметры листа: формат/ориентация/поля → печатная граница в конструкторе */}}
      <div style="display:flex;gap:8px;align-items:center;margin:0 0 8px;font-size:12px;flex-wrap:wrap;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px;padding:6px 10px">
        <span style="font-weight:600;color:#475569">&#x1F4C4; {{t $.Lang "Лист"}}:</span>
        <select id="pg-fmt-{{.Name}}" onchange="ldSetPageField('{{.Name}}','format',this.value)" style="padding:3px">
          <option>A4</option><option>A5</option><option>A3</option><option value="Letter">Letter</option><option value="Legal">Legal</option>
        </select>
        <select id="pg-ori-{{.Name}}" onchange="ldSetPageField('{{.Name}}','orientation',this.value)" style="padding:3px">
          <option value="portrait">{{t $.Lang "книжная"}}</option><option value="landscape">{{t $.Lang "альбомная"}}</option>
        </select>
        <span style="color:#475569">{{t $.Lang "Поля, мм"}}:</span>
        <input id="pg-ml-{{.Name}}" type="number" min="0" step="1" title="{{t $.Lang "Левое"}}"  onchange="ldSetPageMargin('{{.Name}}','left',this.value)"   style="width:46px;padding:3px">
        <input id="pg-mt-{{.Name}}" type="number" min="0" step="1" title="{{t $.Lang "Верхнее"}}" onchange="ldSetPageMargin('{{.Name}}','top',this.value)"    style="width:46px;padding:3px">
        <input id="pg-mr-{{.Name}}" type="number" min="0" step="1" title="{{t $.Lang "Правое"}}"  onchange="ldSetPageMargin('{{.Name}}','right',this.value)"  style="width:46px;padding:3px">
        <input id="pg-mb-{{.Name}}" type="number" min="0" step="1" title="{{t $.Lang "Нижнее"}}"  onchange="ldSetPageMargin('{{.Name}}','bottom',this.value)" style="width:46px;padding:3px">
        <span id="pg-info-{{.Name}}" style="color:#ef4444;font-weight:600"></span>
      </div>

      {{/* Split view: YAML editor (left) + visual designer (right) */}}
      <div style="display:flex;border:1px solid #d1d5db;border-radius:0 0 6px 6px;overflow:hidden;min-height:340px">
        {{/* Left pane: YAML */}}
        <div id="yamlpane-{{.Name}}" data-collapsed="0" style="flex:0 0 42%;border-right:1px solid #d1d5db;display:flex;flex-direction:column;min-width:0;overflow:hidden">
          <div style="background:#f8fafc;border-bottom:1px solid #e2e8f0;padding:3px 4px 3px 10px;font-size:11px;font-weight:600;color:#64748b;flex-shrink:0;letter-spacing:.03em;display:flex;align-items:center;justify-content:space-between;white-space:nowrap">
            <span id="yamllbl-{{.Name}}">YAML</span>
            <button type="button" id="yamltgl-{{.Name}}" onclick="ldToggleYaml('{{.Name}}')" title="{{t $.Lang "Свернуть/развернуть YAML"}}" style="border:none;background:transparent;cursor:pointer;font-size:13px;color:#64748b;padding:0 2px;line-height:1">⮜</button>
          </div>
          <textarea id="ta-mkt-{{.Name}}" name="source"
                    style="flex:1;padding:8px;border:none;outline:none;font-family:'Cascadia Code','Consolas',monospace;font-size:11px;resize:none;tab-size:2;background:#fafbfc;min-height:300px;width:100%;box-sizing:border-box;line-height:1.5"
                    oninput="scheduleYamlSync('{{.Name}}')"
                    onblur="applyYaml('{{.Name}}')">{{.LayoutYAML}}</textarea>
        </div>
        {{/* Right pane: Visual designer */}}
        <div style="flex:1;display:flex;flex-direction:column;min-width:0;overflow:hidden">
          <div style="background:#f8fafc;border-bottom:1px solid #e2e8f0;padding:3px 10px;font-size:11px;font-weight:600;color:#64748b;flex-shrink:0;letter-spacing:.03em">{{t $.Lang "Конструктор"}}</div>
          <div id="veditor-{{.Name}}" style="flex:1;padding:8px;overflow:auto;background:#fff">{{if .LayoutPreview}}{{.LayoutPreview}}{{else}}<p style="color:#999;font-size:12px">{{t $.Lang "Нет данных. Нажмите «+ Область» для начала."}}</p>{{end}}</div>
        </div>
        {{/* Data binding panel (6.5): дерево реквизитов/ТЧ/констант */}}
        <div style="flex:0 0 220px;display:flex;flex-direction:column;min-width:0;border-left:1px solid #d1d5db;background:#fcfdff">
          <div style="background:#f8fafc;border-bottom:1px solid #e2e8f0;padding:3px 10px;font-size:11px;font-weight:600;color:#64748b;flex-shrink:0;letter-spacing:.03em">{{t $.Lang "Данные"}}</div>
          <div id="vdata-{{.Name}}" style="flex:1;padding:6px 8px;overflow:auto;font-size:12px"></div>
        </div>
      </div>

      {{/* Cell properties panel — закреплённый док снизу (не проматывает страницу
           при выборе ячейки). На неактивной cfg-panel скрыт через display:none. */}}
      <div id="vprops-{{.Name}}" style="display:none;position:fixed;left:0;right:0;bottom:0;z-index:50;max-height:44vh;overflow:auto;background:#f0f8ff;border-top:2px solid #b0d0f0;box-shadow:0 -4px 16px rgba(15,23,42,.18);padding:10px 14px">
        <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:6px">
          <span style="font-weight:bold;font-size:12px">{{t $.Lang "Свойства ячейки"}}</span>
          <span>
            <button type="button" onclick="ldDelCell('{{.Name}}')" style="font-size:11px;padding:3px 8px;background:#fff;border:1px solid #fcc;border-radius:4px;cursor:pointer;color:#c33">{{t $.Lang "Удалить ячейку"}}</button>
            <button type="button" onclick="ldDeselect('{{.Name}}')" title="{{t $.Lang "Закрыть"}}" style="font-size:13px;padding:3px 9px;margin-left:6px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;color:#334155">✕</button>
          </span>
        </div>
        <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:6px;font-size:12px">
          <div><label>{{t $.Lang "Текст"}}</label><br><input id="vp-text-{{.Name}}" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','text',this.value)"></div>
          <div><label>{{t $.Lang "Параметр"}}</label><br><input id="vp-param-{{.Name}}" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','parameter',this.value)"></div>
          <div><label>{{t $.Lang "Формат"}}</label><br>
            <select id="vp-fmt-{{.Name}}" style="width:100%;padding:3px" onchange="ldSetFormat('{{.Name}}',this.value)">
              <option value="">{{t $.Lang "Без формата"}}</option>
              <option value="date">{{t $.Lang "Дата"}}</option>
              <option value="datetime">{{t $.Lang "Дата и время"}}</option>
              <option value="number:2">{{t $.Lang "Число (2 знака)"}}</option>
              <option value="number:3">{{t $.Lang "Число (3 знака)"}}</option>
              <option value="currency">{{t $.Lang "Валюта"}}</option>
              <option value="upper">{{t $.Lang "ВЕРХНИЙ регистр"}}</option>
              <option value="lower">{{t $.Lang "нижний регистр"}}</option>
            </select>
          </div>
          <div style="grid-column:1/4;font-size:11px;color:#5b7088;background:#eef5ff;border:1px solid #d6e4f5;border-radius:4px;padding:5px 8px;margin:2px 0">
            &#x1F4A1; {{t $.Lang "В поле «Текст» можно вставлять выражения:"}}
            <code>{{"{{"}}{{t $.Lang "Номер"}}{{"}}"}}</code>,
            <code>{{"{{"}}{{t $.Lang "Дата"}}|date{{"}}"}}</code>,
            <code>{{"{{"}}{{t $.Lang "Контрагент.Наименование"}}{{"}}"}}</code>
          </div>
          <div><label>{{t $.Lang "Шрифт"}}</label><br>
            <select id="vp-ff-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','fontFamily',this.value)">
              <option value="">{{t $.Lang "По умолчанию"}}</option>
              <option>Arial</option><option>Times New Roman</option><option>Courier New</option><option>Verdana</option><option>Tahoma</option>
            </select>
          </div>
          <div><label>{{t $.Lang "Размер"}} (pt)</label><br><input type="number" id="vp-fs-{{.Name}}" min="6" max="72" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','fontSize',parseInt(this.value)||0)"></div>
          <div><label><input type="checkbox" id="vp-bold-{{.Name}}" onchange="updateCellProp('{{.Name}}','bold',this.checked)"> {{t $.Lang "Жирный"}}</label>
               &nbsp;<label><input type="checkbox" id="vp-italic-{{.Name}}" onchange="updateCellProp('{{.Name}}','italic',this.checked)"> {{t $.Lang "Курсив"}}</label></div>
          <div></div>
          <div><label>{{t $.Lang "Гор. выравнивание"}}</label><br>
            <select id="vp-align-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','align',this.value)">
              <option value="">—</option><option value="left">{{t $.Lang "Лево"}}</option><option value="center">{{t $.Lang "Центр"}}</option><option value="right">{{t $.Lang "Право"}}</option>
            </select>
          </div>
          <div><label>{{t $.Lang "Верт. выравнивание"}}</label><br>
            <select id="vp-valign-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','valign',this.value)">
              <option value="">—</option><option value="top">{{t $.Lang "Верх"}}</option><option value="middle">{{t $.Lang "Середина"}}</option><option value="bottom">{{t $.Lang "Низ"}}</option>
            </select>
          </div>
          <div></div>
          <div><label>{{t $.Lang "Фон"}}</label><br><input type="color" id="vp-bg-{{.Name}}" style="width:100%;height:28px" oninput="updateCellProp('{{.Name}}','backColor',this.value)"></div>
          <div><label>{{t $.Lang "Цвет текста"}}</label><br><input type="color" id="vp-fg-{{.Name}}" style="width:100%;height:28px" oninput="updateCellProp('{{.Name}}','textColor',this.value)"></div>
          <div></div>
          {{/* Границы по сторонам (6.3): тоггл-кнопки Л/В/П/Н + толщина + пресеты. */}}
          <div style="grid-column:1/4;border-top:1px solid #d6e4f5;margin-top:4px;padding-top:6px">
            <label style="font-weight:600">{{t $.Lang "Границы по сторонам"}}</label>
            <div style="display:flex;flex-wrap:wrap;align-items:center;gap:4px;margin-top:4px">
              <button type="button" id="vp-bd-left-{{.Name}}"   title="{{t $.Lang "Левая"}}"  style="width:30px;padding:4px 0;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;background:#fff;color:#334155" onclick="ldToggleBorderSide('{{.Name}}','left')">{{t $.Lang "Л"}}</button>
              <button type="button" id="vp-bd-top-{{.Name}}"    title="{{t $.Lang "Верхняя"}}" style="width:30px;padding:4px 0;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;background:#fff;color:#334155" onclick="ldToggleBorderSide('{{.Name}}','top')">{{t $.Lang "В"}}</button>
              <button type="button" id="vp-bd-right-{{.Name}}"  title="{{t $.Lang "Правая"}}" style="width:30px;padding:4px 0;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;background:#fff;color:#334155" onclick="ldToggleBorderSide('{{.Name}}','right')">{{t $.Lang "П"}}</button>
              <button type="button" id="vp-bd-bottom-{{.Name}}" title="{{t $.Lang "Нижняя"}}" style="width:30px;padding:4px 0;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;background:#fff;color:#334155" onclick="ldToggleBorderSide('{{.Name}}','bottom')">{{t $.Lang "Н"}}</button>
              <select id="vp-bw-{{.Name}}" title="{{t $.Lang "Толщина"}}" style="padding:3px;margin-left:4px">
                <option value="thin">{{t $.Lang "Тонкая"}}</option><option value="medium">{{t $.Lang "Средняя"}}</option><option value="thick">{{t $.Lang "Толстая"}}</option>
              </select>
              <span style="width:1px;background:#d1d5db;align-self:stretch;margin:0 2px"></span>
              <button type="button" style="font-size:11px;padding:4px 8px;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;background:#fff" onclick="ldBorderPreset('{{.Name}}','all')">{{t $.Lang "Все"}}</button>
              <button type="button" style="font-size:11px;padding:4px 8px;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;background:#fff" onclick="ldBorderPreset('{{.Name}}','none')">{{t $.Lang "Нет"}}</button>
              <button type="button" style="font-size:11px;padding:4px 8px;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;background:#fff" onclick="ldBorderGridArea('{{.Name}}')">{{t $.Lang "Сетка области"}}</button>
            </div>
          </div>
          <div><label>{{t $.Lang "Границы"}} ({{t $.Lang "пресет"}})</label><br>
            <select id="vp-border-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','border',this.value)">
              <option value="">{{t $.Lang "По умолчанию"}}</option><option value="none">{{t $.Lang "Нет"}}</option><option value="thin">{{t $.Lang "Тонкая"}}</option><option value="all">{{t $.Lang "Все"}}</option><option value="thick">{{t $.Lang "Толстая"}}</option>
            </select>
          </div>
          <div><label>{{t $.Lang "Цвет границы"}}</label><br><input type="color" id="vp-bc-{{.Name}}" style="width:100%;height:28px" oninput="updateCellProp('{{.Name}}','borderColor',this.value)"></div>
          <div></div>
          <div><label>{{t $.Lang "Объединить"}} (colspan)</label><br><input type="number" id="vp-colspan-{{.Name}}" min="1" max="20" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','colspan',parseInt(this.value)||0)"></div>
          <div><label>{{t $.Lang "Объединить"}} (rowspan)</label><br><input type="number" id="vp-rowspan-{{.Name}}" min="1" max="20" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','rowspan',parseInt(this.value)||0)"></div>
          <div></div>
        </div>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
      </div>
    </form>
  </div>
  {{end}}
  {{end}}

  {{/* Subsystems */}}
  {{range $sub := .Subsystems}}
  <div class="cfg-panel" id="sub-{{$sub.Name}}">
    <div class="panel-title">🗂 {{$sub.Title}}</div>
    <div class="panel-kind">{{t $.Lang "Подсистема"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/subsystem" data-home-key="sub-{{$sub.Name}}">
      <input type="hidden" name="subsystem_name" value="{{$sub.Name}}">
      <div class="fg" style="margin-top:12px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="title" value="{{$sub.Title}}" placeholder="{{t $.Lang "Название подсистемы"}}">
      </div>
      {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" $sub.Titles)}}{{end}}
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Иконка"}} <a href="https://lucide.dev/icons" target="_blank" rel="noopener" class="icon-help">lucide.dev ↗</a></label>
        <div class="icon-field">
          <input type="text" name="icon" value="{{$sub.Icon}}" placeholder="shopping-cart" list="lucide-icons" data-icon-preview>
          <span class="icon-preview" aria-hidden="true">{{lucideIcon $sub.Icon}}</span>
        </div>
      </div>
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Порядок"}}</label>
        <input type="number" name="order" value="{{$sub.Order}}" style="width:100px">
      </div>

      <div class="section-hd" style="margin-top:14px">{{t $.Lang "Состав подсистемы"}}</div>
      {{if $.Catalogs}}
      <div style="margin-top:6px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Справочники"}}</span></div>
      {{range $e := $.Catalogs}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="catalogs" value="{{$e.Name}}" {{range $sub.Contents.Catalogs}}{{if eq . $e.Name}}checked{{end}}{{end}}>
        {{$e.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.Docs}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Документы"}}</span></div>
      {{range $e := $.Docs}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="documents" value="{{$e.Name}}" {{range $sub.Contents.Documents}}{{if eq . $e.Name}}checked{{end}}{{end}}>
        {{$e.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.Registers}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Регистры накопления"}}</span></div>
      {{range $r := $.Registers}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="registers" value="{{$r.Name}}" {{range $sub.Contents.Registers}}{{if eq . $r.Name}}checked{{end}}{{end}}>
        {{$r.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.InfoRegisters}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Регистры сведений"}}</span></div>
      {{range $r := $.InfoRegisters}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="inforegs" value="{{$r.Name}}" {{range $sub.Contents.InfoRegs}}{{if eq . $r.Name}}checked{{end}}{{end}}>
        {{$r.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.Reports}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Отчёты"}}</span></div>
      {{range $r := $.Reports}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="reports" value="{{$r.Name}}" {{range $sub.Contents.Reports}}{{if eq . $r.Name}}checked{{end}}{{end}}>
        {{if $r.Title}}{{$r.Title}}{{else}}{{$r.Name}}{{end}}
      </label>
      {{end}}
      {{end}}
      {{if $.Processors}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Обработки"}}</span></div>
      {{range $p := $.Processors}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="processors" value="{{$p.Name}}" {{range $sub.Contents.Processors}}{{if eq . $p.Name}}checked{{end}}{{end}}>
        {{if $p.Title}}{{$p.Title}}{{else}}{{$p.Name}}{{end}}
      </label>
      {{end}}
      {{end}}

      <div class="section-hd" style="margin-top:14px">{{t $.Lang "Рабочий стол подсистемы"}}</div>
      {{if $.Widgets}}
      <div class="fg" style="margin:6px 0;display:flex;align-items:center;gap:8px;flex-wrap:wrap">
        <label style="font-size:12px;color:#555">{{t $.Lang "Раскладка"}}</label>
        <select name="home_layout" style="width:180px" onchange="obToggleLayout(this)">
          <option value="auto" {{if ne $sub.HomeLayout "rows"}}selected{{end}}>{{t $.Lang "Авто (по ширине)"}}</option>
          <option value="rows" {{if eq $sub.HomeLayout "rows"}}selected{{end}}>{{t $.Lang "По рядам"}}</option>
        </select>
      </div>
      {{template "home-layout-editor" dict "Widgets" $.Widgets "Selected" $sub.HomeWidgets "Layout" $sub.HomeLayout "Lang" $.Lang "AutoHint" (t $.Lang "Отметьте виджеты для рабочего стола подсистемы")}}
      <script>window.__homeData=window.__homeData||{};window.__homeData[{{js (printf "sub-%s" $sub.Name)}}]={rows:{{js $sub.HomeRows}}};window.__homeWidgets={{js $.WidgetOptions}};</script>
      {{else}}
      <div style="font-size:12px;color:#94a3b8;margin:6px 0">{{t $.Lang "Виджеты не созданы"}}</div>
      {{end}}

      <div class="module-save-row" style="margin-top:14px">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity $sub.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Widgets */}}
  {{range .Widgets}}
  <div class="cfg-panel" id="wdg-{{.Name}}">
    <div class="panel-title">🧩 {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Виджет дашборда"}} · {{t $.Lang "тип"}} <b>{{.Type}}</b></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/widget">
      <input type="hidden" name="widget_name" value="{{.Name}}">
      <div style="font-size:12px;color:#64748b;margin:6px 0">
        {{t $.Lang "Поля виджета"}}: <code>name</code>, <code>type</code> (kpi/list/chart/actions/recent), <code>title</code>, <code>query</code>, <code>params</code>.
        Для графиков: <code>chart_kind</code> (bar/line/pie), <code>x_field</code> (ось X), <code>y_fields</code> (серии).
        Шаблоны параметров записывайте как <code>&#123;&#123;today|start_of_month&#125;&#125;</code> или <code>&#123;&#123;today|minus_days:30&#125;&#125;</code>.
      </div>
      <div class="wdg-map" id="wdg-ctl-{{.Name}}" style="display:none">
        <div class="row"><label>{{t $.Lang "Тип графика"}}</label>
          <select id="wdg-kind-{{.Name}}" onchange="applyWidgetMapping('{{.Name}}')">
            <option value="bar">столбцы (bar)</option>
            <option value="line">линии (line)</option>
            <option value="pie">круг (pie)</option>
          </select>
        </div>
        <div class="row"><label>{{t $.Lang "Ось X"}}</label><select id="wdg-x-{{.Name}}" onchange="applyWidgetMapping('{{.Name}}')"></select></div>
        <div class="row"><label>{{t $.Lang "Серии"}}</label><span id="wdg-y-{{.Name}}"></span></div>
        <div class="hint">{{t $.Lang "Заполняется из колонок запроса после предпросмотра; изменение правит YAML ниже."}}</div>
      </div>
      <div class="code-wrap">
        <textarea name="yaml" id="ta-wdg-{{.Name}}" style="width:100%;height:380px;min-height:120px;resize:both;font-family:Consolas,monospace;font-size:12px;border:1px solid #ccd0d8;border-radius:4px;padding:8px;tab-size:2">{{.YAML}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="previewWidget('{{.Name}}')">▶ {{t $.Lang "Предпросмотр"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('widget','wdg-{{.Name}}','{{.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-wdg-{{.Name}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
      <div class="wdg-preview" id="wdg-preview-{{.Name}}" style="display:none"></div>
    </form>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/widget-delete" style="margin-top:8px" onsubmit="return confirm('Удалить виджет {{.Name}}?')">
      <input type="hidden" name="widget_name" value="{{.Name}}">
      <button type="submit" style="background:none;border:1px solid #d8dde8;color:#c00;padding:4px 10px;font-size:11px;border-radius:3px;cursor:pointer">{{t $.Lang "Удалить виджет"}}</button>
    </form>
  </div>
  {{end}}

  {{/* Pages (план 66): метаданные pages/*.yaml + обработчик src/*.page.os */}}
  {{range .Pages}}
  <div class="cfg-panel" id="page-{{.Name}}">
    <div class="panel-title">📄 {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Страница"}} · <code>/ui/page/{{.Name}}</code></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/page">
      <input type="hidden" name="page_name" value="{{.Name}}">
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="title" value="{{.Title}}" placeholder="{{.Name}}">
      </div>
      {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" .Titles)}}{{end}}
      <div class="fg">
        <label>{{t $.Lang "Иконка"}} <a href="https://lucide.dev/icons" target="_blank" rel="noopener" class="icon-help">lucide.dev ↗</a></label>
        <div class="icon-field">
          <input type="text" name="icon" value="{{.Icon}}" placeholder="layout-dashboard" list="lucide-icons" data-icon-preview>
          <span class="icon-preview" aria-hidden="true">{{lucideIcon .Icon}}</span>
        </div>
      </div>
      <div class="fg">
        <label>{{t $.Lang "Роли"}} <span style="color:#94a3b8;font-weight:400">({{t $.Lang "через запятую; пусто — всем"}})</span></label>
        <input type="text" name="roles" value="{{range $i, $r := .Roles}}{{if $i}}, {{end}}{{$r}}{{end}}" placeholder="Менеджер, Бухгалтер">
      </div>
      <div class="fg">
        <label>{{t $.Lang "Параметры"}} <span style="color:#94a3b8;font-weight:400">({{t $.Lang "имена через запятую (?имя=…)"}})</span></label>
        <input type="text" name="params" value="{{range $i, $p := .Params}}{{if $i}}, {{end}}{{$p}}{{end}}" placeholder="период, склад">
      </div>
      <details open><summary class="section-hd" style="cursor:pointer;margin-top:12px">{{t $.Lang "Обработчик"}} (<code>{{.Name}}.page.os</code>, {{t $.Lang "Процедура ПриФормировании"}}) <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></summary>
      <div class="code-wrap">
        <pre class="os-code" id="pre-page-{{.Name}}" onclick="startEdit('page-{{.Name}}')">{{if .Source}}{{.Source}}{{else}}Процедура ПриФормировании(Страница, Параметры) Экспорт&#10;    Страница.Заголовок("{{.Name}}");&#10;КонецПроцедуры{{end}}</pre>
        <textarea class="os-edit" id="ta-page-{{.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('page-{{.Name}}')">{{.Source}}</textarea>
      </div>
      </details>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','page-{{.Name}}','{{.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-page-{{.Name}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/page-delete" style="margin-top:8px" onsubmit="return confirm('Удалить страницу {{.Name}}?')">
      <input type="hidden" name="page_name" value="{{.Name}}">
      <button type="submit" style="background:none;border:1px solid #d8dde8;color:#c00;padding:4px 10px;font-size:11px;border-radius:3px;cursor:pointer">{{t $.Lang "Удалить страницу"}}</button>
    </form>
  </div>
  {{end}}

  {{/* Home page */}}
  <div class="cfg-panel" id="home-page">
    <div class="panel-title">🏠 {{t $.Lang "Главная страница"}}</div>
    <div class="panel-kind">{{t $.Lang "Раскладка стартового дашборда"}} (<code>config/home_page.yaml</code>)</div>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/home-page" data-home-key="home">
      <div class="fg" style="margin-top:12px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="home_title" value="{{.GlobalHome.Title}}" placeholder="{{t $.Lang "Главная"}}">
      </div>
      {{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" .GlobalHome.Titles)}}{{end}}
      {{if .Widgets}}
      <div class="fg" style="margin:6px 0;display:flex;align-items:center;gap:8px;flex-wrap:wrap">
        <label style="font-size:12px;color:#555">{{t $.Lang "Раскладка"}}</label>
        <select name="home_layout" style="width:180px" onchange="obToggleLayout(this)">
          <option value="auto" {{if ne .GlobalHome.Layout "rows"}}selected{{end}}>{{t $.Lang "Авто (по ширине)"}}</option>
          <option value="rows" {{if eq .GlobalHome.Layout "rows"}}selected{{end}}>{{t $.Lang "По рядам"}}</option>
        </select>
      </div>
      {{template "home-layout-editor" dict "Widgets" .Widgets "Selected" .GlobalHome.Widgets "Layout" .GlobalHome.Layout "Lang" .Lang "AutoHint" (t .Lang "Отметьте виджеты для главной страницы")}}
      <script>window.__homeData=window.__homeData||{};window.__homeData["home"]={rows:{{js .GlobalHome.Rows}}};window.__homeWidgets={{js .WidgetOptions}};</script>
      {{else}}
      <div style="font-size:12px;color:#94a3b8;margin:6px 0">{{t $.Lang "пока ни одного — создайте через «+» в дереве «Виджеты»"}}</div>
      {{end}}
      <div class="module-save-row" style="margin-top:14px">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and .FieldsSaved (eq .FieldsSavedEntity "home-page")}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>

    <details style="margin-top:16px">
      <summary style="cursor:pointer;font-size:12px;color:#64748b;font-weight:600">{{t $.Lang "Расширенно (YAML)"}}</summary>
      <form method="POST" action="/bases/{{.Base.ID}}/configurator/home-page-yaml" style="margin-top:8px">
        <div style="font-size:12px;color:#64748b;margin:6px 0">
          Поля: <code>title</code>, <code>layout</code> (<code>auto</code> / <code>rows</code> / <code>grid</code>), <code>rows[].widgets</code>, <code>titles</code>.
          Если файл пуст, на главной показываются все зарегистрированные виджеты.
        </div>
        <div class="code-wrap">
          <textarea name="yaml" id="ta-home-page" style="width:100%;height:260px;min-height:120px;resize:both;font-family:Consolas,monospace;font-size:12px;border:1px solid #ccd0d8;border-radius:4px;padding:8px;tab-size:2">{{.HomePageYAML}}</textarea>
        </div>
        <div class="module-save-row">
          <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
          <button type="button" class="btn-check" onclick="runCheck('home_page','home-page','home_page')">{{t $.Lang "Проверить"}}</button>
          <span class="check-result" id="check-home-page"></span>
        </div>
      </form>
    </details>
  </div>

</div>{{/* cfg-right */}}
</div>{{/* cfg-split */}}
{{end}}

{{/* home-layout-editor — общий редактор раскладки рабочего стола: пейн «Авто»
   (галочки) и пейн «По рядам» (drag-конструктор). Параметры (dict): Widgets,
   Selected (отмеченные имена), Layout ("auto"|"rows"), Lang, AutoHint. */}}
{{define "home-layout-editor"}}
<div class="ob-auto"{{if eq .Layout "rows"}} style="display:none"{{end}}>
  <div style="font-size:12px;color:#64748b;margin:6px 0">{{.AutoHint}}</div>
  {{$sel := .Selected}}
  {{range $w := .Widgets}}
  <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
    <input type="checkbox" name="home_widgets" value="{{$w.Name}}" {{range $sel}}{{if eq . $w.Name}}checked{{end}}{{end}}>
    {{if $w.Title}}{{$w.Title}} <span style="color:#94a3b8">({{$w.Name}})</span>{{else}}{{$w.Name}}{{end}}
  </label>
  {{end}}
</div>
<div class="ob-rows-pane"{{if ne .Layout "rows"}} style="display:none"{{end}}>
  <input type="hidden" name="home_rows">
  <div style="font-size:12px;color:#64748b;margin:6px 0">{{t $.Lang "Перетащите виджеты в ряды. «+ Ряд» добавляет новый ряд."}}</div>
  <div class="ob-rows"></div>
  <button type="button" class="ob-add-row" style="margin:6px 0;background:none;border:1px dashed #c3c9d4;color:#475569;padding:5px 12px;font-size:12px;border-radius:5px;cursor:pointer">+ {{t $.Lang "Ряд"}}</button>
  <div style="font-size:11px;color:#94a3b8;margin:8px 0 4px">{{t $.Lang "Доступные виджеты"}}</div>
  <div class="ob-pool ob-zone"></div>
</div>
{{end}}

{{define "entity-detail"}}
{{$e := .Entity}}
{{$baseID := .BaseID}}
{{$allEntities := .AllEntityNames}}
{{$allEnums := .AllEnumNames}}
{{$fSaved := .FieldsSaved}}
{{$fSavedEnt := .FieldsSavedEntity}}
{{$lang := .Lang}}
{{$availLangs := .AvailableLangs}}

<div class="obj-editor">
  <div class="obj-tabs">
    <div class="obj-tab active" onclick="cfgObjTab(this,'ot-data-{{$e.Name}}')">{{t $.Lang "Данные"}}</div>
    <div class="obj-tab" onclick="cfgObjTab(this,'ot-forms-{{$e.Name}}')">{{t $.Lang "Формы"}}</div>
    <div class="obj-tab" onclick="cfgObjTab(this,'ot-print-{{$e.Name}}')">{{t $.Lang "Печатные формы"}}</div>
    <div class="obj-tab" onclick="cfgObjTab(this,'ot-modules-{{$e.Name}}')">{{t $.Lang "Модули"}}</div>
  </div>

  <div class="obj-pane active" id="ot-data-{{$e.Name}}">

<form method="POST" action="/bases/{{$baseID}}/configurator/fields">
<input type="hidden" name="entity" value="{{$e.Name}}">
<input type="hidden" name="entity_kind" value="{{$e.Kind}}">
{{range $e.TableParts}}<input type="hidden" name="tp_names" value="{{.Name}}">{{end}}
{{if $availLangs}}{{template "titles-block" (dict "Lang" $lang "Langs" $availLangs "Prefix" "titles" "Values" $e.Titles)}}{{end}}

{{if eq $e.Kind "Документ"}}
<div class="section-hd">{{t $.Lang "Свойства"}}</div>
<div style="margin-bottom:10px">
  <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
    <input type="checkbox" name="posting" value="true" {{if $e.Posting}}checked{{end}}>
    <span>{{t $.Lang "Проводится — поддержка кнопки «Провести» и обработки проведения"}}</span>
  </label>
</div>
{{end}}

{{/* Ввод на основании (Plan 38): доступен и для документов, и для
     справочников. Маркер based_on_present=1 нужен, чтобы POST-handler мог
     отличить «секция вообще не пришла» от «все чекбоксы сняты» — без него
     based_on невозможно было бы очистить через UI. */}}
<details {{if $e.BasedOn}}open{{end}} style="margin-bottom:10px">
<summary class="section-hd" style="cursor:pointer">{{t $.Lang "Ввод на основании"}}{{if $e.BasedOn}} ({{len $e.BasedOn}}){{end}}</summary>
<input type="hidden" name="based_on_present" value="1">
<div style="font-size:12px;color:#475569;margin:6px 0">{{t $.Lang "Объекты, на основании которых можно вводить эту сущность. При создании появится кнопка «Ввести на основании ▾» в форме источника."}}</div>
<div style="display:flex;flex-wrap:wrap;gap:6px 14px;font-size:13px">
  {{range $allEntities}}{{if ne . $e.Name}}{{$entName := .}}
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer">
    <input type="checkbox" name="based_on" value="{{$entName}}"
      {{range $b := $e.BasedOn}}{{if eq $b $entName}}checked{{end}}{{end}}>
    <span>{{$entName}}</span>
  </label>
  {{end}}{{end}}
</div>
{{if $e.Receivers}}
<div style="margin-top:10px;padding-top:8px;border-top:1px dashed #e2e8f0">
  <div style="font-size:12px;color:#475569;margin-bottom:4px">{{t $.Lang "На основании этой сущности вводятся:"}}</div>
  <div style="font-size:13px">{{range $i, $r := $e.Receivers}}{{if $i}}, {{end}}<code>{{$r}}</code>{{end}}</div>
  <div style="font-size:11px;color:#94a3b8;margin-top:4px">{{t $.Lang "Это обратный список: исходное based_on хранится у этих сущностей. Чтобы изменить — откройте соответствующий объект."}}</div>
</div>
{{end}}
</details>

{{if eq $e.Kind "Справочник"}}
<div class="section-hd">Свойства</div>
<div style="margin-bottom:10px">
  <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
    <input type="checkbox" name="hierarchical" value="true" {{if $e.Hierarchical}}checked{{end}}>
    <span>Иерархический — поддержка групп (папок) и режима «Дерево / Список»</span>
  </label>
  <div style="color:#94a3b8;font-size:11px;margin-left:24px;margin-top:2px">
    После включения требуется миграция БД: появятся колонки <code>is_folder</code> и <code>parent_id</code>.
  </div>
</div>
<details {{if $e.Activity}}open{{end}} style="margin-bottom:10px">
  <summary class="section-hd" style="cursor:pointer">Активность</summary>
  <input type="hidden" name="activity_present" value="1">
  <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer;margin:6px 0">
    <input type="checkbox" name="activity_enabled" value="1" {{if $e.Activity}}checked{{end}}>
    <span>Использовать скрытие элементов справочника</span>
  </label>
  <div style="display:grid;grid-template-columns:minmax(140px,180px) minmax(180px,280px);gap:8px 12px;align-items:center;font-size:12px;margin-left:24px">
    <label style="color:#475569">Реквизит активности</label>
    <select name="activity_field" style="padding:5px 6px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
      <option value="">— выбрать bool-реквизит —</option>
      {{range $f := $e.Fields}}{{if eq $f.Type "bool"}}
      <option value="{{$f.Name}}" {{if and $e.Activity (eq $e.Activity.Field $f.Name)}}selected{{end}}>{{$f.Name}}</option>
      {{end}}{{end}}
    </select>
    <label style="color:#475569">Список по умолчанию</label>
    <select name="activity_default_scope" style="padding:5px 6px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
      <option value="active" {{if or (not $e.Activity) (eq $e.Activity.DefaultScope "active")}}selected{{end}}>Активные</option>
      <option value="all" {{if and $e.Activity (eq $e.Activity.DefaultScope "all")}}selected{{end}}>Все</option>
    </select>
    <span></span>
    <label style="display:flex;align-items:center;gap:7px;cursor:pointer">
      <input type="checkbox" name="activity_hide_from_choice" value="1" {{if or (not $e.Activity) $e.Activity.HideFromChoice}}checked{{end}}>
      <span>Скрывать неактивные из выбора ссылок</span>
    </label>
  </div>
  <div style="color:#94a3b8;font-size:11px;margin-left:24px;margin-top:6px">
    В пользовательском режиме появятся фильтры «Активные / Скрытые / Все». Неактивные элементы не удаляются и остаются доступными в отчётах и отборах.
  </div>
</details>
{{end}}

{{if $e.Fields}}
<details open><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Реквизиты"}} ({{len $e.Fields}})</summary>
<table class="fields-tbl" id="ft-{{$e.Name}}">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th><th title="{{t $.Lang "Кнопка «+ Создать» в picker'е для ссылочного поля. По умолчанию включена для шапки документа."}}">{{t $.Lang "+ в picker'е"}}</th></tr>
{{range $i, $f := $e.Fields}}
<input type="hidden" name="field.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="field.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$e.Name}}-f{{$i}}');cfgToggleNum(this,'cfn-{{$e.Name}}-f{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
      <option value="enum"      {{if eq $f.Type "enum"}}selected{{end}}>{{t $.Lang "перечисление →"}}</option>
    </select>
    <span id="cfn-{{$e.Name}}-f{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
      <input type="number" min="1" name="field.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
      , <input type="number" min="0" name="field.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
    </span>
  </td>
  <td>
    <select name="field.{{$i}}.ref" id="cfr-{{$e.Name}}-f{{$i}}"{{if and (ne $f.Type "reference") (ne $f.Type "enum")}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{if eq $f.Type "enum"}}
        {{range $allEnums}}<option value="{{.}}"{{if eq . $f.EnumName}} selected{{end}}>{{.}}</option>{{end}}
      {{else}}
        {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
      {{end}}
    </select>
  </td>
  <td style="text-align:center">
    {{if eq $f.Type "reference"}}
    <input type="hidden" name="field.{{$i}}.inline_present" value="1">
    <input type="checkbox" name="field.{{$i}}.inline_allow" value="1"{{if $f.InlineAllowChecked false}} checked{{end}} title="{{t $.Lang "Показывать «+ Создать» в picker'е"}}">
    {{end}}
  </td>
</tr>
{{if $availLangs}}<tr><td colspan="4" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $lang "Langs" $availLangs "Prefix" (printf "field.%d.titles" $i) "Values" $f.Titles)}}</td></tr>{{end}}
{{end}}
</table>
<button type="button" onclick="cfgAddField('ft-{{$e.Name}}','new_field','{{$e.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить поле"}}</button>
</details>
{{end}}

{{range $j, $tp := $e.TableParts}}
<details open><summary class="section-hd" style="cursor:pointer">📋 {{$tp.Name}} ({{len $tp.Fields}})</summary>
<div class="tp-block">
<table class="fields-tbl" id="ft-{{$e.Name}}-tp{{$j}}">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th><th title="{{t $.Lang "Кнопка «+ Создать» в picker'е. В ТЧ по умолчанию выключена."}}">{{t $.Lang "+ в picker'е"}}</th></tr>
{{range $i, $f := $tp.Fields}}
<input type="hidden" name="tp.{{$tp.Name}}.field.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="tp.{{$tp.Name}}.field.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$e.Name}}-tp{{$j}}f{{$i}}');cfgToggleNum(this,'cfn-{{$e.Name}}-tp{{$j}}f{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
      <option value="enum"      {{if eq $f.Type "enum"}}selected{{end}}>{{t $.Lang "перечисление →"}}</option>
    </select>
    <span id="cfn-{{$e.Name}}-tp{{$j}}f{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
      <input type="number" min="1" name="tp.{{$tp.Name}}.field.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
      , <input type="number" min="0" name="tp.{{$tp.Name}}.field.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
    </span>
  </td>
  <td>
    <select name="tp.{{$tp.Name}}.field.{{$i}}.ref" id="cfr-{{$e.Name}}-tp{{$j}}f{{$i}}"{{if and (ne $f.Type "reference") (ne $f.Type "enum")}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{if eq $f.Type "enum"}}
        {{range $allEnums}}<option value="{{.}}"{{if eq . $f.EnumName}} selected{{end}}>{{.}}</option>{{end}}
      {{else}}
        {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
      {{end}}
    </select>
  </td>
  <td style="text-align:center">
    {{if eq $f.Type "reference"}}
    <input type="hidden" name="tp.{{$tp.Name}}.field.{{$i}}.inline_present" value="1">
    <input type="checkbox" name="tp.{{$tp.Name}}.field.{{$i}}.inline_allow" value="1"{{if $f.InlineAllowChecked true}} checked{{end}} title="{{t $.Lang "Показывать «+ Создать» в picker'е"}}">
    {{end}}
  </td>
</tr>
{{if $availLangs}}<tr><td colspan="4" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $lang "Langs" $availLangs "Prefix" (printf "tp.%s.field.%d.titles" $tp.Name $i) "Values" $f.Titles)}}</td></tr>{{end}}
{{end}}
</table>
<button type="button" onclick="cfgAddField('ft-{{$e.Name}}-tp{{$j}}','new_tp.{{$tp.Name}}.field','{{$e.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить поле"}}</button>
</div>
</details>
{{end}}

<button type="button" onclick="cfgAddTP(this,'{{$e.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить табличную часть"}}</button>

<div class="module-save-row" style="margin-bottom:14px">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить типы полей"}}</button>
  {{if and $fSaved (eq $fSavedEnt $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>

{{/* Predefined items — only for catalogs */}}
{{if eq $e.Kind "Справочник"}}
<details style="margin-top:18px"><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Предопределённые элементы"}} ({{len $e.Predefined}})</summary>
<form method="POST" action="/bases/{{$baseID}}/configurator/predefined">
<input type="hidden" name="entity" value="{{$e.Name}}">
{{range $e.Fields}}<input type="hidden" name="pre_field_names" value="{{.Name}}">{{end}}
<div style="font-size:11px;color:#64748b;margin-bottom:8px">Элементы, которые всегда присутствуют в справочнике. Имя — программный идентификатор (без пробелов).</div>
<table class="fields-tbl" id="pre-tbl-{{$e.Name}}">
<tr><th>{{t $.Lang "Имя"}}</th>{{range $e.Fields}}<th>{{.Name}}</th>{{end}}</tr>
{{range $i, $pd := $e.Predefined}}
<tr>
  <td><input type="text" name="pre.{{$i}}.name" value="{{$pd.Name}}" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>
  {{range $e.Fields}}<td><input type="text" name="pre.{{$i}}.field.{{.Name}}" value="{{index $pd.Fields .Name}}" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>{{end}}
</tr>
{{end}}
</table>
<button type="button" onclick="cfgAddPreRow('pre-tbl-{{$e.Name}}',{{len $e.Fields}})" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:6px 0">+ {{t $.Lang "Добавить элемент"}}</button>
<div class="module-save-row" style="margin-bottom:8px;margin-top:6px">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить предопределённые"}}</button>
  {{if and $fSaved (eq $fSavedEnt $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>
</details>
{{end}}

  </div>{{/* end ot-data */}}

  <div class="obj-pane" id="ot-forms-{{$e.Name}}">

<form method="POST" action="/bases/{{$baseID}}/configurator/form">
<input type="hidden" name="entity" value="{{$e.Name}}">

<div class="module-tabs" style="margin-top:8px">
  <div class="module-tab active" onclick="formTab(this,'fl-{{$e.Name}}','fe-{{$e.Name}}')">📋 {{t $.Lang "Форма списка"}}</div>
  <div class="module-tab" onclick="formTab(this,'fe-{{$e.Name}}','fl-{{$e.Name}}')">📄 {{t $.Lang "Форма элемента"}}</div>
</div>

{{/* List form fields */}}
<div class="module-pane active" id="fl-{{$e.Name}}" style="padding:10px 0">
<p style="font-size:11px;color:#64748b;margin-bottom:8px">{{t $.Lang "Выберите поля, отображаемые в списке. Порядок строк = порядок колонок."}}</p>
<div id="fl-sort-{{$e.Name}}">
{{range $i, $f := $e.Fields}}
<div class="form-field-row" style="display:flex;align-items:center;gap:6px;padding:3px 0;font-size:12px">
  <input type="hidden" name="lf.{{$i}}.name" value="{{$f.Name}}">
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer;flex:1">
    <input type="checkbox" name="lf.{{$i}}.vis" value="1" {{if not $f.FormListHidden}}checked{{end}}>
    <span style="color:#1a4a80">{{$f.Name}}</span>
    <span class="ft {{fieldTypeClass $f.Type}}" style="font-size:11px">{{fieldTypeLabel $f.Type $f.RefEntity}}</span>
  </label>
  <button type="button" onclick="moveUp(this)" style="background:none;border:1px solid #e2e8f0;border-radius:3px;padding:1px 6px;cursor:pointer;font-size:11px">↑</button>
  <button type="button" onclick="moveDown(this)" style="background:none;border:1px solid #e2e8f0;border-radius:3px;padding:1px 6px;cursor:pointer;font-size:11px">↓</button>
</div>
{{end}}
</div>
</div>

{{/* Element form fields */}}
<div class="module-pane" id="fe-{{$e.Name}}" style="padding:10px 0">
<p style="font-size:11px;color:#64748b;margin-bottom:8px">{{t $.Lang "Выберите поля, отображаемые в форме элемента."}}</p>
<div id="fe-sort-{{$e.Name}}">
{{range $i, $f := $e.Fields}}
<div class="form-field-row" style="display:flex;align-items:center;gap:6px;padding:3px 0;font-size:12px">
  <input type="hidden" name="ef.{{$i}}.name" value="{{$f.Name}}">
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer;flex:1">
    <input type="checkbox" name="ef.{{$i}}.vis" value="1" {{if not $f.FormItemHidden}}checked{{end}}>
    <span style="color:#1a4a80">{{$f.Name}}</span>
    <span class="ft {{fieldTypeClass $f.Type}}" style="font-size:11px">{{fieldTypeLabel $f.Type $f.RefEntity}}</span>
  </label>
</div>
{{end}}
{{range $j, $tp := $e.TableParts}}
<div style="font-size:11px;font-weight:600;color:#7c3aed;margin:8px 0 2px;padding-left:2px">📋 {{$tp.Name}} ({{t $.Lang "табличная часть"}})</div>
{{range $i, $f := $tp.Fields}}
<div class="form-field-row" style="display:flex;align-items:center;gap:6px;padding:3px 0 3px 16px;font-size:12px">
  <input type="hidden" name="ef.tp{{$j}}.{{$i}}.name" value="tp.{{$tp.Name}}.{{$f.Name}}">
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer;flex:1">
    <input type="checkbox" name="ef.tp{{$j}}.{{$i}}.vis" value="1" checked>
    <span style="color:#1a4a80">{{$f.Name}}</span>
    <span class="ft {{fieldTypeClass $f.Type}}" style="font-size:11px">{{fieldTypeLabel $f.Type $f.RefEntity}}</span>
  </label>
</div>
{{end}}
{{end}}
</div>
</div>

<div class="module-save-row">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить формы"}}</button>
  {{if and $fSaved (eq $fSavedEnt $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>

{{/* ── Управляемые формы (план 37, этап 4) ────────────────────────────── */}}
<div style="background:#f8fafc;border:1px dashed #c8d4f0;border-radius:6px;padding:12px 14px;font-size:12px;color:#475569;line-height:1.5">
  <p style="margin:0 0 8px">
    Управляемая форма — декларативное описание UI в YAML, переопределяющее
    авто-форму выше. Поддерживает группы, страницы-закладки, реквизиты формы,
    события и обработчики на DSL OneBase. Подробнее: <a href="https://github.com/ivanarama/onebase/blob/main/docs/forms.md" target="_blank" style="color:#1a4a80">docs/forms.md</a>.
  </p>
  {{$mine := filterFormsByEntity $.ManagedForms $e.Name}}
  {{if $mine}}
  <table style="width:100%;border-collapse:collapse;margin:8px 0;font-size:12px">
    <thead><tr style="background:#fff;border-bottom:1px solid #e2e8f0">
      <th style="text-align:left;padding:4px 8px">Имя</th>
      <th style="text-align:left;padding:4px 8px">Тип</th>
      <th style="text-align:left;padding:4px 8px">Модуль</th>
      <th></th>
    </tr></thead>
    <tbody>
    {{range $mine}}
    <tr style="border-bottom:1px solid #eef0f5">
      <td style="padding:6px 8px">◇ {{formLabel .Name}}</td>
      <td style="padding:6px 8px">{{if .Kind}}{{.Kind}}{{else}}—{{end}}</td>
      <td style="padding:6px 8px">{{if .HasOS}}есть{{else}}—{{end}}</td>
      <td style="text-align:right;padding:6px 8px">
        <a href="/bases/{{$baseID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}&from=e-{{$e.Name}}"
           style="display:inline-block;padding:3px 10px;background:#1a4a80;color:#fff;text-decoration:none;border-radius:4px;font-size:11px">
          Редактировать
        </a>
      </td>
    </tr>
    {{end}}
    </tbody>
  </table>
  <p style="margin:4px 0 0;color:#64748b;font-size:11px">
    В пользовательском режиме карточка <b>{{$e.Name}}</b> будет рендериться по
    YAML с маркером ◇ managed, а не авто-форме выше.
  </p>
  {{else}}
  <p style="margin:0 0 10px">У сущности <b>{{$e.Name}}</b> нет управляемых форм. Авто-форма выше используется по умолчанию.</p>
  {{end}}
  <div style="margin-top:10px;display:flex;gap:6px;flex-wrap:wrap;align-items:center">
    <a href="/bases/{{$baseID}}/configurator/forms/edit?entity={{$e.Name}}&name=ФормаОбъекта&from=e-{{$e.Name}}"
       style="display:inline-block;padding:5px 12px;background:#16a34a;color:#fff;text-decoration:none;border-radius:4px;font-size:12px">
      + Форма объекта
    </a>
    <a href="/bases/{{$baseID}}/configurator/forms/edit?entity={{$e.Name}}&name=ФормаСписка&from=e-{{$e.Name}}"
       style="display:inline-block;padding:5px 12px;background:#16a34a;color:#fff;text-decoration:none;border-radius:4px;font-size:12px">
      + Форма списка
    </a>
    <a href="/bases/{{$baseID}}/configurator/forms"
       style="display:inline-block;padding:5px 12px;background:#e2e8f0;color:#334155;text-decoration:none;border-radius:4px;font-size:12px">
      Все формы / Импорт из 1С
    </a>
  </div>
</div>

  </div>{{/* end ot-forms */}}

  <div class="obj-pane" id="ot-print-{{$e.Name}}">
    {{if $e.LinkedPrintForms}}
    <div style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:8px">
      {{range $e.LinkedPrintForms}}
      <a href="#" onclick="cfgSelectPanel('pf-{{.Name}}');return false"
         style="display:inline-flex;align-items:center;gap:5px;padding:5px 12px;background:#f0f4ff;border:1px solid #c8d4f0;border-radius:4px;font-size:12px;color:#1a4a80;text-decoration:none">
        🖨 {{.Name}}
      </a>
      {{end}}
    </div>
    {{else}}
    <div style="color:#94a3b8;font-size:12px;padding:8px 0 4px">
      {{t $.Lang "Печатных форм нет."}}
      <a href="#" onclick="cfgNewObj('printform');return false" style="color:#1a4a80">{{t $.Lang "Создать печатную форму"}}</a>
    </div>
    {{end}}
    <div style="margin-top:6px">
      <button type="button" onclick="cfgNewLayout('/bases/{{.BaseID}}/configurator/new-layout','{{$e.Name}}')"
              style="font-size:12px;padding:5px 12px;background:#16a34a;color:#fff;border:none;border-radius:4px;cursor:pointer">
        + {{t $.Lang "Печатная форма (макет)"}}
      </button>
      <button type="button" onclick="cfgImportPdfLayout('/bases/{{.BaseID}}/configurator/layout/import-pdf')"
              style="font-size:12px;padding:5px 12px;background:#0369a1;color:#fff;border:none;border-radius:4px;cursor:pointer;margin-left:6px"
              title="{{t $.Lang "Извлечь черновик макета из PDF (выгрузка 1С/Excel с текстовым слоем)"}}">
        &#x1F4C4; {{t $.Lang "Создать макет из PDF"}}
      </button>
    </div>
  </div>{{/* end ot-print */}}

  <div class="obj-pane" id="ot-modules-{{$e.Name}}">
<div class="module-editor-wrap">
  <div class="module-tabs">
    <div class="module-tab active" onclick="modTab(this,'mp-obj-{{$e.Name}}')">📝 {{t $.Lang "Модуль объекта"}}</div>
    {{if eq $e.Kind "Документ"}}<div class="module-tab" onclick="modTab(this,'mp-post-{{$e.Name}}')">✅ {{t $.Lang "ОбработкаПроведения"}}</div>{{end}}
    <div class="module-tab" onclick="modTab(this,'mp-mgr-{{$e.Name}}')">📋 {{t $.Lang "Модуль менеджера"}}</div>
  </div>

  <div class="module-pane active" id="mp-obj-{{$e.Name}}">
    <form method="POST" action="/bases/{{.BaseID}}/configurator/module">
      <input type="hidden" name="entity" value="{{$e.Name}}">
      <input type="hidden" name="module_type" value="object">
      <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
        <pre class="os-code clickable-code" id="pre-{{$e.Name}}"
             onclick="startEdit('{{$e.Name}}')">{{if $e.Source}}{{$e.Source}}{{else}}// Кликните для редактирования&#10;Процедура ПриЗаписи()&#10;&#10;КонецПроцедуры{{end}}</pre>
        <textarea class="os-edit" id="ta-{{$e.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('{{$e.Name}}')">{{$e.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','{{$e.Name}}','{{$e.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-{{$e.Name}}"></span>
        <span class="edit-hint">✎ {{t $.Lang "кликните на код для редактирования"}}</span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>

  {{if eq $e.Kind "Документ"}}
  <div class="module-pane" id="mp-post-{{$e.Name}}">
    <div style="font-size:11px;color:#64748b;margin-bottom:6px">{{t $.Lang "Процедура"}} <b>{{t $.Lang "ОбработкаПроведения"}}()</b> — {{t $.Lang "вызывается при нажатии «Провести». Активируется флагом"}} <b>{{t $.Lang "Проводится"}}</b> {{t $.Lang "в свойствах документа. Здесь пишите движения регистров."}}</div>
    <form method="POST" action="/bases/{{.BaseID}}/configurator/module">
      <input type="hidden" name="entity" value="{{$e.Name}}">
      <input type="hidden" name="module_type" value="posting">
      <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
        <pre class="os-code clickable-code" id="pre-post-{{$e.Name}}"
             onclick="startEdit('post-{{$e.Name}}')">{{if $e.PostingSource}}{{$e.PostingSource}}{{else}}Процедура ОбработкаПроведения()&#10;  // Движения.ИмяРегистра.Очистить()&#10;  // Дв = Движения.ИмяРегистра.Добавить()&#10;  // Дв.ВидДвижения = "Приход"&#10;  // Дв.Номенклатура = Строка.Номенклатура&#10;  // Дв.Количество = Строка.Количество&#10;КонецПроцедуры{{end}}</pre>
        <textarea class="os-edit" id="ta-post-{{$e.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('post-{{$e.Name}}')">{{$e.PostingSource}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','post-{{$e.Name}}','{{$e.Name}}-ОбработкаПроведения')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-post-{{$e.Name}}"></span>
        <span class="edit-hint">✎ {{t $.Lang "кликните на код для редактирования"}}</span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  <div class="module-pane" id="mp-mgr-{{$e.Name}}">
    <div style="font-size:11px;color:#64748b;margin-bottom:6px">{{t $.Lang "Экспортные процедуры и функции этого модуля вызываются как"}} <b>{{if eq $e.Kind "Документ"}}{{t $.Lang "Документы"}}{{else}}{{t $.Lang "Справочники"}}{{end}}.{{$e.Name}}.{{t $.Lang "Метод"}}(…)</b> — {{t $.Lang "по аналогии с 1С:Предприятие. Здесь размещают функции уровня типа объекта: печать, поиск, сервисные расчёты."}}</div>
    <form method="POST" action="/bases/{{.BaseID}}/configurator/module">
      <input type="hidden" name="entity" value="{{$e.Name}}">
      <input type="hidden" name="module_type" value="manager">
      <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
        <pre class="os-code clickable-code" id="pre-mgr-{{$e.Name}}"
             onclick="startEdit('mgr-{{$e.Name}}')">{{if $e.ManagerSource}}{{$e.ManagerSource}}{{else}}Функция Пример(Параметр)&#10;  Возврат Параметр;&#10;КонецФункции{{end}}</pre>
        <textarea class="os-edit" id="ta-mgr-{{$e.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('mgr-{{$e.Name}}')">{{$e.ManagerSource}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','mgr-{{$e.Name}}','{{$e.Name}}-Менеджер')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-mgr-{{$e.Name}}"></span>
        <span class="edit-hint">✎ {{t $.Lang "кликните на код для редактирования"}}</span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
</div>
  </div>{{/* end ot-modules */}}

</div>{{/* end obj-editor */}}
<script>
// issue #132 фаза 2: «открыть в редакторе» из дерева файлов — выделить объект по
// selectedTreeId надёжно (по готовности DOM, самодостаточно — не зависит от
// автовыбора в configurator.js).
(function(){
  var id=(window.__cfg&&window.__cfg.selectedTreeId)||''; if(!id)return;
  function go(){
    var n=document.querySelector('.cfg-item[data-id="'+String(id).replace(/["\\]/g,'')+'"]');
    if(!n)return;
    var grp=n.closest('details.cfg-tree'); if(grp)grp.open=true;
    if(typeof selItem==='function')selItem(n);
    try{n.scrollIntoView({block:'center'});}catch(e){}
    n.style.outline='2px solid #f59e0b'; setTimeout(function(){n.style.outline='';},1600);
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',go);else go();
})();
</script>
{{end}}`

// ── Register detail (editable) ────────────────────────────────────────────────

const cfgRegDetail = `{{define "register-detail"}}
{{$rg := .Register}}
{{$baseID := .BaseID}}
{{$allEntities := .AllEntityNames}}
{{$fSaved := .FieldsSaved}}
{{$fSavedEnt := .FieldsSavedEntity}}

<form method="POST" action="/bases/{{$baseID}}/configurator/register-fields">
<input type="hidden" name="register" value="{{$rg.Name}}">
{{if $.AvailableLangs}}{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" "titles" "Values" $rg.Titles)}}{{end}}

<div class="section-hd">{{t $.Lang "Измерения"}}</div>
<table class="fields-tbl" id="rg-dim-{{$rg.Name}}">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
{{range $i, $f := $rg.Dimensions}}
<input type="hidden" name="dim.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="dim.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$rg.Name}}-d{{$i}}');cfgToggleNum(this,'cfn-{{$rg.Name}}-d{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
    </select>
    <span id="cfn-{{$rg.Name}}-d{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
      <input type="number" min="1" name="dim.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
      , <input type="number" min="0" name="dim.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
    </span>
  </td>
  <td>
    <select name="dim.{{$i}}.ref" id="cfr-{{$rg.Name}}-d{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{if $.AvailableLangs}}<tr><td colspan="3" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "dim.%d.titles" $i) "Values" $f.Titles)}}</td></tr>{{end}}
{{end}}
</table>
<button type="button" onclick="cfgAddField('rg-dim-{{$rg.Name}}','new_dim','')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить измерение"}}</button>

<div class="section-hd">{{t $.Lang "Ресурсы"}}</div>
<table class="fields-tbl" id="rg-res-{{$rg.Name}}">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
{{range $i, $f := $rg.Resources}}
<input type="hidden" name="res.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="res.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$rg.Name}}-r{{$i}}');cfgToggleNum(this,'cfn-{{$rg.Name}}-r{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
    </select>
    <span id="cfn-{{$rg.Name}}-r{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
      <input type="number" min="1" name="res.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
      , <input type="number" min="0" name="res.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
    </span>
  </td>
  <td>
    <select name="res.{{$i}}.ref" id="cfr-{{$rg.Name}}-r{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{if $.AvailableLangs}}<tr><td colspan="3" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "res.%d.titles" $i) "Values" $f.Titles)}}</td></tr>{{end}}
{{end}}
</table>
<button type="button" onclick="cfgAddField('rg-res-{{$rg.Name}}','new_res','')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить ресурс"}}</button>

<div class="section-hd">{{t $.Lang "Реквизиты"}}</div>
<table class="fields-tbl" id="rg-attr-{{$rg.Name}}">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
{{range $i, $f := $rg.Attributes}}
<input type="hidden" name="attr.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="attr.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$rg.Name}}-a{{$i}}');cfgToggleNum(this,'cfn-{{$rg.Name}}-a{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
    </select>
    <span id="cfn-{{$rg.Name}}-a{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
      <input type="number" min="1" name="attr.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
      , <input type="number" min="0" name="attr.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
    </span>
  </td>
  <td>
    <select name="attr.{{$i}}.ref" id="cfr-{{$rg.Name}}-a{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{if $.AvailableLangs}}<tr><td colspan="3" style="padding:0 0 4px">{{template "titles-block" (dict "Lang" $.Lang "Langs" $.AvailableLangs "Prefix" (printf "attr.%d.titles" $i) "Values" $f.Titles)}}</td></tr>{{end}}
{{end}}
</table>
<button type="button" onclick="cfgAddField('rg-attr-{{$rg.Name}}','new_attr','')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить реквизит"}}</button>

<div class="module-save-row" style="margin-bottom:14px">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить типы полей"}}</button>
  {{if and $fSaved (eq $fSavedEnt $rg.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>
{{end}}`
