package ui

// tplManagedForm — шаблон рендеринга «управляемой формы» из FormModule
// (план 37, этап 3). В отличие от tplForm, который автоматически выводит
// все поля Entity подряд, этот шаблон обходит дерево FormModule.Elements
// и отрисовывает каждый элемент по его Kind: ГруппаФормы → fieldset,
// СтраницыФормы → tabs, ПолеВвода → input/select (зависит от типа поля),
// и т.д.
//
// data_path вида "Объект.Контрагент" мапится на поле объекта по имени
// "Контрагент" (отбрасываем префикс "Объект."). Префикс "Список." и другие
// реквизиты формы — пока игнорируются (заглушка), будут добавлены позже.
//
// Опциональность: managed-форма выбирается в handlers.go только если в
// Entity.Forms есть FormModule с IsManaged()==true и подходящим Kind.
// Иначе работает старая авто-форма (tplForm) — backward-compat.
const tplManagedForm = `
{{define "managed-element"}}
{{$el := .El}}{{$ctx := .Ctx}}
{{if eq (str $el.Kind) "ГруппаФормы"}}
  <fieldset class="form-group-box{{if eq $el.Orientation "horizontal"}} managed-group-horizontal{{end}}" style="border:1px solid #e2e8f0;border-radius:8px;padding:12px 14px;margin-bottom:14px">
    {{if $el.TitleMap}}<legend style="font-weight:600;color:#475569;padding:0 6px;font-size:13px">{{fieldTitleRU $el.TitleMap $el.Name}}</legend>{{end}}
    <div class="managed-group-body">
      {{range $el.Children}}{{template "managed-element" (dict "El" . "Ctx" $ctx)}}{{end}}
    </div>
  </fieldset>
{{else if eq (str $el.Kind) "СтраницыФормы"}}
  {{/* CSS активной вкладки вынесен в стиль managed-форм (см. в конце шаблона)
       чтобы inline-style не побеждал .active по приоритету. */}}
  <div class="managed-tabs" data-tabs="{{$el.Name}}">
    <div class="managed-tab-headers" style="display:flex;gap:2px;border-bottom:2px solid #e2e8f0;margin-bottom:12px">
      {{range $i, $page := $el.Children}}
        {{if eq (str $page.Kind) "Страница"}}
        <button type="button" class="managed-tab-btn{{if eq $i 0}} active{{end}}" data-tab-idx="{{$i}}"
          onclick="this.closest('.managed-tabs').querySelectorAll('.managed-tab-btn').forEach(b=>b.classList.remove('active'));this.classList.add('active');this.closest('.managed-tabs').querySelectorAll('.managed-tab-content').forEach(c=>c.style.display='none');this.closest('.managed-tabs').querySelectorAll('.managed-tab-content')[{{$i}}].style.display='block'">
          {{fieldTitleRU $page.TitleMap $page.Name}}
        </button>
        {{end}}
      {{end}}
    </div>
    {{range $i, $page := $el.Children}}
      {{if eq (str $page.Kind) "Страница"}}
      <div class="managed-tab-content" data-tab-content="{{$i}}" style="display:{{if eq $i 0}}block{{else}}none{{end}}">
        {{range $page.Children}}{{template "managed-element" (dict "El" . "Ctx" $ctx)}}{{end}}
      </div>
      {{end}}
    {{end}}
  </div>
{{else if eq (str $el.Kind) "Страница"}}
  {{/* Отдельная страница вне набора СтраницыФормы (её можно добавить на холсте) —
       рендерим как именованный блок с детьми, а не «рендеринг не реализован». */}}
  <fieldset class="form-group-box" style="border:1px solid #e2e8f0;border-radius:8px;padding:12px 14px;margin-bottom:14px">
    {{if $el.TitleMap}}<legend style="font-weight:600;color:#475569;padding:0 6px;font-size:13px">{{fieldTitleRU $el.TitleMap $el.Name}}</legend>{{end}}
    {{range $el.Children}}{{template "managed-element" (dict "El" . "Ctx" $ctx)}}{{end}}
  </fieldset>
{{else if eq (str $el.Kind) "ПолеВвода"}}
  {{$fn := dpField $el.DataPath}}
  {{$f := fieldByName $ctx.Entity $fn}}
  {{$hChg := hasHandler $el "ПриИзменении"}}
  <div class="form-group">
    <label>{{fieldTitleRU $el.TitleMap $fn}}{{if $el.Required}} <span style="color:#dc2626">*</span>{{end}}</label>
    {{if $f}}
      {{if isRef (str $f.Type)}}
        <div style="display:flex;gap:6px;align-items:center">
          <select id="ref-{{$fn}}" name="{{$fn}}" style="flex:1" data-ref-entity="{{$f.RefEntity}}"{{if $f.InlineCreateEnabled false}} data-ref-allow-create="1"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
            <option value="">— выбрать —</option>
            {{range index $ctx.RefOptions $fn}}
            <option value="{{index . "id"}}" {{if eq (index . "id") (index $ctx.Values $fn)}}selected{{end}}>{{index . "_label"}}</option>
            {{end}}
          </select>
          <button type="button" onclick="openRefPicker('ref-{{$fn}}')" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px">…</button>
          <button type="button" onclick="openRefCurrent('ref-{{$fn}}')" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px" title="Открыть карточку">🔍</button>
        </div>
      {{else if isEnum (str $f.Type)}}
        <select name="{{$fn}}"{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
          <option value="">— выбрать —</option>
          {{range index $ctx.EnumOptions $fn}}
          <option value="{{.Value}}" {{if eq .Value (index $ctx.Values $fn)}}selected{{end}}>{{.Label}}</option>
          {{end}}
        </select>
      {{else if eq (str $f.Type) "date"}}
        <input type="datetime-local" name="{{$fn}}" value="{{index $ctx.Values $fn}}"{{if $el.ReadOnly}} readonly{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
      {{else if eq (str $f.Type) "bool"}}
        <select name="{{$fn}}"{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
          <option value="false" {{if eq (index $ctx.Values $fn) "false"}}selected{{end}}>Нет</option>
          <option value="true" {{if eq (index $ctx.Values $fn) "true"}}selected{{end}}>Да</option>
        </select>
      {{else if isRichText (str $f.Type)}}
        {{/* textarea — скрытое form-backing поле; Quill (этап 2) монтируется на
             .richtext-editor и синхронизирует HTML обратно перед submit. Без JS
             textarea остаётся рабочим (прогрессивное улучшение). */}}
        <textarea name="{{$fn}}" class="richtext-field" rows="8" style="width:100%"{{if $el.ReadOnly}} readonly{{end}}>{{index $ctx.Values $fn}}</textarea>
        {{if not $el.ReadOnly}}<div class="richtext-editor"></div>{{end}}
      {{else if eq (str $el.Type) "file"}}
        <div style="display:flex;gap:6px;align-items:center">
          <input type="text" name="{{$fn}}" id="file-path-{{$fn}}" placeholder="Путь к файлу или выберите …" style="flex:1"{{if $el.ReadOnly}} readonly{{end}}>
          <textarea name="_fc_{{$fn}}" id="file-content-{{$fn}}" style="display:none"></textarea>
          <input type="file" id="file-pick-{{$fn}}" style="display:none" onchange="obFilePick(this,'file-path-{{$fn}}','file-content-{{$fn}}')">
          <button type="button" onclick="document.getElementById('file-pick-{{$fn}}').click()" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;white-space:nowrap" title="Выбрать файл">…</button>
        </div>
      {{else if $el.Multiline}}
        <textarea name="{{$fn}}" rows="5" style="width:100%"{{if $el.ReadOnly}} readonly{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>{{index $ctx.Values $fn}}</textarea>
      {{else}}
        <input type="text" name="{{$fn}}" value="{{index $ctx.Values $fn}}" placeholder="{{$fn}}"{{if $el.ReadOnly}} readonly{{end}}{{if $el.Mask}} pattern="{{$el.Mask}}"{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
      {{end}}
    {{else if eq (str $el.Type) "file"}}
      {{/* Поле не найдено в Entity, но элемент объявлен как file */}}
      <div style="display:flex;gap:6px;align-items:center">
        <input type="text" name="{{$fn}}" id="file-path-{{$fn}}" placeholder="Путь к файлу или выберите …" style="flex:1">
        <textarea name="_fc_{{$fn}}" id="file-content-{{$fn}}" style="display:none"></textarea>
        <input type="file" id="file-pick-{{$fn}}" style="display:none" onchange="obFilePick(this,'file-path-{{$fn}}','file-content-{{$fn}}')">
        <button type="button" onclick="document.getElementById('file-pick-{{$fn}}').click()" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;white-space:nowrap" title="Выбрать файл">…</button>
      </div>
    {{else}}
      {{/* Поле не найдено в Entity (возможно реквизит формы, ещё не привязан) */}}
      <input type="text" name="{{$fn}}" value="{{index $ctx.Values $fn}}" placeholder="{{$fn}}" style="background:#fef9c3"
        title="Реквизит формы '{{$el.DataPath}}' не найден среди полей сущности"{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
    {{end}}
    {{if $el.Hint}}<small style="color:#94a3b8;font-size:11px">{{$el.Hint}}</small>{{end}}
  </div>
{{else if eq (str $el.Kind) "ПолеСписка"}}
  {{/* Реквизит со списком значений (аналог 1С СписокВыбора): <select> из
       декларативных choices (ключ контекста — имя элемента). Выбор дёргает
       ПриИзменении тем же путём obFire, что enum/ссылка — обработчик в .form.os
       может подгрузить связанные данные и вернуть их в values. */}}
  {{$fn := dpField $el.DataPath}}
  {{$hChg := hasHandler $el "ПриИзменении"}}
  <div class="form-group">
    <label>{{fieldTitleRU $el.TitleMap $fn}}{{if $el.Required}} <span style="color:#dc2626">*</span>{{end}}</label>
    <select name="{{$fn}}"{{if hasHandler $el "НачалоВыбора"}} data-el="{{$el.Name}}" onfocus="obStartListChoice('{{$el.Name}}')"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
      <option value="">— выбрать —</option>
      {{range index $ctx.ChoiceOptions $el.Name}}
      <option value="{{.Value}}" {{if eq .Value (index $ctx.Values $fn)}}selected{{end}}>{{.Label}}</option>
      {{end}}
    </select>
    {{if $el.Hint}}<small style="color:#94a3b8;font-size:11px">{{$el.Hint}}</small>{{end}}
  </div>
{{else if eq (str $el.Kind) "Флажок"}}
  {{$fn := dpField $el.DataPath}}
  <div class="form-group" style="display:flex;align-items:center;gap:8px">
    <input type="checkbox" id="cb-{{$fn}}" name="{{$fn}}" value="true"
      {{if eq (index $ctx.Values $fn) "true"}}checked{{end}}{{if $el.ReadOnly}} disabled{{end}}>
    <label for="cb-{{$fn}}" style="margin-bottom:0;cursor:pointer">{{fieldTitleRU $el.TitleMap $fn}}</label>
  </div>
{{else if eq (str $el.Kind) "Надпись"}}
  <div class="form-decoration" style="padding:6px 0;color:#475569;font-size:13px">
    {{fieldTitleRU $el.TitleMap $el.Name}}
  </div>
{{else if eq (str $el.Kind) "Кнопка"}}
  <button type="button" class="btn btn-secondary" style="margin:6px 4px 6px 0"{{if $el.ReadOnly}} disabled{{end}}{{if hasHandler $el "Нажатие"}} onclick="obFire('{{$el.Name}}','Нажатие')"{{end}}>
    {{fieldTitleRU $el.TitleMap $el.Name}}
  </button>
{{else if eq (str $el.Kind) "ПолеКартинки"}}
  {{if $el.Picture}}
    <img src="/static/forms/{{$el.Picture}}" alt="{{$el.Name}}" style="max-width:{{if $el.Width}}{{$el.Width}}px{{else}}100px{{end}};max-height:{{if $el.Height}}{{$el.Height}}px{{else}}100px{{end}}">
  {{else}}
    <span style="color:#cbd5e1">[Картинка: {{$el.Name}}]</span>
  {{end}}
{{else if eq (str $el.Kind) "ТабличнаяЧасть"}}
  {{/* Табличная часть в managed-форме (план 37, этап 8). Имена name= совпадают
       с парсером parseTablePartRows: "tp.<TPName>.<idx>.<field>". obFire-JS
       перерисовывает tbody#mtp-body-<TPName> при изменении tableparts.
       Ссылочные колонки — select с TPRefOptions, иначе UUID без имени. */}}
  {{$tpName := dpField $el.DataPath}}
  {{$tpMeta := tablePartByName $ctx.Entity $tpName}}
  {{$tpRows := index $ctx.TablePartRows $tpName}}
  {{$tpRef := index $ctx.TPRefOptions $tpName}}
  {{$tpEnum := index $ctx.TPEnumLabels $tpName}}
  {{$tpCmds := tpCommandButtons $el}}
  <h3 style="margin:18px 0 8px;font-size:14px">{{fieldTitleRU $el.TitleMap (or $tpMeta.Title $tpName)}}</h3>
  {{if $tpMeta}}
  {{if $tpCmds}}
  <div style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:6px">
    {{range $tpCmds}}
    <button type="button" class="btn btn-sm" style="background:#eef2ff;color:#3730a3;border:1px solid #c7d2fe"
      {{if $el.ReadOnly}}disabled{{end}}{{if hasHandler . "Нажатие"}} onclick="obFire('{{.Name}}','Нажатие',{_tp:'{{$tpName}}'})"{{end}}>
      {{fieldTitleRU .TitleMap .Name}}
    </button>
    {{end}}
  </div>
  {{end}}
  {{if not $el.NoGrid}}
  <div id="sg-{{$tpName}}" class="ob-grid" style="height:{{if gt (len $tpRows) 8}}300{{else}}200{{end}}px;width:100%"
       data-sg-tp="{{$tpName}}"
       data-sg-el="{{$el.Name}}"
       {{if $el.ReadOnly}}data-sg-ro="1"{{end}}
       {{if hasHandler $el "ПриИзменении"}}data-sg-recalc="1"{{end}}
       {{if $el.AutoSum}}data-sg-autosum="1"{{end}}
       {{if hasHandler $el "ПриДобавленииСтроки"}}data-sg-rowadd="1"{{end}}
       {{if hasHandler $el "ПриУдаленииСтроки"}}data-sg-rowdel="1"{{end}}
       data-sg-cols='[{{range $i, $f := $tpMeta.Fields}}{{if $i}},{{end}}{"id":"{{$f.Name}}","name":"{{$f.Name}}","type":"{{$f.Type}}"{{if $f.RefEntity}},"ref":"{{$f.RefEntity}}"{{end}}{{if isEnum (str $f.Type)}},"enum":true{{end}}}{{end}}]'
       data-sg-ref='{{jsJSON $tpRef}}'
       data-sg-enum='{{jsJSON $tpEnum}}'
       data-sg-rows='{{jsJSON $tpRows}}'
       {{if $tpCmds}}data-sg-cmd="1"{{end}}></div>
  <input type="hidden" name="tp_json.{{$tpName}}" id="tp-json-{{$tpName}}" value="">
  <div style="display:flex;gap:6px;margin-top:4px">
    <button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569"
      onclick="obGridAddRow('{{$tpName}}')">+ Добавить строку</button>
    <button type="button" class="btn btn-sm" style="background:#fee2e2;color:#991b1b"
      onclick="obGridDelRow('{{$tpName}}')">− Удалить строку</button>
  </div>
{{else}}
<table class="tp-table" data-tp="{{$tpName}}">
    <thead>
      <tr>
        {{if $tpCmds}}<th style="width:30px"></th>{{end}}
        {{range $tpMeta.Fields}}<th>{{.Name}}</th>{{end}}
        <th style="width:40px"></th>
      </tr>
    </thead>
    <tbody id="tp-body-{{$tpName}}" {{if $tpCmds}}data-tp-cmd="1" {{end}}data-tp-fields="{{range $i, $f := $tpMeta.Fields}}{{if $i}},{{end}}{{$f.Name}}|{{$f.Type}}{{if $f.RefEntity}}:{{$f.RefEntity}}{{end}}{{end}}">
    {{range $i, $row := $tpRows}}
      <tr{{with formRowClass $row}} class="{{.}}"{{end}}>
        {{if $tpCmds}}<td style="text-align:center"><input type="checkbox" class="_tp-sel"></td>{{end}}
        {{range $f := $tpMeta.Fields}}
        <td{{with formCellClass $row $f.Name}} class="{{.}}"{{end}}>
          {{$v := index $row $f.Name}}
          {{if isRef (str $f.Type)}}
            <div style="display:flex;gap:4px;align-items:center">
              <select name="tp.{{$tpName}}.{{$i}}.{{$f.Name}}" style="flex:1" data-ref-entity="{{$f.RefEntity}}"{{if $f.InlineCreateEnabled true}} data-ref-allow-create="1"{{end}}>
                <option value="">— выбрать —</option>
                {{range index $tpRef $f.Name}}
                <option value="{{index . "id"}}" {{if eq (str (index . "id")) (refID $v)}}selected{{end}}>{{index . "_label"}}</option>
                {{end}}
              </select>
              <button type="button" onclick="openRefPicker(this.parentElement.querySelector('select'))" style="padding:4px 8px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="Выбрать из списка">...</button>
              <button type="button" onclick="openRefCurrent(this.parentElement.querySelector('select'))" style="padding:4px 7px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="Открыть карточку">🔍</button>
            </div>
          {{else if eq (str $f.Type) "number"}}
            <input type="number" step="any" name="tp.{{$tpName}}.{{$i}}.{{$f.Name}}" value="{{$v}}" data-tp-num="{{$f.Name}}" oninput="recalcTpRow(this)">
          {{else}}
            <input type="text" name="tp.{{$tpName}}.{{$i}}.{{$f.Name}}" value="{{$v}}" oninput="recalcTpRow(this)">
          {{end}}
        </td>
        {{end}}
        <td><button type="button" class="del-btn" onclick="this.closest('tr').remove()">×</button></td>
      </tr>
    {{end}}
    </tbody>
    <tfoot id="tp-foot-{{$tpName}}" class="tp-footer" style="display:none"><tr>
      {{if $tpCmds}}<td></td>{{end}}
      {{range $f := $tpMeta.Fields}}{{if eq (str $f.Type) "number"}}<td class="tp-total" data-tp-total="{{$tpName}}.{{$f.Name}}" style="text-align:right;font-variant-numeric:tabular-nums">0</td>{{else}}<td></td>{{end}}{{end}}<td></td>
    </tr></tfoot>
  </table>
  <button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin:0 0 12px"
    onclick="addTpRow('{{$tpName}}', [{{range $tpMeta.Fields}}'{{.Name}}',{{end}}], [{{range $tpMeta.Fields}}{{if eq (str .Type) "number"}}'{{.Name}}',{{end}}{{end}}], document.getElementById('tp-body-{{$tpName}}').rows.length)">
    + Добавить строку
{{end}}
  </button>
  {{else}}
  {{/* ValueTable: формовый атрибут-таблица (не документная ТЧ). */}}
  {{$vtCols := formAttrVT $ctx.Form $tpName}}
  {{if $vtCols}}
  {{$vtRows := index $ctx.TablePartRows $tpName}}
  {{$vtCmds := tpCommandButtons $el}}
  <h3 style="margin:18px 0 8px;font-size:14px">{{fieldTitleRU $el.TitleMap (or $tpMeta.Title $tpName)}}</h3>
  {{if $vtCmds}}
  <div style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:6px">
    {{range $vtCmds}}
    <button type="button" class="btn btn-sm" style="background:#eef2ff;color:#3730a3;border:1px solid #c7d2fe"
      onclick="obFire('{{.Name}}','Нажатие',{_tp:'{{$tpName}}'})">
      {{fieldTitleRU .TitleMap .Name}}
    </button>
    {{end}}
  </div>
  {{end}}
  <table class="tp-table" data-vt="{{$tpName}}">
    <thead>
      <tr>
        {{range $vtCols}}<th>{{if .Title}}{{index .Title "ru"}}{{else}}{{.Name}}{{end}}</th>{{end}}
        <th style="width:40px"></th>
      </tr>
    </thead>
    <tbody id="vt-body-{{$tpName}}" data-vt-fields="{{range $i, $c := $vtCols}}{{if $i}},{{end}}{{$c.Name}}|{{$c.TypeRef}}{{end}}">
    {{range $i, $row := $vtRows}}
      <tr{{with formRowClass $row}} class="{{.}}"{{end}}>
        {{range $c := $vtCols}}
        <td{{with formCellClass $row $c.Name}} class="{{.}}"{{end}}>
          {{$v := index $row $c.Name}}
          {{if eq (lower $c.TypeRef) "number"}}
            <input type="number" step="any" name="vt.{{$tpName}}.{{$i}}.{{$c.Name}}" value="{{$v}}" data-vt-num="{{$c.Name}}">
          {{else if eq (lower $c.TypeRef) "bool"}}
            <input type="checkbox" name="vt.{{$tpName}}.{{$i}}.{{$c.Name}}" value="true" {{if eq (str $v) "true"}}checked{{end}}>
          {{else}}
            <input type="text" name="vt.{{$tpName}}.{{$i}}.{{$c.Name}}" value="{{$v}}">
          {{end}}
        </td>
        {{end}}
        <td><button type="button" class="del-btn" onclick="this.closest('tr').remove()">×</button></td>
      </tr>
    {{end}}
    </tbody>
  </table>
  <button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin:0 0 12px"
    onclick="addVtRow('{{$tpName}}', [{{range $vtCols}}'{{.Name}}',{{end}}])">
    + Добавить строку
  </button>
  {{else}}
  <div style="background:#fef9c3;padding:8px;border-radius:6px;font-size:12px;color:#92400e">
    Табличная часть «{{$tpName}}» не найдена в метаданных сущности.
  </div>
  {{end}}
  {{end}}
{{else if eq (str $el.Kind) "ПолеДаты"}}
  {{/* Нативный выбор ДАТЫ без времени (issue #150). Браузер показывает дату
       по локали (в ru — дд.ММ.гггг). Значение круглим до YYYY-MM-DD, что
       корректно парсится при сохранении (formToFields, layout 2006-01-02). */}}
  {{$fn := dpField $el.DataPath}}
  {{$hChg := hasHandler $el "ПриИзменении"}}
  {{$dv := index $ctx.Values $fn}}
  <div class="form-group">
    <label>{{fieldTitleRU $el.TitleMap $fn}}{{if $el.Required}} <span style="color:#dc2626">*</span>{{end}}</label>
    <input type="date" name="{{$fn}}" value="{{if ge (len $dv) 10}}{{slice $dv 0 10}}{{else}}{{$dv}}{{end}}"{{if $el.ReadOnly}} readonly{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
  </div>
{{else if eq (str $el.Kind) "Переключатель"}}
  {{/* Поле с набором значений: радио-переключатель (по умолчанию) или список
       (view: select). Для enum-поля значения берутся из перечисления
       автоматически; иначе — из el.Options. Submit шлёт обычную пару name=поле,
       значение приводится по типу поля в formToFields (план 71b, C1/C2). */}}
  {{$fn := dpField $el.DataPath}}
  {{$f := fieldByName $ctx.Entity $fn}}
  {{$cur := index $ctx.Values $fn}}
  {{$hChg := hasHandler $el "ПриИзменении"}}
  {{$enum := and $f (isEnum (str $f.Type))}}
  <div class="form-group">
    <label>{{fieldTitleRU $el.TitleMap $fn}}{{if $el.Required}} <span style="color:#dc2626">*</span>{{end}}</label>
    {{if eq $el.View "select"}}
      <select name="{{$fn}}"{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}>
        <option value="">— выбрать —</option>
        {{if $enum}}
          {{range index $ctx.EnumOptions $fn}}<option value="{{.Value}}" {{if eq .Value $cur}}selected{{end}}>{{.Label}}</option>{{end}}
        {{else}}
          {{range $el.Options}}<option value="{{.ValueStr}}" {{if eq .ValueStr $cur}}selected{{end}}>{{.Label}}</option>{{end}}
        {{end}}
      </select>
    {{else}}
      <div class="switch-options" style="display:flex;flex-wrap:wrap;gap:12px;padding:4px 0">
        {{if $enum}}
          {{range index $ctx.EnumOptions $fn}}<label style="display:inline-flex;align-items:center;gap:5px;cursor:pointer"><input type="radio" name="{{$fn}}" value="{{.Value}}"{{if eq .Value $cur}} checked{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}> {{.Label}}</label>{{end}}
        {{else}}
          {{range $el.Options}}<label style="display:inline-flex;align-items:center;gap:5px;cursor:pointer"><input type="radio" name="{{$fn}}" value="{{.ValueStr}}"{{if eq .ValueStr $cur}} checked{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} onchange="obFire('{{$el.Name}}','ПриИзменении')"{{end}}> {{.Label}}</label>{{end}}
        {{end}}
      </div>
    {{end}}
    {{if $el.Hint}}<small style="color:#94a3b8;font-size:11px">{{$el.Hint}}</small>{{end}}
  </div>
{{else if eq (str $el.Kind) "СтраницаКоманднаяПанель"}}
  {{/* пропускаем — отрисовывается через toolbar в обвязке формы */}}
{{else if eq (str $el.Kind) "КоманднаяПанель"}}
  {{/* пропускаем — отрисовывается через toolbar в обвязке формы */}}
{{else}}
  <div class="form-group" style="background:#fef9c3;padding:8px;border-radius:6px;font-size:11px;color:#92400e">
    Элемент «{{$el.Name}}» типа «{{$el.Kind}}»: рендеринг не реализован.
  </div>
{{end}}
{{end}}

{{define "page-managed-form"}}
{{template "head" .}}{{if not .IsPopup}}{{template "nav" .}}{{end}}
<style>
.managed-group-horizontal>.managed-group-body{display:flex;flex-wrap:wrap;gap:12px;align-items:flex-start}
.managed-group-horizontal>.managed-group-body>.form-group{flex:1 1 220px;min-width:180px;margin-bottom:0}
.managed-group-horizontal>.managed-group-body>.form-decoration,.managed-group-horizontal>.managed-group-body>button{flex:0 0 auto}
</style>
{{if hasGridTP .Form}}
<link rel="stylesheet" href="/vendor/slickgrid/slick.grid.css">
<link rel="stylesheet" href="/vendor/slickgrid/slick-default-theme.css">
<style>
.ob-grid{font-size:13px;border:1px solid #cbd5e1;border-radius:6px;overflow:hidden}
.ob-grid .slick-header-columns{background:#f1f5f9;border-bottom:2px solid #cbd5e1}
.ob-grid .slick-header-column{font-weight:600;color:#475569;font-size:12px;padding:6px 8px;border-right:1px solid #cbd5e1}
.ob-grid .slick-header-column:hover{background:#e2e8f0}
/* Зебра — на строке; ячейки прозрачны, чтобы фон строки просвечивал. */
.ob-grid .slick-cell{padding:4px 8px;border-right:1px solid #e2e8f0;border-bottom:1px solid #e2e8f0;background:transparent}
.ob-grid .slick-row.odd{background:#f6f8fb}
.ob-grid .slick-row:hover .slick-cell{background:#eef4ff}
.ob-grid .slick-cell.selected{background:#dbeafe}
.ob-grid .slick-cell.active{box-shadow:inset 0 0 0 2px #3b82f6}
.ob-grid .slick-footerrow{background:#f1f5f9;border-top:2px solid #cbd5e1}
.ob-grid .slick-footerrow-column{padding:4px 8px;border-right:1px solid #e2e8f0;color:#334155}
.ob-grid .ob-num{text-align:right;font-variant-numeric:tabular-nums}
.ob-grid .ob-ref{color:#2563eb;cursor:pointer}
.ob-grid .ob-ref:hover{text-decoration:underline}
</style>
{{end}}
{{if .FormConditionalCSS}}<style>{{.FormConditionalCSS}}</style>{{end}}
<main>
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;max-width:1400px">
  <h2 style="margin-bottom:0">
    {{if .IsProcessor}}{{.Processor.DisplayName $.Lang}}{{else}}{{if .IsNew}}{{t $.Lang "Создать"}}{{else}}{{t $.Lang "Редактировать"}}{{end}} — {{.Entity.DisplayName $.Lang}}{{end}}
    <span style="font-size:11px;color:#10b981;background:#d1fae5;padding:2px 8px;border-radius:10px;vertical-align:middle;font-weight:500" title="Управляемая форма из forms/{{if .IsProcessor}}{{lower .Processor.Name}}{{else}}{{lower .Entity.Name}}{{end}}/">◇ managed</span>
  </h2>
  {{if .IsPopup}}
  <a href="javascript:void(0)" onclick="try{parent.postMessage({source:'obRefCancel'}, '*')}catch(e){}" title="Закрыть" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
  {{else}}
  <a href="{{if .IsProcessor}}/ui/{{else}}/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}{{end}}" title="Закрыть" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
  {{end}}
</div>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
{{if .RunError}}<div class="error">{{.RunError}}</div>{{end}}
{{if .Messages}}{{range .Messages}}<div class="msg-info">{{.}}</div>{{end}}{{end}}
{{if .FormWarnings}}{{range .FormWarnings}}<div class="msg-info">{{.}}</div>{{end}}{{end}}

{{if not .IsPopup}}
<div style="display:flex;align-items:center;gap:8px;margin-bottom:16px;flex-wrap:wrap">
  {{if .IsProcessor}}
  {{/* Кнопка «Выполнить» скрыта: managed-форма использует свои кнопки (Предпросмотр, Записать, ЗаписатьИПровести) */}}
  {{else}}
  {{if .Entity.Posting}}
    {{if not .IsNew}}
      {{if eq (index .Values "posted") "true"}}
        <span style="color:#16a34a;font-weight:600;font-size:13px">✓ Проведён</span>
      {{else}}
        <span style="color:#94a3b8;font-size:13px">Не проведён</span>
      {{end}}
    {{end}}
  {{end}}
  {{if .CanWrite}}<button class="btn btn-secondary" type="submit" name="_action" value="" form="main-form">Записать</button>{{end}}
  {{if .Entity.Posting}}
    {{if ne (index .Values "deletion_mark") "true"}}
      {{if .CanPost}}<button class="btn btn-primary" type="submit" name="_action" value="post" form="main-form">Провести</button>{{end}}
      {{if .CanPost}}<button class="btn btn-post" type="submit" name="_action" value="post_and_close" form="main-form">Провести и закрыть</button>{{end}}
    {{end}}
    {{if not .IsNew}}
      {{if eq (index .Values "posted") "true"}}
        {{if $.CanUnpost}}<button class="btn btn-sm" style="background:#e2e8f0;color:#374151" form="form-unpost" type="submit">Отменить проведение</button>{{end}}
      {{end}}
    {{end}}
  {{end}}
  {{if and .CanDelete (not .IsNew) (eq (index .Values "deletion_mark") "true")}}
    <form method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/delete?mark=0" style="display:inline">
      <button class="btn btn-sm btn-secondary" type="submit">Снять пометку на удаление</button>
    </form>
  {{end}}
  {{if not .IsNew}}
    <a href="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/history" class="btn btn-sm btn-secondary">История</a>
    {{if or .AllPrintForms .HasPrintProc}}
    <div style="position:relative">
      <button type="button" class="btn btn-sm btn-secondary" onclick="var d=this.nextElementSibling;d.style.display=d.style.display==='none'?'block':'none'">{{t $.Lang "Печать"}} ▾</button>
      <div style="display:none;position:absolute;top:100%;left:0;background:#fff;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.1);min-width:200px;z-index:50;margin-top:4px">
        {{range .AllPrintForms}}
        <div style="display:flex;align-items:center;border-bottom:1px solid #f1f5f9">
          <a href="/ui/{{lower (str $.Entity.Kind)}}/{{$.Entity.Name}}/{{$.ID}}/print/{{.Name}}" target="_blank"
             style="flex:1;display:block;padding:9px 16px;color:#334155;text-decoration:none;font-size:13px">{{.Name}}{{if .External}} <span style="color:#94a3b8;font-size:11px">({{t $.Lang "внешняя"}})</span>{{end}}</a>
          <a href="/ui/{{lower (str $.Entity.Kind)}}/{{$.Entity.Name}}/{{$.ID}}/print/{{.Name}}/pdf" target="_blank"
             style="padding:9px 14px;color:#16a34a;text-decoration:none;font-size:12px;font-weight:600">PDF</a>
        </div>
        {{end}}
        {{if .HasPrintProc}}
        <a href="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/print/_module" target="_blank"
           style="display:block;padding:9px 16px;color:#334155;text-decoration:none;font-size:13px;border-bottom:1px solid #f1f5f9">📋 {{t $.Lang "Печать (модуль)"}}</a>
        {{end}}
      </div>
    </div>
    {{end}}
    {{if .Receivers}}
    <div style="position:relative;display:inline-block">
      <button type="button" class="btn btn-sm btn-secondary" onclick="var d=this.nextElementSibling;d.style.display=d.style.display==='none'?'block':'none'">{{t $.Lang "Ввести на основании"}} ▾</button>
      <div style="display:none;position:absolute;top:100%;left:0;background:#fff;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.1);min-width:200px;z-index:50;margin-top:4px">
        {{range .Receivers}}
        <a href="/ui/{{lower (str .Kind)}}/{{.Name}}/new?based_on={{$.Entity.Name}}&based_on_id={{$.ID}}"
           style="display:block;padding:9px 16px;color:#334155;text-decoration:none;font-size:13px;border-bottom:1px solid #f1f5f9">{{.DisplayName $.Lang}}</a>
        {{end}}
      </div>
    </div>
    {{end}}
    {{if and .CanDelete (not (deleteHidden .Form))}}
    <form method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/delete"
          onsubmit="return confirm('{{if .IsAdmin}}Удалить запись навсегда?{{else}}Пометить запись на удаление?{{end}}')" style="margin-left:auto">
      <button class="btn btn-danger btn-sm" type="submit">{{if .IsAdmin}}Удалить{{else}}Пометить на удаление{{end}}</button>
    </form>
    {{end}}
  {{end}}
  {{end}}{{/* end if not .IsProcessor */}}
</div>
{{end}}{{/* end if not .IsPopup */}}
{{if and (not .IsNew) .Entity.Posting}}
{{if eq (index .Values "posted") "true"}}
<form id="form-unpost" method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/{{.ID}}/unpost"></form>
{{end}}
{{end}}

{{/* Движения регистров: свёрнутые «таблеточки» по каждому регистру с
     количеством строк. Симметрично page-form, чтобы пользователь видел
     результат проведения и в managed-форме. */}}
{{if and (not .IsNew) .DocMovements}}
<div style="margin-bottom:12px;display:flex;gap:6px;flex-wrap:wrap">
  {{range $regName, $rows := .DocMovements}}
  <details style="display:inline">
    <summary style="display:inline;cursor:pointer;font-size:12px;padding:4px 10px;background:#f0f4ff;color:#1a4a80;border-radius:4px;font-weight:600;list-style:none">
      {{$regName}} ({{len $rows}}) ▾
    </summary>
    <div style="position:absolute;z-index:100;background:#fff;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.12);margin-top:4px;min-width:300px;max-height:300px;overflow:auto">
      <table class="list-tbl" style="font-size:12px;margin:0">
        <tr><th>№</th><th>Вид</th>{{$first := index $rows 0}}{{range $k, $v := $first}}{{if and (ne $k "line_number") (ne $k "вид_движения")}}<th>{{$k}}</th>{{end}}{{end}}</tr>
        {{range $i, $row := $rows}}
        <tr>
          <td>{{add $i 1}}</td>
          <td>{{if eq (index $row "вид_движения") "Приход"}}<span style="color:#16a34a">▲</span>{{else if eq (index $row "вид_движения") "Расход"}}<span style="color:#dc2626">▼</span>{{else}}—{{end}}</td>
          {{range $k, $v := $row}}{{if and (ne $k "line_number") (ne $k "вид_движения")}}<td>{{$v}}</td>{{end}}{{end}}
        </tr>
        {{end}}
      </table>
    </div>
  </details>
  {{end}}
</div>
{{end}}

<div class="card">
<form id="main-form" method="POST" onsubmit="if(window.obGridSync)obGridSync()" {{if .IsProcessor}}action="/ui/processor/{{lower .Processor.Name}}" enctype="multipart/form-data"{{end}}>
{{if and (not .IsNew) (index .Values "_version")}}<input type="hidden" name="_version" value="{{index .Values "_version"}}">{{end}}
{{if .IsPopup}}<input type="hidden" name="_popup" value="1">{{end}}

{{$ctx := .}}
{{range .Form.Elements}}
  {{template "managed-element" (dict "El" . "Ctx" $ctx)}}
{{end}}

<div style="margin-top:16px">
  {{if .IsPopup}}
  {{if .CanWrite}}<button class="btn btn-primary" type="submit" name="_action" value="" form="main-form">Записать и выбрать</button>{{end}}
  <a href="javascript:void(0)" onclick="try{parent.postMessage({source:'obRefCancel'}, '*')}catch(e){}" class="btn btn-cancel">Отмена</a>
  {{else if .IsProcessor}}
  {{/* Кнопка «Выполнить» скрыта: managed-форма использует свои кнопки */}}
  <a href="/ui/" class="btn btn-cancel">Отмена</a>
  {{else}}
  {{if .CanWrite}}<button class="btn btn-secondary" type="submit" name="_action" value="" form="main-form">Записать</button>{{end}}
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" class="btn btn-cancel">Отмена</a>
  {{end}}
</div>
</form>
</div>

{{/* ── Рантайм событий managed-формы (план 37, этап 8) ────────────────── */}}
<script>
// Опции справочников для ТЧ — используются при JS-перерисовке после
// событий obFire, чтобы ссылочные колонки рендерились как select, а не
// как text input с UUID. Структура: {tpName: {fieldName: [{id, _label}, ...]}}.
window._tpRefOpts = {{jsJSON .TPRefOptions}};
// Переводы значений перечислений для ТЧ — используются при JS-перерисовке.
// Структура: {tpName: {fieldName: {value: label}}}.
window._tpEnumLabels = {{jsJSON .TPEnumLabels}};
// Порядок значений перечислений для DOM-ТЧ (applyTableParts).
// Структура: {tpName: {fieldName: [value, ...]}} — значения в порядке объявления values:.
window._tpEnumOrder = {{jsJSON .TPEnumOrder}};
// obFire — общая JS-обвязка для onclick/onchange элементов формы.
// Отправляет текущие form-values + имя элемента/события в /ui/.../form-event,
// получает JSON с новыми значениями и сообщениями от Сообщить(), применяет их.
(function(){
  const KIND   = "{{lower (str .Entity.Kind)}}";
  const ENTITY = "{{.Entity.Name}}";
  {{if .IsProcessor}}
  const URL    = "/ui/processor/{{lower .Processor.Name}}/form-event";
  const DOC_ID = "";
  {{else}}
  const URL    = "/ui/" + KIND + "/" + ENTITY + "/form-event";
  const DOC_ID = {{if .ID}}"{{.ID}}"{{else}}""{{end}};
  {{end}}

  function ensureBanner(){
    let b = document.getElementById('ob-fmevt-banner');
    if (b) return b;
    b = document.createElement('div');
    b.id = 'ob-fmevt-banner';
    b.style.cssText = 'position:fixed;top:14px;right:14px;max-width:380px;z-index:9999;display:flex;flex-direction:column;gap:6px;pointer-events:none';
    document.body.appendChild(b);
    return b;
  }
  function flash(text, kind){
    const b = ensureBanner();
    const d = document.createElement('div');
    const palette = kind === 'err'
      ? 'background:#fee2e2;color:#991b1b;border:1px solid #fecaca'
      : 'background:#d1fae5;color:#065f46;border:1px solid #a7f3d0';
    d.style.cssText = palette + ';padding:8px 28px 8px 12px;border-radius:6px;font-size:12px;box-shadow:0 1px 3px rgba(0,0,0,.08);pointer-events:auto;cursor:copy;position:relative;white-space:pre-wrap;word-break:break-word';
    const msg = document.createElement('span');
    msg.textContent = text;
    d.appendChild(msg);
    // Клик по тосту — скопировать текст в буфер (удобно для ошибок). Тост не
    // закрывается по клику; для закрытия — крестик.
    d.title = 'Клик — скопировать текст';
    d.onclick = function(){
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(text); }
        else { const ta=document.createElement('textarea'); ta.value=text; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove(); }
        const prev = d.style.boxShadow; d.style.boxShadow='0 0 0 2px #16a34a';
        setTimeout(function(){ d.style.boxShadow=prev; }, 600);
      } catch(e){}
    };
    const x = document.createElement('span');
    x.textContent = '×';
    x.style.cssText = 'position:absolute;top:4px;right:8px;cursor:pointer;font-weight:700;opacity:.55;font-size:14px;line-height:1';
    x.onclick = function(ev){ ev.stopPropagation(); d.remove(); };
    d.appendChild(x);
    b.appendChild(d);
    // Ошибки держим до закрытия крестиком (чтобы успеть прочитать/скопировать);
    // info-сообщения сами исчезают через 5 c.
    if (kind !== 'err') setTimeout(() => d.remove(), 5000);
  }
  // Доступно другим скриптам (например, грид-IIFE показывает ошибки настройки).
  window.obFlash = flash;
  function applyValues(values){
    if (!values) return;
    const form = document.getElementById('main-form');
    if (!form) return;
    Object.keys(values).forEach(function(k){
      const v = values[k];
      // Пропускаем файловые поля: не подставляем содержимое в поле пути
      const fc = form.querySelector('[name="_fc_' + (window.CSS && CSS.escape ? CSS.escape(k) : k) + '"]');
      if (fc) return;
      const inp = form.querySelector('[name="' + (window.CSS && CSS.escape ? CSS.escape(k) : k) + '"]');
      if (!inp) return;
      if (inp.type === 'checkbox') {
        inp.checked = v === true || v === 'true' || v === 1;
      } else {
        inp.value = (v === null || v === undefined) ? '' : v;
      }
    });
  }
  // applyChoiceList — заполняет <select> элемента ПолеСписка динамическим списком
  // значений из ответа НачалоВыбора (choiceList). Текущее значение сохраняется,
  // если присутствует в новом списке.
  function applyChoiceList(elName, list){
    if (!elName || !list) return;
    const sel = document.querySelector('select[data-el="' + (window.CSS && CSS.escape ? CSS.escape(elName) : elName) + '"]');
    if (!sel) return;
    const cur = sel.value;
    while (sel.options.length) sel.remove(0);
    const o0 = document.createElement('option'); o0.value = ''; o0.textContent = '— выбрать —'; sel.appendChild(o0);
    for (let i = 0; i < list.length; i++){
      const o = document.createElement('option');
      o.value = list[i].value;
      o.textContent = (list[i].label != null && list[i].label !== '') ? list[i].label : list[i].value;
      if (String(list[i].value) === String(cur)) o.selected = true;
      sel.appendChild(o);
    }
  }
  // obStartListChoice — событие НачалоВыбора для ПолеСписка: на фокусе элемента
  // обработчик формы формирует список значений (ДобавитьЗначениеСписка), ответ
  // приходит в choiceList и применяется applyChoiceList. Флаг busy защищает от
  // повторных одновременных запросов по одному элементу.
  window.obStartListChoice = function(elName){
    window._obListChoiceBusy = window._obListChoiceBusy || {};
    if (!elName || window._obListChoiceBusy[elName] || !window.obFire) return;
    window._obListChoiceBusy[elName] = true;
    Promise.resolve(window.obFire(elName, 'НачалоВыбора')).catch(function(){}).then(function(){
      window._obListChoiceBusy[elName] = false;
    });
  };
  function obFormRowClass(row) {
    return row && row._form_row_class ? String(row._form_row_class) : '';
  }
  function obFormCellClass(row, field) {
    var cc = row && row._form_cell_classes;
    if (!cc || !field) return '';
    if (cc[field]) return String(cc[field]);
    var want = String(field).toLowerCase();
    for (var k in cc) {
      if (Object.prototype.hasOwnProperty.call(cc, k) && String(k).toLowerCase() === want) {
        return cc[k] ? String(cc[k]) : '';
      }
    }
    return '';
  }
  function applyFormConditionalCSS(css) {
    var id = 'ob-form-conditional-runtime-css';
    var el = document.getElementById(id);
    if (!el) {
      el = document.createElement('style');
      el.id = id;
      document.head.appendChild(el);
    }
    el.textContent = css || '';
  }
  window.applyFormConditionalCSS = applyFormConditionalCSS;
  // Перерисовка табчастей по ответу сервера. tbody у нас имеет
  // id=mtp-body-<TP> и атрибут data-tp-fields="name|type[:Ref],name|type,..."
  // где field-meta использовалось для определения типа input при первичном рендере;
  // тот же формат используется тут для повторного создания строк.
  function applyTableParts(tps){
    if (!tps) return;
    Object.keys(tps).forEach(function(tpName){
      const tbody = document.getElementById('tp-body-' + tpName);
      if (!tbody) return;
      const fieldsMeta = (tbody.getAttribute('data-tp-fields') || '').split(',').map(function(s){
        const idx = s.indexOf('|');
        if (idx < 0) return { name: s, type: 'string', ref: '' };
        const name = s.slice(0, idx);
        const rest = s.slice(idx + 1);
        const refIdx = rest.indexOf(':');
        if (refIdx >= 0) return { name: name, type: rest.slice(0, refIdx), ref: rest.slice(refIdx + 1) };
        return { name: name, type: rest, ref: '' };
      });
      const rows = tps[tpName] || [];
      const refOpts = (window._tpRefOpts && window._tpRefOpts[tpName]) || {};
      const tpEnumLabels = (window._tpEnumLabels && window._tpEnumLabels[tpName]) || {};
      const tpEnumOrder = (window._tpEnumOrder && window._tpEnumOrder[tpName]) || {};
      const hasCmd = tbody.getAttribute('data-tp-cmd') === '1';
      tbody.innerHTML = '';
      rows.forEach(function(row, idx){
        const tr = document.createElement('tr');
        tr.className = obFormRowClass(row);
        if (hasCmd) {
          const tdSel = document.createElement('td');
          tdSel.style.textAlign = 'center';
          const cbSel = document.createElement('input');
          cbSel.type = 'checkbox'; cbSel.className = '_tp-sel';
          tdSel.appendChild(cbSel);
          tr.appendChild(tdSel);
        }
        fieldsMeta.forEach(function(f){
          const td = document.createElement('td');
          td.className = obFormCellClass(row, f.name);
          const v = row[f.name];
          const isRef = f.type === 'reference' || f.type.indexOf('reference') === 0;
          const isEnum = f.type === 'enum' || f.type.indexOf('enum') === 0;
          if (isRef && refOpts[f.name]) {
            const sel = document.createElement('select');
            sel.name = 'tp.' + tpName + '.' + idx + '.' + f.name;
            const empty = document.createElement('option');
            empty.value = ''; empty.textContent = '— выбрать —';
            sel.appendChild(empty);
            // v приходит сериализованным как UUID-string (serializeTablePartRows),
            // но на всякий случай учитываем и legacy-формат с GetRefUUID-методом.
            const cur = (v && typeof v === 'object' && v.GetRefUUID) ? v.GetRefUUID() : (v == null ? '' : String(v));
            refOpts[f.name].forEach(function(opt){
              const o = document.createElement('option');
              o.value = opt.id;
              o.textContent = opt._label;
              if (String(opt.id) === cur) o.selected = true;
              sel.appendChild(o);
            });
            td.appendChild(sel);
          } else if (isEnum && tpEnumLabels[f.name]) {
            const enumLabMap = tpEnumLabels[f.name];
            const sel = document.createElement('select');
            sel.name = 'tp.' + tpName + '.' + idx + '.' + f.name;
            const cur = (v == null ? '' : String(v));
            // Используем _tpEnumOrder для правильного порядка значений (порядок
            // объявления values:), а не алфавитный Object.keys(enumLabMap).
            const orderedVals = (tpEnumOrder[f.name] && tpEnumOrder[f.name].length > 0)
              ? tpEnumOrder[f.name] : Object.keys(enumLabMap);
            orderedVals.forEach(function(val){
              const o = document.createElement('option');
              o.value = val;
              o.textContent = enumLabMap[val] !== undefined ? enumLabMap[val] : val;
              if (val === cur) o.selected = true;
              sel.appendChild(o);
            });
            td.appendChild(sel);
          } else {
            const inp = document.createElement('input');
            inp.name = 'tp.' + tpName + '.' + idx + '.' + f.name;
            if (isRef) {
              inp.type = 'text';
              inp.placeholder = 'UUID';
              inp.value = (v && typeof v === 'object' && v.GetRefUUID) ? v.GetRefUUID() : (v == null ? '' : String(v));
            } else if (f.type === 'number') {
              inp.type = 'number';
              inp.step = 'any';
              inp.value = (v == null ? '' : v);
            } else {
              inp.type = 'text';
              inp.value = (v == null ? '' : v);
            }
            td.appendChild(inp);
          }
          tr.appendChild(td);
        });
        const tdDel = document.createElement('td');
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'del-btn';
        btn.textContent = '×';
        btn.onclick = function(){ tr.remove(); };
        tdDel.appendChild(btn);
        tr.appendChild(tdDel);
        tbody.appendChild(tr);
      });
    });
  }

  // Экспортируем в window, чтобы grid-aware обёртка (план 48) могла
  // переопределить applyTableParts и обновлять SlickGrid после round-trip.
  // obFire ниже зовёт именно window.applyTableParts — так обёртка попадает
  // в цепочку, а не остаётся мёртвым кодом.
  window.applyTableParts = applyTableParts;

  // applyFormTables(vts) — перерисовка ValueTable (формовых атрибутов-таблиц) по
  // ответу сервера (formTables). Зеркалит applyTableParts, но для tbody
  // id=vt-body-<name>: имена инпутов vt.<name>.<idx>.<field>, типы берутся из
  // data-vt-fields ("name|TypeRef,..."). Нужна, чтобы VT обновлялась после
  // round-trip-события (ПриИзменении и т.п.) — раньше сервер слал formTables, но
  // клиент их игнорировал, и таблица «застывала».
  function applyFormTables(vts){
    if (!vts) return;
    Object.keys(vts).forEach(function(vtName){
      var tbody = document.getElementById('vt-body-' + vtName);
      if (!tbody) return;
      var fieldsMeta = (tbody.getAttribute('data-vt-fields') || '').split(',').map(function(s){
        var idx = s.indexOf('|');
        if (idx < 0) return { name: s, type: 'string' };
        return { name: s.slice(0, idx), type: (s.slice(idx + 1) || 'string').toLowerCase() };
      });
      var rows = vts[vtName] || [];
      tbody.innerHTML = '';
      rows.forEach(function(row, idx){
        var tr = document.createElement('tr');
        tr.className = obFormRowClass(row);
        fieldsMeta.forEach(function(f){
          var td = document.createElement('td');
          td.className = obFormCellClass(row, f.name);
          var v = row[f.name];
          var inp = document.createElement('input');
          inp.name = 'vt.' + vtName + '.' + idx + '.' + f.name;
          if (f.type === 'number') {
            inp.type = 'number'; inp.step = 'any';
            inp.setAttribute('data-vt-num', f.name);
            inp.value = (v == null ? '' : v);
          } else if (f.type === 'bool') {
            inp.type = 'checkbox'; inp.value = 'true';
            if (String(v) === 'true') inp.checked = true;
          } else {
            inp.type = 'text';
            inp.value = (v == null ? '' : v);
          }
          td.appendChild(inp);
          tr.appendChild(td);
        });
        var tdDel = document.createElement('td');
        var btn = document.createElement('button');
        btn.type = 'button'; btn.className = 'del-btn'; btn.textContent = '×';
        btn.onclick = function(){ tr.remove(); };
        tdDel.appendChild(btn);
        tr.appendChild(tdDel);
        tbody.appendChild(tr);
      });
    });
  }
  window.applyFormTables = applyFormTables;

  // obFilePick — при выборе файла: имя в текстовое поле, содержимое в скрытый
  // textarea. Кодировка: UTF-8 → fallback Windows-1251 (TextDecoder).
  // В webview/Electron — file.path вместо содержимого.
  window.obFilePick = function(input, pathId, contentId) {
    const file = input.files[0];
    if (!file) return;
    const pathEl = document.getElementById(pathId);
    const contentEl = contentId ? document.getElementById(contentId) : null;
    if (!pathEl) return;
    if (file.path) {
      pathEl.value = file.path;
      if (contentEl) contentEl.value = '';
      return;
    }
    pathEl.value = file.name;
    if (!contentEl) return;
    const reader = new FileReader();
    reader.onload = function() {
      const bytes = new Uint8Array(reader.result);
      let text;
      try {
        text = new TextDecoder('utf-8', {fatal: true}).decode(bytes);
      } catch(e) {
        text = new TextDecoder('windows-1251').decode(bytes);
      }
      contentEl.value = text;
    };
    reader.readAsArrayBuffer(file);
  };

  // obFire(elementName, eventName[, extraParams]) — extraParams (объект)
  // добавляются к телу запроса. Используется подбором (план 46): фаза 2
  // шлёт {_pick_result}, команды ТЧ — {_tp, _tp_selected}.
  window.obFire = async function(elementName, eventName, extraParams){
   try {
    // Зафиксировать активную правку ячейки грида: иначе её значение не попадёт
    // в tp_json, а редактор держит editor-lock — из-за чего первый клик по
    // кнопке лишь закрывает редактор и «не нажимается».
    var _grids = window._obGrids || {};
    for (var _t in _grids) {
      var _lk = _grids[_t].grid && _grids[_t].grid.getEditorLock && _grids[_t].grid.getEditorLock();
      if (_lk && _lk.isActive()) _lk.commitCurrentEdit();
    }
    if (window.obGridSync) obGridSync();
    const form = document.getElementById('main-form');
    if (!form) return;
    const fd = new FormData(form);
    fd.set('_element', elementName || '');
    fd.set('_event', eventName || '');
    fd.set('_kind', 'object');
    if (DOC_ID) fd.set('_id', DOC_ID);
    const body = new URLSearchParams();
    fd.forEach((v, k) => {
      if (k.startsWith('_fc_')) return; // skip file-content helper fields
      if (typeof v !== 'string') { body.append(k, ''); return; }
      // If a _fc_ counterpart exists with content, prefer it over the path
      const fcEl = form.querySelector('[name="_fc_' + k + '"]');
      if (fcEl && fcEl.value) { body.append(k, fcEl.value); }
      else { body.append(k, v); }
    });
    // Команда над ТЧ: подмешать индексы выделенных строк (_tp_selected) по
    // имени ТЧ из extraParams._tp.
    if (extraParams && extraParams._tp) {
      // Plan 48: check if SlickGrid exists for this TP
      var obg = (window._obGrids || {})[extraParams._tp];
      if (obg) {
        // getSelectedRows бросает «Selection model is not set», если модель
        // выделения не установлена (плагин не завендорен). Командам подбора/
        // пересчёта/очистки выделение не нужно — гасим ошибку и шлём пусто.
        var sel = [];
        try { sel = obg.grid.getSelectedRows() || []; } catch (e) { sel = []; }
        body.append('_tp_selected', sel.join(','));
      } else {
        // Legacy: read from DOM checkboxes
        const tbody = document.getElementById('tp-body-' + extraParams._tp);
        if (tbody) {
          const sel = [];
          Array.prototype.forEach.call(tbody.rows, (tr, i) => {
            const cb = tr.querySelector('._tp-sel');
            if (cb && cb.checked) sel.push(i);
          });
          body.append('_tp_selected', sel.join(','));
        }
      }
    }
    if (extraParams) {
      Object.keys(extraParams).forEach(k => body.append(k, extraParams[k]));
    }
    try {
      const res = await fetch(URL, {
        method: 'POST',
        body: body,
        headers: { 'Content-Type': 'application/x-www-form-urlencoded; charset=utf-8' },
        credentials: 'same-origin'
      });
      const data = await res.json();
      // Подбор фазы 1: сервер вернул pickerData — открыть диалог, не трогая
      // ТЧ (её обновит фаза 2 после «Перенести»).
      if (data.pickerData) {
        (data.messages || []).forEach(m => flash(m, 'ok'));
        if (data.error) flash(data.error, 'err');
        openItemPicker(data.pickerData, elementName);
        return;
      }
      if (Object.prototype.hasOwnProperty.call(data, 'conditionalCss')) applyFormConditionalCSS(data.conditionalCss);
      window.applyTableParts(data.tableparts);
      applyValues(data.values);
      applyChoiceList(elementName, data.choiceList);
      applyFormTables(data.formTables);
      (data.messages || []).forEach(m => flash(m, 'ok'));
      if (data.error) flash(data.error, 'err');
    } catch (e) {
      flash('Сетевая ошибка: ' + (e && e.message ? e.message : e), 'err');
    }
   } catch (e) {
      // Синхронные ошибки (obGridSync, сборка формы) больше не «глотаются»
      // как unhandled rejection — показываем баннер, чтобы причина была видна.
      flash('Ошибка формы: ' + (e && e.message ? e.message : e), 'err');
   }
  };

  // Отслеживание «грязной» формы — чтобы Esc/закрытие спрашивало подтверждение
  // только при наличии несохранённых изменений. Плюс пометка несохранённого
  // документа звёздочкой в заголовке вкладки браузера (аналог «*» в 1С) и
  // предупреждение при ЛЮБОМ уходе со страницы — крестик, клик по ссылке,
  // закрытие/обновление вкладки.
  window._obFormDirty = false;
  var _obBaseTitle = document.title;
  function _obMarkDirty(){
    window._obFormDirty = true;
    if (document.title.charAt(0) !== '●') document.title = '● ' + _obBaseTitle;
  }
  document.addEventListener('input',  function(e){ if (e.target && e.target.closest && e.target.closest('#main-form')) _obMarkDirty(); }, true);
  document.addEventListener('change', function(e){ if (e.target && e.target.closest && e.target.closest('#main-form')) _obMarkDirty(); }, true);
  // Сохранение формы (Записать/Провести) сбрасывает «грязный» флаг — иначе
  // beforeunload спрашивал бы подтверждение даже при штатной отправке.
  var _obMainForm = document.getElementById('main-form');
  if (_obMainForm) _obMainForm.addEventListener('submit', function(){ window._obFormDirty = false; });
  window.addEventListener('beforeunload', function(e){
    if (window._obFormDirty) { e.preventDefault(); e.returnValue = ''; return ''; }
  });

  // Esc — отмена незаконченного ввода / закрытие формы (как в 1С). Порядок:
  //   1) открыт модальный диалог (подбор/выбор ссылки) → закрыть его;
  //   2) активен редактор ячейки грида → отменить правку ячейки (форму НЕ
  //      закрываем);
  //   3) фокус в поле ввода → снять фокус (отменить ввод);
  //   4) иначе → закрыть форму (с подтверждением, если были изменения).
  //
  // ВАЖНО: слушатель в ФАЗЕ ПЕРЕХВАТА (capture=true). В фазе всплытия SlickGrid
  // успевал отменить правку РАНЬШЕ нас, editor-lock становился неактивным, и мы
  // ошибочно закрывали документ прямо из редактирования ячейки.
  document.addEventListener('keydown', function(e){
    if (e.key !== 'Escape' && e.keyCode !== 27) return;
    var modal = document.getElementById('_item-picker-modal') || document.getElementById('_ref-picker-modal');
    if (modal) { modal.remove(); e.preventDefault(); e.stopPropagation(); return; }
    var grids = window._obGrids || {};
    for (var tp in grids) {
      var lock = grids[tp].grid && grids[tp].grid.getEditorLock && grids[tp].grid.getEditorLock();
      if (lock && lock.isActive()) { lock.cancelCurrentEdit(); e.preventDefault(); e.stopPropagation(); return; }
    }
    var ae = document.activeElement;
    if (ae && /^(INPUT|SELECT|TEXTAREA)$/.test(ae.tagName) && !ae.readOnly && ae.type !== 'submit' && ae.type !== 'button') {
      ae.blur(); e.preventDefault(); e.stopPropagation(); return;
    }
    var cancel = document.querySelector('a.btn-cancel');
    if (cancel) {
      if (window._obFormDirty && !confirm('Данные были изменены и не записаны. Закрыть форму?')) {
        e.preventDefault(); e.stopPropagation(); return;
      }
      e.preventDefault(); e.stopPropagation(); cancel.click();
    }
  }, true);
})();
</script>

{{/* Общие JS-функции addTpRow / openRefCreate / openRefPicker — те же,
     что в обычной auto-форме, чтобы "+" рядом со ссылкой и "Добавить
     строку" в ТЧ работали и в managed-форме. */}}

{{/* addVtRow — JS для добавления строки в ValueTable (формовый атрибут-таблица). */}}
<script>
function addVtRow(vtName, fields) {
  var tbody = document.getElementById("vt-body-" + vtName);
  if (!tbody) return;
  var idx = tbody.rows.length;
  var tr = document.createElement("tr");
  var fieldTypes = (tbody.getAttribute("data-vt-fields") || "").split(",");
  fields.forEach(function(fn, i) {
    var td = document.createElement("td");
    var inp = document.createElement("input");
    inp.name = "vt." + vtName + "." + idx + "." + fn;
    var ft = (fieldTypes[i] || "").split("|")[1] || "";
    if (ft === "number") {
      inp.type = "number"; inp.step = "any";
      inp.setAttribute("data-vt-num", fn);
    } else if (ft === "bool") {
      inp.type = "checkbox"; inp.value = "true";
    } else {
      inp.type = "text";
    }
    td.appendChild(inp);
    tr.appendChild(td);
  });
  var tdDel = document.createElement("td");
  var btn = document.createElement("button");
  btn.type = "button"; btn.className = "del-btn"; btn.textContent = "×";
  btn.onclick = function(){ tr.remove(); };
  tdDel.appendChild(btn);
  tr.appendChild(tdDel);
  tbody.appendChild(tr);
}
</script>

{{if hasGridTP .Form}}
<script src="/vendor/slickgrid/slick.core.js"></script>
<script src="/vendor/slickgrid/slick.interactions.js"></script>
<script src="/vendor/slickgrid/slick.grid.js"></script>
<script src="/vendor/slickgrid/slick.dataview.js"></script>
<script src="/vendor/slickgrid/slick.editors.js"></script>
<script src="/vendor/slickgrid/slick.formatters.js"></script>
<script>
// SlickGrid initializer for managed-form table parts (plan 48).
// Grids are stored in window._obGrids = {tpName: {grid, dataView, columns}}.
(function(){
  window._obGrids = window._obGrids || {};

  // resizeGrid — пересчитывает геометрию грида и растягивает колонки на всю
  // ширину контейнера. Критично для ТЧ на вкладках/в свёрнутых группах: при
  // инициализации в скрытом (display:none) контейнере SlickGrid меряет ширину
  // 0 и autosizeColumns схлопывает колонки в узкую полоску. Поэтому ресайзим
  // только видимый грид (offsetParent != null) — и повторяем при показе вкладки.
  function resizeGrid(g) {
    if (!g || !g.div || g.div.offsetParent === null) return;
    g.grid.resizeCanvas();
    g.grid.autosizeColumns();
    updateTotals(g); // footer-ячейки пересоздаются при re-render — обновляем суммы
  }

  // updateTotals — строка итогов (footer row SlickGrid). Для каждой числовой
  // колонки считает сумму по всем строкам модели и выводит в footer-ячейку,
  // выровненную по колонке. В первой колонке — подпись «Итого».
  function updateTotals(g) {
    // Полностью defensive: итоги — вторичны и НИКОГДА не должны ломать
    // перерисовку грида (этот код вызывается из подписчиков onRowCountChanged).
    try {
      if (!g || !g.grid || typeof g.grid.getOptions !== "function" || !g.grid.getOptions().showFooterRow) return;
      if (typeof g.grid.getFooterRowColumn !== "function") return;
      var items = g.dataView.getItems();
      var cols = g.columnsMeta || [];
      for (var i = 0; i < cols.length; i++) {
        var c = cols[i];
        var node = g.grid.getFooterRowColumn(c.id);
        if (!node) continue;
        if (c.type === "number") {
          var sum = 0;
          for (var r = 0; r < items.length; r++) {
            var n = Number(String(items[r][c.id] == null ? "" : items[r][c.id]).replace(",", "."));
            if (!isNaN(n)) sum += n;
          }
          node.innerHTML = '<span style="display:block;text-align:right;font-weight:600;font-variant-numeric:tabular-nums">' +
            sum.toLocaleString("ru-RU", {minimumFractionDigits: 0, maximumFractionDigits: 2}) + "</span>";
        } else {
          node.innerHTML = (i === 0) ? '<span style="color:#64748b">Итого</span>' : "";
        }
      }
    } catch (e) { if (window.console) console.warn("updateTotals:", e); }
  }
  // _obResizeGrids — пройтись по всем гридам и пересчитать видимые. Вызывается
  // при переключении вкладок managed-формы и при ресайзе окна.
  window._obResizeGrids = function() {
    var grids = window._obGrids || {};
    for (var tp in grids) resizeGrid(grids[tp]);
  };

  // Serialize ref value: extract id from {id,_label} object or return raw value
  function refId(v) {
    if (v && typeof v === "object") {
      if (v.id !== undefined) return v.id;
      if (v.UUID !== undefined) return v.UUID; // сериализованный *interpreter.Ref
    }
    return (v == null) ? "" : String(v);
  }

  // Custom ref editor with dropdown search and picker button (plan 48, phase 4).
  function ObRefEditor(refField, refOptsList, args) {
    var wrapper, input, dropBtn, list, isOpen = false, selectedId = '', defaultValue = '', pickerInterval = null;

    function label(id) {
      for (var k = 0; k < refOptsList.length; k++) {
        if (String(refOptsList[k].id) === String(id)) return refOptsList[k]._label;
      }
      return '';
    }

    this.init = function() {
      wrapper = document.createElement('div');
      wrapper.style.cssText = 'display:flex;align-items:center;width:100%;height:100%;gap:2px';

      input = document.createElement('input');
      input.type = 'text';
      input.style.cssText = 'flex:1;border:none;outline:none;padding:2px 4px;font-size:13px;min-width:0';
      var cur = args.item[args.column.field];
      defaultValue = cur;
      selectedId = (cur && typeof cur === 'object') ? (cur.id || '') : (cur || '');
      input.value = label(selectedId) || String(selectedId);

      dropBtn = document.createElement('button');
      dropBtn.type = 'button';
      dropBtn.textContent = '…';
      dropBtn.title = 'Выбрать из списка';
      dropBtn.style.cssText = 'border:none;background:none;cursor:pointer;font-size:12px;padding:0 4px;color:#2563eb;flex-shrink:0';

      wrapper.appendChild(input);
      wrapper.appendChild(dropBtn);
      args.container.appendChild(wrapper);

      // Dropdown list
      list = document.createElement('div');
      list.style.cssText = 'position:absolute;z-index:9999;background:#fff;border:1px solid #e2e8f0;border-radius:6px;box-shadow:0 4px 12px rgba(0,0,0,.12);max-height:200px;overflow-y:auto;min-width:160px;font-size:13px';

      function buildList(filter) {
        list.innerHTML = '';
        var f = (filter || '').toLowerCase();
        var found = false;
        for (var k = 0; k < refOptsList.length; k++) {
          var opt = refOptsList[k];
          if (f && opt._label.toLowerCase().indexOf(f) < 0) continue;
          found = true;
          var item = document.createElement('div');
          item.textContent = opt._label;
          item.setAttribute('data-id', opt.id);
          item.style.cssText = 'padding:6px 10px;cursor:pointer;border-bottom:1px solid #f1f5f9';
          item.addEventListener('mouseenter', function() { this.style.background = '#eef2ff'; });
          item.addEventListener('mouseleave', function() { this.style.background = ''; });
          (function(o) {
            item.addEventListener('mousedown', function(e) {
              e.preventDefault();
              selectedId = o.id;
              input.value = o._label;
              closeList();
              args.commitChanges();
            });
          })(opt);
          list.appendChild(item);
        }
        if (!found) {
          var empty = document.createElement('div');
          empty.textContent = 'Ничего не найдено';
          empty.style.cssText = 'padding:8px 10px;color:#94a3b8;font-style:italic';
          list.appendChild(empty);
        }
      }

      function openList() {
        if (isOpen) return;
        isOpen = true;
        buildList(input.value);
        var rect = input.getBoundingClientRect();
        list.style.left = rect.left + 'px';
        list.style.top = rect.bottom + 'px';
        list.style.width = Math.max(rect.width, 160) + 'px';
        document.body.appendChild(list);
      }

      function closeList() {
        if (!isOpen) return;
        isOpen = false;
        if (list.parentElement) list.remove();
      }

      input.addEventListener('focus', openList);
      input.addEventListener('input', function() {
        if (isOpen) buildList(input.value);
        else openList();
      });
      input.addEventListener('blur', function() { setTimeout(closeList, 150); });

      dropBtn.addEventListener('click', function(e) {
        e.preventDefault();
        e.stopPropagation();
        // Use existing openRefPicker mechanism
        var selEl = document.createElement('select');
        selEl.setAttribute('data-ref-entity', args.column.refEntity || '');
        refOptsList.forEach(function(opt) {
          var o = document.createElement('option');
          o.value = opt.id; o.textContent = opt._label;
          selEl.appendChild(o);
        });
        selEl.value = selectedId;
        // Monkey-patch: picker callback sets our cell value
        var origPicker = window.openRefPicker;
        window.openRefPicker(selEl);
        // Poll for picker result via the select value
        pickerInterval = setInterval(function() {
          var modal = document.getElementById('_ref-picker-modal');
          if (!modal) {
            clearInterval(pickerInterval);
            // Picker closed - check if value changed
            var newVal = selEl.value;
            if (newVal !== selectedId) {
              selectedId = newVal;
              input.value = label(newVal);
              args.commitChanges();
            }
          }
        }, 300);
      });

      input.focus();
      input.select();
    };

    this.destroy = function() {
      // ВАЖНО: closeList объявлена внутри init() и здесь недоступна — обращение
      // к ней бросало ReferenceError, из-за чего SlickGrid не мог разрушить
      // редактор, commitCurrentEdit падал и активная ячейка «залипала» (нельзя
      // было перейти на другую). Закрываем выпадающий список напрямую по
      // editor-scoped переменным list/isOpen.
      isOpen = false;
      // Гасим polling-таймер пикера: иначе он переживёт редактор и попытается
      // закоммитить значение в уже уничтоженную ячейку.
      if (pickerInterval) { clearInterval(pickerInterval); pickerInterval = null; }
      if (list && list.parentElement) list.remove();
      if (wrapper && wrapper.parentElement) wrapper.remove();
    };
    this.focus = function() { input && input.focus(); };
    this.getValue = function() { return selectedId; };
    this.setValue = function(val) { selectedId = val; input.value = label(val); };
    this.loadValue = function(item) {
      var v = item[args.column.field];
      defaultValue = v;
      selectedId = (v && typeof v === 'object') ? (v.id || '') : (v || '');
      input.value = label(selectedId);
    };
    this.serializeValue = function() { return selectedId; };
    this.applyValue = function(item, state) { item[args.column.field] = state; };
    this.isValueChanged = function() { return selectedId !== defaultValue; };
    this.validate = function() { return {valid: true, msg: null}; };
    this.init();
  }

  // Custom number editor with locale-aware parsing (plan 48, phase 3).
  function ObNumberEditor(args) {
    var input, defaultValue;
    this.init = function() {
      input = document.createElement('input');
      input.type = 'text';
      input.className = 'editor-text';
      input.style.cssText = 'width:100%;height:100%;border:none;outline:none;padding:2px 4px;text-align:right;font-variant-numeric:tabular-nums;font-size:13px';
      args.container.appendChild(input);
      input.focus();
      var val = args.item[args.column.field];
      defaultValue = val;
      if (val != null && val !== '') input.value = String(val).replace('.', ',');
      input.select();
    };
    this.destroy = function() { input.remove(); };
    this.focus = function() { input.focus(); };
    this.getValue = function() { return input.value; };
    this.setValue = function(val) { input.value = val; };
    this.loadValue = function(item) {
      var v = item[args.column.field];
      defaultValue = v;
      input.value = (v != null && v !== '') ? String(v).replace('.', ',') : '';
    };
    this.serializeValue = function() {
      var v = input.value.replace(/\s/g, '').replace(',', '.');
      return v === '' ? '' : v;
    };
    this.applyValue = function(item, state) { item[args.column.field] = state; };
    this.isValueChanged = function() {
      return input.value !== ((defaultValue != null) ? String(defaultValue).replace('.', ',') : '');
    };
    this.validate = function() {
      var v = input.value.replace(/\s/g, '').replace(',', '.');
      if (v !== '' && isNaN(Number(v))) return {valid: false, msg: 'Введите число'};
      return {valid: true, msg: null};
    };
    this.init();
  }

  // Build SlickGrid columns from metadata with editors (plan 48, phase 3).
  function buildColumns(colsMeta, refOpts, enumLabels) {
    var columns = [];
    for (var i = 0; i < colsMeta.length; i++) {
      var c = colsMeta[i];
      var col = {id: c.id, name: c.name, field: c.id, width: 120, resizable: true, sortable: true};
      if (c.type === "number") {
        col.cssClass = "ob-num";
        col.editor = ObNumberEditor;
        // Подсветка значений: отрицательные — красным (недостачи, возвраты,
        // отклонения); колонка «дефицит» при положительном значении — оранжевым.
        var warnPos = /дефицит/i.test(c.id || "");
        col.formatter = (function(warn){ return function(row, cell, value) {
          if (value == null || value === "") return "";
          var n = Number(String(value).replace(',', '.'));
          if (isNaN(n)) return "<span>" + value + "</span>";
          var s = n.toLocaleString("ru-RU", {minimumFractionDigits:0, maximumFractionDigits:2});
          if (n < 0) return "<span style='color:#dc2626;font-weight:600'>" + s + "</span>";
          if (warn && n > 0) return "<span style='color:#ea580c;font-weight:600'>" + s + "</span>";
          return "<span>" + s + "</span>";
        }; })(warnPos);
      } else if (c.ref) {
        col.cssClass = "ob-ref";
        col.editor = (function(refField, refOptsList) {
          return ObRefEditor.bind(null, refField, refOptsList);
        })(c.id, refOpts[c.id] || []);
        col.formatter = (function(refField) {
          return function(row, cell, value, colDef, dataCtx) {
            if (!value) return "";
            // Ссылка может прийти объектом: {id,_label} (клиентский формат) или
            // {UUID,Name} (сериализованный *interpreter.Ref, если просочился мимо
            // serializeValue). Извлекаем подпись/идентификатор — иначе String(obj)
            // дал бы «[object Object]».
            if (typeof value === "object") {
              if (value._label) return "<span>" + value._label + "</span>";
              if (value.Name)   return "<span>" + value.Name + "</span>";
              value = (value.id !== undefined) ? value.id : (value.UUID !== undefined ? value.UUID : "");
            }
            var opts = (refOpts && refOpts[refField]) || [];
            for (var k = 0; k < opts.length; k++) {
              if (String(opts[k].id) === String(value)) return "<span>" + opts[k]._label + "</span>";
            }
            return "<span>" + String(value) + "</span>";
          };
        })(c.id);
      } else if (c.enum) {
        col.cssClass = "ob-enum";
        col.editor = Slick.Editors.Text;
        col.formatter = (function(enumField) {
          return function(row, cell, value) {
            if (value == null || value === "") return "";
            var labels = (enumLabels && enumLabels[enumField]) || {};
            var lbl = labels[value];
            return "<span>" + (lbl != null ? lbl : String(value)) + "</span>";
          };
        })(c.id);
      } else if (c.type === "bool") {
        col.cssClass = "ob-bool";
        col.editor = Slick.Editors.Checkbox;
        col.formatter = function(row, cell, value) {
          var on = (value === true || value === "true" || value === 1 || value === "1");
          return on ? '<span style="color:#16a34a;font-weight:700">✓</span>'
                    : '<span style="color:#cbd5e1">—</span>';
        };
      } else {
        col.editor = Slick.Editors.Text;
      }
      columns.push(col);
    }
    return columns;
  }

  // Serialize ref value
  function refId(v) {
    if (v && typeof v === "object") {
      if (v.id !== undefined) return v.id;
      if (v.UUID !== undefined) return v.UUID; // сериализованный *interpreter.Ref
    }
    return (v == null) ? "" : String(v);
  }

  function copyFormGridStyleKeys(source, item) {
    item._obRowClass = source && source._form_row_class ? String(source._form_row_class) : "";
    item._obCellClasses = source && source._form_cell_classes ? source._form_cell_classes : {};
    return item;
  }

  function formGridItemMetadata(row) {
    var item = this.getItem(row);
    if (!item) return null;
    var meta = null;
    if (item._obRowClass) {
      meta = meta || {};
      meta.cssClasses = item._obRowClass;
    }
    var cc = item._obCellClasses || {};
    var columns = {};
    Object.keys(cc).forEach(function(field) {
      if (cc[field]) columns[field] = {cssClass: String(cc[field])};
    });
    if (Object.keys(columns).length) {
      meta = meta || {};
      meta.columns = columns;
    }
    return meta;
  }

  // Serialize all grid data into hidden inputs (for form submit / obFire)
  window.obGridSync = function() {
    var grids = window._obGrids || {};
    for (var tpName in grids) {
      var g = grids[tpName];
      // Сериализуем в исходном порядке (_ord), а не в порядке текущей
      // сортировки отображения — чтобы сортировка «для просмотра» не меняла
      // порядок строк в сохраняемом документе.
      var items = g.dataView.getItems().slice().sort(function(a, b) {
        return (a._ord || 0) - (b._ord || 0);
      });
      var rows = items.map(function(item) {
        var row = {};
        var cols = g.columnsMeta || [];
        for (var i = 0; i < cols.length; i++) {
          row[cols[i].id] = refId(item[cols[i].id]);
        }
        return row;
      });
      var inp = document.getElementById("tp-json-" + tpName);
      if (inp) inp.value = JSON.stringify(rows);
    }
  };

  function gridCellEventParams(tpName, args, columns, dataView) {
    var rowIndex = -1;
    if (args && args.item && dataView && typeof dataView.getItems === "function") {
      var items = dataView.getItems().slice().sort(function(a, b) {
        return (a._ord || 0) - (b._ord || 0);
      });
      for (var i = 0; i < items.length; i++) {
        if (items[i] && items[i].id === args.item.id) { rowIndex = i; break; }
      }
    }
    if (rowIndex < 0 && args && typeof args.row === "number") rowIndex = args.row;
    var cellIndex = (args && typeof args.cell === "number") ? args.cell : -1;
    var colName = "";
    if (cellIndex >= 0 && columns && columns[cellIndex]) colName = columns[cellIndex].field || columns[cellIndex].id || "";
    return {
      _tp: tpName,
      _tp_row: rowIndex >= 0 ? String(rowIndex) : "",
      _tp_row_number: rowIndex >= 0 ? String(rowIndex + 1) : "",
      _tp_col: colName,
      _tp_col_index: cellIndex >= 0 ? String(cellIndex) : ""
    };
  }

  // Add empty row to grid
  // obFireRowEvent — серверное событие строки ТЧ (ПриДобавленииСтроки/
  // ПриУдаленииСтроки). Дёргается после добавления/удаления строки, но только
  // если у элемента ТЧ объявлен обработчик (флаг data-sg-rowadd/data-sg-rowdel),
  // — иначе впустую гоняли бы сеть. Путь тот же, что у ПриИзменении: obFire
  // синхронизирует ТЧ (obGridSync) и применяет values/tableparts из ответа.
  window.obFireRowEvent = function(tpName, attr, eventName) {
    var div = document.getElementById("sg-" + tpName);
    if (!div || div.getAttribute(attr) !== "1") return;
    var elName = div.getAttribute("data-sg-el") || tpName;
    if (window.obFire) window.obFire(elName, eventName, {_tp: tpName});
  };

  window.obGridAddRow = function(tpName) {
    var g = (window._obGrids || {})[tpName];
    if (!g) return;
    var _lk = g.grid.getEditorLock && g.grid.getEditorLock();
    if (_lk && _lk.isActive()) _lk.commitCurrentEdit();
    var nextId = 0, nextOrd = 0;
    g.dataView.getItems().forEach(function(it) {
      if (it.id >= nextId) nextId = it.id + 1;
      if ((it._ord || 0) >= nextOrd) nextOrd = (it._ord || 0) + 1;
    });
    var item = {id: nextId, _ord: nextOrd};
    var cols = g.columnsMeta || [];
    for (var i = 0; i < cols.length; i++) item[cols[i].id] = "";
    g.dataView.addItem(item);
    window._obFormDirty = true;
    g.grid.invalidate();
    // scrollRowIntoView ждёт ИНДЕКС отображаемой строки, не id записи —
    // после удалений они расходятся. Берём индекс из getRowById.
    var rowIdx = g.dataView.getRowById(nextId);
    if (rowIdx !== undefined && g.columns.length > 0) {
      g.grid.scrollRowIntoView(rowIdx);
      g.grid.setActiveCell(rowIdx, 0);
      g.grid.editActiveCell();
    }
    obFireRowEvent(tpName, "data-sg-rowadd", "ПриДобавленииСтроки");
  };

  // Delete selected row from grid
  window.obGridDelRow = function(tpName) {
    var g = (window._obGrids || {})[tpName];
    if (!g) return;
    var _lk = g.grid.getEditorLock && g.grid.getEditorLock();
    if (_lk && _lk.isActive()) _lk.commitCurrentEdit();
    // Без плагина выделения getSelectedRows бросает исключение — удаляем
    // активную (текущую) строку, как в обычной таблице.
    var sel = [];
    try { sel = g.grid.getSelectedRows() || []; } catch (e) { sel = []; }
    if (!sel.length) { var ac = g.grid.getActiveCell(); if (ac) sel = [ac.row]; }
    if (!sel.length) return;
    var items = g.dataView.getItems();
    var toRemove = sel.map(function(r) { return items[r]; }).filter(Boolean);
    for (var i = 0; i < toRemove.length; i++) g.dataView.deleteItem(toRemove[i].id);
    window._obFormDirty = true;
    g.grid.invalidate();
    g.grid.setSelectedRows([]);
    obFireRowEvent(tpName, "data-sg-rowdel", "ПриУдаленииСтроки");
  };

  // SlickGrid-aware applyTableParts. Оборачивает window.applyTableParts (DOM-
  // версию из obFire-IIFE): для ТЧ с гридом обновляет модель грида, для
  // остальных вызывает origApplyTP. Активную ячейку сохраняем, чтобы серверный
  // пересчёт сумм (Р2) не сбивал курсор при быстром вводе с клавиатуры.
  var origApplyTP = window.applyTableParts;
  window.applyTableParts = function(tps) {
    if (!tps) return;
    var grids = window._obGrids || {};
    Object.keys(tps).forEach(function(tpName) {
      var g = grids[tpName];
      if (!g) return;
      var active = g.grid.getActiveCell();
      var rows = tps[tpName] || [];
      var cols = g.columnsMeta || [];
      var items = rows.map(function(r, idx) {
        var item = {id: idx, _ord: idx};
        // == null (не || "") — иначе число 0 / false терялись бы как пустая строка.
        for (var i = 0; i < cols.length; i++) item[cols[i].id] = (r[cols[i].id] == null ? "" : r[cols[i].id]);
        copyFormGridStyleKeys(r, item);
        return item;
      });
      g.dataView.setItems(items);
      g.grid.invalidate();
      if (active && active.row < items.length) {
        g.grid.setActiveCell(active.row, active.cell);
      }
      updateTotals(g);
    });
    if (origApplyTP) origApplyTP(tps);
  };

  // setupGrid инициализирует один грид. Вынесено из цикла в отдельную функцию,
  // чтобы каждый грид замыкал свои grid/dataView/tpName (иначе при нескольких
  // ТЧ на форме все подписки замыкали бы последний грид — классический баг var
  // в цикле).
  function setupGrid(div) {
    var tpName = div.getAttribute("data-sg-tp");
    if (window._obGrids[tpName]) return;

    // ВАЖНО: jsJSON от nil-слайса даёт литерал "null", а не "[]". Без защиты
    // от null для пустой табличной части (новый документ) JSON.parse("null")
    // вернёт null и rowsRaw.map бросит TypeError ДО создания грида — грид не
    // создавался и не регистрировался, из-за чего add/удаление/подбор тихо не
    // работали именно в новых документах.
    var colsRaw = JSON.parse(div.getAttribute("data-sg-cols") || "[]") || [];
    var refOpts = JSON.parse(div.getAttribute("data-sg-ref") || "null") || {};
    var enumLabels = JSON.parse(div.getAttribute("data-sg-enum") || "null") || {};
    var rowsRaw = JSON.parse(div.getAttribute("data-sg-rows") || "[]") || [];

    var columns = buildColumns(colsRaw, refOpts, enumLabels);
    // _ord — исходный порядок строки. Клиентская сортировка меняет ПОРЯДОК
    // ОТОБРАЖЕНИЯ (dataView.sort), но при сохранении (obGridSync) строки
    // сериализуются по _ord — чтобы сортировка «для просмотра» не переставляла
    // строки документа (у табличной части порядок значим).
    var items = rowsRaw.map(function(r, idx) {
      var item = {id: idx, _ord: idx};
      // == null (не || "") — иначе сохранённое числовое 0 грузилось бы пустым.
      for (var i = 0; i < colsRaw.length; i++) item[colsRaw[i].id] = (r[colsRaw[i].id] == null ? "" : r[colsRaw[i].id]);
      copyFormGridStyleKeys(r, item);
      return item;
    });

    var dataView = new Slick.Data.DataView();
    dataView.getItemMetadata = formGridItemMetadata;
    dataView.setItems(items);

    var readOnly = div.getAttribute("data-sg-ro") === "1";
    var options = {
      enableCellNavigation: true,
      enableColumnReorder: false,
      editable: !readOnly,
      // autoEdit:false — как в 1С: клик выделяет ячейку, в редактирование входим
      // по Enter / двойному клику / началу ввода (а не сразу по одиночному клику).
      autoEdit: false,
      autoHeight: false,
      rowHeight: 28,
      headerRowHeight: 30,
      syncColumnCellResize: true,
      enableTextSelectionOnCells: true,
      enableAddRow: false,
      multiSelect: true,
      // ВАЖНО: footer-строке нужны ОБЕ опции — createFooterRow создаёт DOM,
      // showFooterRow показывает его. Только showFooterRow без createFooterRow
      // роняет рендер (обращение к несуществующему _footerRowScroller[0]).
      createFooterRow: true,
      showFooterRow: true,
      footerRowHeight: 28
    };

    var grid = new Slick.Grid(div, dataView, columns, options);

    // Регистрируем грид СРАЗУ после создания — ДО подписок ниже. Если что-то в
    // подписках бросит исключение, грид всё равно в window._obGrids, и кнопки
    // (добавить/удалить строку, подбор, сериализация) продолжат работать.
    window._obGrids[tpName] = {
      grid: grid, dataView: dataView, columns: columns,
      columnsMeta: colsRaw, refOpts: refOpts, div: div
    };

   try {
    dataView.onRowCountChanged.subscribe(function() { grid.updateRowCount(); grid.render(); updateTotals(window._obGrids[tpName]); });
    dataView.onRowsChanged.subscribe(function(e, args) { grid.invalidateRows(args.rows); grid.render(); });

    // Сортировка по клику на заголовок (колонки sortable). Порядок ОТОБРАЖЕНИЯ;
    // на сохранение не влияет (см. _ord и obGridSync). Числа сортируются как
    // числа, ссылки — по наименованию (_label), остальное — по строке.
    grid.onSort.subscribe(function(e, args) {
      var field = args.sortCol.field;
      var sign = args.sortAsc ? 1 : -1;
      var meta = null;
      for (var i = 0; i < colsRaw.length; i++) { if (colsRaw[i].id === field) { meta = colsRaw[i]; break; } }
      var isNum = meta && meta.type === "number";
      var isRef = meta && meta.ref;
      function keyOf(it) {
        var v = it[field];
        if (isNum) { var n = Number(String(v == null ? "" : v).replace(",", ".")); return isNaN(n) ? -Infinity : n; }
        if (isRef) {
          var id = (v && typeof v === "object") ? (v.id || "") : (v == null ? "" : v);
          var opts = refOpts[field] || [];
          for (var k = 0; k < opts.length; k++) { if (String(opts[k].id) === String(id)) return String(opts[k]._label).toLowerCase(); }
          return String(id).toLowerCase();
        }
        return String(v == null ? "" : v).toLowerCase();
      }
      dataView.sort(function(a, b) { var ka = keyOf(a), kb = keyOf(b); return ka > kb ? sign : (ka < kb ? -sign : 0); });
      grid.invalidate(); grid.render();
    });

    // Клиентский авторасчёт Сумма = Количество × Цена — ТОЛЬКО при явном opt-in
    // (data-sg-autosum ← auto_sum: true у ТЧ в форме). Без флага обычная ТЧ с
    // колонками Цена/Количество/Сумма больше НЕ связывается автоматически (#215.1).
    // Колонки определяем ПО ИМЕНИ (а не «ровно 3 числовые»), чтобы работало и
    // когда есть доп. числовые колонки (НДС и т.п.). Это мгновенная подсказка;
    // точный пересчёт (НДС, итоги — decimal) делает сервер при записи/проведении.
    function num(v) { var n = Number(String(v == null ? "" : v).replace(/\s/g, "").replace(",", ".")); return isNaN(n) ? 0 : n; }
    function findColId(variants) {
      for (var i = 0; i < colsRaw.length; i++) {
        var nm = String(colsRaw[i].name || colsRaw[i].id).toLowerCase();
        for (var j = 0; j < variants.length; j++) { if (nm === variants[j]) return colsRaw[i].id; }
      }
      return null;
    }
    var autoSum = div.getAttribute("data-sg-autosum") === "1";
    var colQty = autoSum ? findColId(["количество", "кол-во", "колво", "кол", "quantity", "qty"]) : null;
    var colPrice = autoSum ? findColId(["цена", "price"]) : null;
    var colSum = autoSum ? findColId(["сумма", "amount", "sum"]) : null;
    grid.onCellChange.subscribe(function(e, args) {
      window._obFormDirty = true;
      if (colQty && colPrice && colSum && args && args.item && args.cell != null) {
        var changed = columns[args.cell] && columns[args.cell].field;
        // Пересчитываем сумму при правке количества/цены; саму сумму не трогаем,
        // если её правят вручную.
        if (changed === colQty || changed === colPrice) {
          args.item[colSum] = num(args.item[colQty]) * num(args.item[colPrice]);
          grid.invalidateRow(args.row); grid.render();
        }
      }
      updateTotals(window._obGrids[tpName]);
    });

    // Delete key removes selected rows
    grid.onKeyDown.subscribe(function(e) {
      if (e.key === 'Delete' && !grid.getEditorLock().isActive()) {
        var sel = [];
        try { sel = grid.getSelectedRows() || []; } catch (er) { sel = []; }
        if (!sel.length) { var ac = grid.getActiveCell(); if (ac) sel = [ac.row]; }
        if (sel.length) {
          var its = dataView.getItems();
          var toRemove = sel.map(function(r) { return its[r]; }).filter(Boolean);
          for (var i = 0; i < toRemove.length; i++) dataView.deleteItem(toRemove[i].id);
          window._obFormDirty = true;
          grid.invalidate();
          obFireRowEvent(tpName, "data-sg-rowdel", "ПриУдаленииСтроки");
          e.stopImmediatePropagation();
        }
      }
    });

    // План 48 (Р2): серверный пересчёт зависимых колонок (Сумма = Кол × Цена)
    // через round-trip. Дёргаем obFire('ПриИзменении') только если у элемента
    // ТЧ есть такой обработчик (data-sg-recalc) — иначе впустую гоняли бы сеть
    // на каждый ввод. Debounce 250 мс коалесцирует быстрые правки (вопрос O3).
    // Деньги считаются на сервере (decimal), клиент лишь отображает результат.
    if (div.getAttribute("data-sg-recalc") === "1") {
      var elName = div.getAttribute("data-sg-el") || tpName;
      var recalcTimer = null;
      grid.onCellChange.subscribe(function(e, args) {
        var params = gridCellEventParams(tpName, args, columns, dataView);
        if (recalcTimer) clearTimeout(recalcTimer);
        recalcTimer = setTimeout(function() {
          if (window.obFire) window.obFire(elName, "ПриИзменении", params);
        }, 250);
      });
    }

   } catch (err) {
     // Подписки/настройка дали сбой. Грид уже зарегистрирован выше, поэтому
     // базовые операции работают. Показываем причину, чтобы не гадать вслепую.
     if (window.console) console.error("SlickGrid setup error [" + tpName + "]:", err);
     if (window.obFlash) window.obFlash("Грид «" + tpName + "»: " + (err && err.message ? err.message : err), "err");
   }

    // Растягиваем колонки на ширину контейнера, если грид уже виден. Для ТЧ на
    // скрытой вкладке ресайз отложится до её показа (см. хук на .managed-tab-btn).
    resizeGrid(window._obGrids[tpName]);
  }

  // Initialize all grids
  function initGrids() {
    var divs = document.querySelectorAll(".ob-grid[data-sg-tp]");
    for (var d = 0; d < divs.length; d++) setupGrid(divs[d]);
  }

  // При переключении вкладки managed-формы её содержимое становится видимым —
  // пересчитываем гриды (inline-onclick кнопки уже переключил display, наш
  // делегированный слушатель на document отработает после него в фазе всплытия).
  if (!window._obTabHook) {
    window._obTabHook = true;
    document.addEventListener("click", function(e) {
      var btn = e.target && e.target.closest ? e.target.closest(".managed-tab-btn") : null;
      if (btn) setTimeout(window._obResizeGrids, 0);
    });
    var _rt = null;
    window.addEventListener("resize", function() {
      if (_rt) clearTimeout(_rt);
      _rt = setTimeout(window._obResizeGrids, 100);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initGrids);
  } else {
    initGrids();
  }
})();</script>
{{end}}

{{/* Вложения к записи (issue #152) — тот же UI, что и в авто-форме. */}}
{{template "ob-attachments" .}}

{{template "form-shared-js" .}}

{{/* Авто-вызов ПриОткрытииФормы при загрузке страницы. Без этого
     серверный handler на event="ПриОткрытии" никогда не запустится —
     браузер не генерирует такое событие. План 37, этап 8. */}}
{{if hasFormHandler .Form "ПриОткрытии"}}
<script>
document.addEventListener('DOMContentLoaded', function(){
  // setTimeout 0 → даём obFire-IIFE выше зарегистрировать window.obFire.
  setTimeout(function(){ if (window.obFire) obFire('', 'ПриОткрытии'); }, 0);
});
</script>
{{end}}

{{/* Стиль активной вкладки. Inline-style на кнопке управляет базовым
     видом, а .active переопределяет цвет/border (выше по специфичности
     не получается без !important — поэтому используем именно класс). */}}
<style>
.managed-tab-btn{padding:8px 14px;border:none;background:none;cursor:pointer;font-size:13px;color:#64748b;border-bottom:2px solid transparent;margin-bottom:-2px;font-family:inherit}
.managed-tab-btn:hover{color:#1a4a80;background:#f5f8ff}
.managed-tab-btn.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}
/* Мобильная адаптация управляемых форм (этап 45): заголовки вкладок скроллятся
   по горизонтали, поля в одну колонку. ТЧ по умолчанию рендерятся SlickGrid'ом
   (.ob-grid) — у него собственный горизонтальный скролл; .tp-table (режим
   NoGrid и виртуальные ТЧ) скроллятся общим правилом main table из tplHead. */
@media (max-width:820px){
  .managed-tab-headers{overflow-x:auto;flex-wrap:nowrap;-webkit-overflow-scrolling:touch}
  .managed-tab-btn{white-space:nowrap}
  .form-group-box{padding:10px 12px}
  .ob-grid{max-width:100%}
}
</style>

{{/* Запоминание активной вкладки. После POST-сабмита (например, ошибка
     проведения) форма перерисовывается с нуля и сбрасывается на первую
     вкладку «Основное» — пользователь терял контекст и не понимал, что
     ошибка относится к ТЧ на другой вкладке. Сохраняем индекс активной
     вкладки в sessionStorage по pathname+имя группы вкладок и
     восстанавливаем при загрузке. */}}
<script>
(function(){
  function tabKey(tabs){ return "obtab:" + location.pathname + ":" + (tabs.getAttribute("data-tabs") || ""); }
  // Сохраняем выбор при клике (наш слушатель — после inline-onclick кнопки).
  document.addEventListener("click", function(e){
    var btn = e.target && e.target.closest ? e.target.closest(".managed-tab-btn") : null;
    if (!btn) return;
    var tabs = btn.closest(".managed-tabs");
    if (!tabs) return;
    try { sessionStorage.setItem(tabKey(tabs), btn.getAttribute("data-tab-idx") || "0"); } catch(_){}
  });
  // Восстанавливаем при загрузке. Кликаем по кнопке — это переключит и display,
  // и (через _obTabHook) пересчитает гриды на показанной вкладке.
  function restore(){
    var groups = document.querySelectorAll(".managed-tabs");
    for (var i = 0; i < groups.length; i++) {
      var tabs = groups[i];
      var idx;
      try { idx = sessionStorage.getItem(tabKey(tabs)); } catch(_){ idx = null; }
      if (idx == null || idx === "0") continue;
      var btn = tabs.querySelector('.managed-tab-btn[data-tab-idx="' + idx + '"]');
      if (btn) btn.click();
    }
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", restore);
  else restore();
})();
</script>

</main>
</body></html>
{{end}}
`
