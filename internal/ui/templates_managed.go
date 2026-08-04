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
        <button type="button" class="managed-tab-btn{{if eq $i 0}} active{{end}}" data-tab-idx="{{$i}}">
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
          <select id="ref-{{$fn}}" name="{{$fn}}" style="flex:1" data-ref-entity="{{$f.RefEntity}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $f.InlineCreateEnabled false}} data-ref-allow-create="1"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
            <option value="">{{if $el.ReadOnly}}—{{else}}— выбрать —{{end}}</option>
            {{range index $ctx.RefOptions $fn}}
            <option value="{{index . "id"}}" {{if eq (index . "id") (index $ctx.Values $fn)}}selected{{end}}>{{index . "_label"}}</option>
            {{end}}
          </select>
          {{/* Нередактируемому полю кнопка подбора не нужна — выбирать нечего, а
               серая «…» рядом заставляет читать значение как незаполненный ввод.
               «Открыть карточку» остаётся и остаётся РАБОЧЕЙ: посмотреть связанный
               объект — не редактирование, и на readonly-поле это как раз то, что
               нужно (открыть звонок, клиента, документ-основание). */}}
          {{if not $el.ReadOnly}}
          <button type="button" data-ob-ref-picker="ref-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px">…</button>
          {{end}}
          {{/* Открывать нечего, пока значение не выбрано — на пустом поле кнопка
               только занимала место и сбивала выравнивание соседей. */}}
          {{if index $ctx.Values $fn}}
          <button type="button" data-ob-ref-current="ref-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px" title="Открыть карточку">🔍</button>
          {{end}}
        </div>
      {{else if isEnum (str $f.Type)}}
        <select name="{{$fn}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
          <option value="">{{if $el.ReadOnly}}—{{else}}— выбрать —{{end}}</option>
          {{range index $ctx.EnumOptions $fn}}
          <option value="{{.Value}}" {{if eq .Value (index $ctx.Values $fn)}}selected{{end}}>{{.Label}}</option>
          {{end}}
        </select>
      {{else if eq (str $f.Type) "date"}}
        <input type="datetime-local" name="{{$fn}}" value="{{index $ctx.Values $fn}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
      {{else if eq (str $f.Type) "bool"}}
        <select name="{{$fn}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
          <option value="false" {{if eq (index $ctx.Values $fn) "false"}}selected{{end}}>Нет</option>
          <option value="true" {{if eq (index $ctx.Values $fn) "true"}}selected{{end}}>Да</option>
        </select>
      {{else if eq (str $f.Type) "number"}}
        <input type="text" inputmode="decimal" pattern="[+-]?([0-9]+([.,][0-9]+)?|[.,][0-9]+)" name="{{$fn}}" value="{{index $ctx.Values $fn}}" title="Введите число; десятичный разделитель — запятая или точка"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
      {{else if isRichText (str $f.Type)}}
        {{/* textarea — скрытое form-backing поле; Quill (этап 2) монтируется на
             .richtext-editor и синхронизирует HTML обратно перед submit. Без JS
             textarea остаётся рабочим (прогрессивное улучшение). */}}
        <textarea name="{{$fn}}" class="richtext-field" rows="8" style="width:100%"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}>{{index $ctx.Values $fn}}</textarea>
        {{if not $el.ReadOnly}}<div class="richtext-editor"></div>{{end}}
      {{else if eq (str $el.Type) "file"}}
        <div style="display:flex;gap:6px;align-items:center">
          <input type="text" name="{{$fn}}" id="file-path-{{$fn}}" placeholder="Путь к файлу или выберите …" style="flex:1"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}>
          <textarea name="_fc_{{$fn}}" id="file-content-{{$fn}}" style="display:none"></textarea>
          <input type="file" id="file-pick-{{$fn}}" style="display:none" data-ob-file-pick-path="file-path-{{$fn}}" data-ob-file-pick-content="file-content-{{$fn}}">
          <button type="button" data-ob-file-trigger="file-pick-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;white-space:nowrap" title="Выбрать файл">…</button>
        </div>
      {{else if $el.Multiline}}
        <textarea name="{{$fn}}" rows="5" style="width:100%"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>{{index $ctx.Values $fn}}</textarea>
      {{else}}
        <input type="text" name="{{$fn}}" value="{{index $ctx.Values $fn}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}{{if $el.Mask}} pattern="{{$el.Mask}}"{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
      {{end}}
    {{else if eq (str $el.Type) "file"}}
      {{/* Поле не найдено в Entity, но элемент объявлен как file */}}
      <div style="display:flex;gap:6px;align-items:center">
        <input type="text" name="{{$fn}}" id="file-path-{{$fn}}" placeholder="Путь к файлу или выберите …" style="flex:1"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}>
        <textarea name="_fc_{{$fn}}" id="file-content-{{$fn}}" style="display:none"></textarea>
        <input type="file" id="file-pick-{{$fn}}" style="display:none" data-ob-file-pick-path="file-path-{{$fn}}" data-ob-file-pick-content="file-content-{{$fn}}">
        <button type="button" data-ob-file-trigger="file-pick-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;white-space:nowrap" title="Выбрать файл">…</button>
      </div>
    {{else}}
      {{$attr := attrByName $ctx.Form $fn}}
      {{if and $attr (attrRefEntity $attr.TypeRef) (index $ctx.RefOptions $fn)}}
        {{/* Реквизит формы ссылочного типа — рабочий пикер выбора (фикс B): select
             из вариантов справочника (mergeFormLocalRefOptions) + кнопка подбора.
             Условие требует ещё и загруженных опций: mergeFormLocalRefOptions
             зовётся только из renderEntityForm и только для save:false с сущностью
             в реестре. Формы обработок рендерят page-managed-form напрямую
             (handlers_processors.go), и без этой проверки поле там превращалось в
             пустой select, теряющий текущее значение при записи. Нет опций —
             остаётся прежний текстовый ввод со значением. */}}
        <div style="display:flex;gap:6px;align-items:center">
          <select id="ref-{{$fn}}" name="{{$fn}}" style="flex:1" data-ref-entity="{{attrRefEntity $attr.TypeRef}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
            <option value="">{{if $el.ReadOnly}}—{{else}}— выбрать —{{end}}</option>
            {{range index $ctx.RefOptions $fn}}
            <option value="{{index . "id"}}" {{if eq (index . "id") (index $ctx.Values $fn)}}selected{{end}}>{{index . "_label"}}</option>
            {{end}}
          </select>
          <button type="button" data-ob-ref-picker="ref-{{$fn}}"{{if $el.ReadOnly}} disabled{{end}} style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px">…</button>
        </div>
      {{else if $attr}}
        {{/* Объявленный скалярный реквизит формы — обычное поле ввода. Жёлтая
             подсветка ниже адресована ОПЕЧАТКЕ в data_path; штатный реквизит
             формы работает полноценно (обработчик читает его голым именем и как
             Объект.<Реквизит>), и предупреждать о нём не о чем. */}}
        <input type="text" name="{{$fn}}" value="{{index $ctx.Values $fn}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}{{if $el.Mask}} pattern="{{$el.Mask}}"{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
      {{else}}
        {{/* Ни поля сущности, ни реквизита формы с таким именем — почти всегда
             опечатка в data_path: подсвечиваем, чтобы это не осталось незамеченным. */}}
        <input type="text" name="{{$fn}}" value="{{index $ctx.Values $fn}}" placeholder="{{$fn}}" style="background:#fef9c3"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}
          title="Реквизит формы '{{$el.DataPath}}' не найден среди полей сущности"{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
      {{end}}
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
    <select name="{{$fn}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if hasHandler $el "НачалоВыбора"}} data-el="{{$el.Name}}" data-ob-list-choice="{{$el.Name}}"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
      <option value="">{{if $el.ReadOnly}}—{{else}}— выбрать —{{end}}</option>
      {{range index $ctx.ChoiceOptions $el.Name}}
      <option value="{{.Value}}" {{if eq .Value (index $ctx.Values $fn)}}selected{{end}}>{{.Label}}</option>
      {{end}}
    </select>
    {{if $el.Hint}}<small style="color:#94a3b8;font-size:11px">{{$el.Hint}}</small>{{end}}
  </div>
{{else if eq (str $el.Kind) "Флажок"}}
  {{$fn := dpField $el.DataPath}}
  {{$hChg := hasHandler $el "ПриИзменении"}}
  <div class="form-group managed-checkbox" style="display:flex;align-items:center;gap:8px">
    {{/* ПриИзменении у флажка работает так же, как у остальных полей: без
         data-ob-fire-change обработчик «поставил галку → выполнилось действие»
         молча не вызывался. */}}
    <input type="checkbox" id="cb-{{$fn}}" name="{{$fn}}" value="true"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}
      {{if eq (index $ctx.Values $fn) "true"}}checked{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
    <label for="cb-{{$fn}}" style="margin-bottom:0;cursor:pointer">{{fieldTitleRU $el.TitleMap $fn}}</label>
  </div>
{{else if eq (str $el.Kind) "Надпись"}}
  <div class="form-decoration" style="padding:6px 0;color:#475569;font-size:13px">
    {{fieldTitleRU $el.TitleMap $el.Name}}
  </div>
{{else if eq (str $el.Kind) "Кнопка"}}
  <button type="button" class="btn btn-secondary managed-btn"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.HotKey}} data-ob-hotkey="{{$el.HotKey}}" aria-keyshortcuts="{{$el.HotKey}}"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if hasHandler $el "Нажатие"}} data-ob-fire-click="{{$el.Name}}"{{end}}>
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
      {{if .AccessKey}}accesskey="{{.AccessKey}}" {{end}}{{if .HotKey}}data-ob-hotkey="{{.HotKey}}" aria-keyshortcuts="{{.HotKey}}" {{end}}{{if $el.ReadOnly}}disabled{{end}}{{if hasHandler . "Нажатие"}} data-ob-fire-click="{{.Name}}" data-ob-fire-tp="{{$tpName}}"{{end}}>
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
      data-ob-grid-add="{{$tpName}}">+ Добавить строку</button>
    <button type="button" class="btn btn-sm" style="background:#fee2e2;color:#991b1b"
      data-ob-grid-del="{{$tpName}}">− Удалить строку</button>
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
                <option value="">{{if $el.ReadOnly}}—{{else}}— выбрать —{{end}}</option>
                {{range index $tpRef $f.Name}}
                <option value="{{index . "id"}}" {{if eq (str (index . "id")) (refID $v)}}selected{{end}}>{{index . "_label"}}</option>
                {{end}}
              </select>
              <button type="button" data-ob-ref-picker="closest" style="padding:4px 8px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="Выбрать из списка">...</button>
              <button type="button" data-ob-ref-current="closest" style="padding:4px 7px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="Открыть карточку">🔍</button>
            </div>
          {{else if eq (str $f.Type) "number"}}
            <input type="number" step="any" name="tp.{{$tpName}}.{{$i}}.{{$f.Name}}" value="{{$v}}" data-tp-num="{{$f.Name}}" data-ob-recalc-tp-row>
          {{else}}
            <input type="text" name="tp.{{$tpName}}.{{$i}}.{{$f.Name}}" value="{{$v}}" data-ob-recalc-tp-row>
          {{end}}
        </td>
        {{end}}
        <td><button type="button" class="del-btn" data-ob-remove-row>×</button></td>
      </tr>
    {{end}}
    </tbody>
    <tfoot id="tp-foot-{{$tpName}}" class="tp-footer" style="display:none"><tr>
      {{if $tpCmds}}<td></td>{{end}}
      {{range $f := $tpMeta.Fields}}{{if eq (str $f.Type) "number"}}<td class="tp-total" data-tp-total="{{$tpName}}.{{$f.Name}}" style="text-align:right;font-variant-numeric:tabular-nums">0</td>{{else}}<td></td>{{end}}{{end}}<td></td>
    </tr></tfoot>
  </table>
  <button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin:0 0 12px"
    data-ob-add-tp="{{$tpName}}">
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
      {{if .AccessKey}}accesskey="{{.AccessKey}}" {{end}}{{if .HotKey}}data-ob-hotkey="{{.HotKey}}" aria-keyshortcuts="{{.HotKey}}" {{end}}data-ob-fire-click="{{.Name}}" data-ob-fire-tp="{{$tpName}}">
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
        <td><button type="button" class="del-btn" data-ob-remove-row>×</button></td>
      </tr>
    {{end}}
    </tbody>
  </table>
  <button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin:0 0 12px"
    data-ob-add-vt="{{$tpName}}">
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
    <input type="date" name="{{$fn}}" value="{{if ge (len $dv) 10}}{{slice $dv 0 10}}{{else}}{{$dv}}{{end}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} readonly{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
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
      <select name="{{$fn}}"{{if $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}>
        <option value="">{{if $el.ReadOnly}}—{{else}}— выбрать —{{end}}</option>
        {{if $enum}}
          {{range index $ctx.EnumOptions $fn}}<option value="{{.Value}}" {{if eq .Value $cur}}selected{{end}}>{{.Label}}</option>{{end}}
        {{else}}
          {{range $el.Options}}<option value="{{.ValueStr}}" {{if eq .ValueStr $cur}}selected{{end}}>{{.Label}}</option>{{end}}
        {{end}}
      </select>
    {{else}}
      <div class="switch-options" style="display:flex;flex-wrap:wrap;gap:12px;padding:4px 0">
        {{if $enum}}
          {{range $i, $opt := index $ctx.EnumOptions $fn}}<label style="display:inline-flex;align-items:center;gap:5px;cursor:pointer"><input type="radio" name="{{$fn}}" value="{{$opt.Value}}"{{if and (eq $i 0) $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if eq $opt.Value $cur}} checked{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}> {{$opt.Label}}</label>{{end}}
        {{else}}
          {{range $i, $opt := $el.Options}}<label style="display:inline-flex;align-items:center;gap:5px;cursor:pointer"><input type="radio" name="{{$fn}}" value="{{$opt.ValueStr}}"{{if and (eq $i 0) $el.AccessKey}} accesskey="{{$el.AccessKey}}"{{end}}{{if eq $opt.ValueStr $cur}} checked{{end}}{{if $el.ReadOnly}} disabled{{end}}{{if $hChg}} data-ob-fire-change="{{$el.Name}}"{{end}}> {{$opt.Label}}</label>{{end}}
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
{{if .TabTitle}}<meta name="ob-tab-title" content="{{.TabTitle}}">{{end}}
<style>
.managed-group-horizontal>.managed-group-body{display:flex;flex-wrap:wrap;gap:12px;align-items:flex-start}
/* Поле в горизонтальной группе не растягивается на всю строку: иначе одинокое
   поле уезжало во всю ширину, а кнопка рядом с ним — к правому краю экрана. */
.managed-group-horizontal>.managed-group-body>.form-group{flex:0 1 260px;min-width:180px;margin-bottom:0}
.managed-group-horizontal>.managed-group-body>.form-decoration,.managed-group-horizontal>.managed-group-body>button{flex:0 0 auto}
/* Кнопка формы: отступы задаются классом, а не inline-стилем — иначе правило
   выравнивания в горизонтальной группе ниже проигрывало бы по приоритету. */
.managed-btn{margin:6px 4px 6px 0}
/* Кнопка встаёт вровень с полем, а не с его меткой: метка занимает
   line-height 18px + margin-bottom 5px (см. label в общем стиле), а разницу
   высот кнопки (30px) и поля (39px) добираем до общей средней линии. */
.managed-group-horizontal>.managed-group-body>.form-group>label{line-height:18px}
.managed-group-horizontal>.managed-group-body>.managed-btn{align-self:flex-start;margin:27px 0 0 0}
/* Флажок без метки сверху выравниваем по той же линии, что и поля рядом. */
.managed-group-horizontal>.managed-group-body>.form-group.managed-checkbox{align-self:flex-start;margin-top:27px}
/* Нередактируемое поле — это ЗНАЧЕНИЕ, а не ввод: убираем стрелку списка и
   гасим рамку, чтобы результат команды не читался как незаполненное поле.
   Флажок и переключатель исключены: appearance:none стирает сам квадратик,
   и нередактируемая галка превращалась в подпись без индикатора. */
.form-group input[readonly],.form-group input:disabled:not([type=checkbox]):not([type=radio]),.form-group select:disabled,.form-group textarea[readonly]{
  background:#f8fafc;border-color:#eef2f7;color:#334155;cursor:default;opacity:1;-webkit-appearance:none;appearance:none}
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
    {{if .IsProcessor}}{{.Processor.DisplayName $.Lang}}{{else}}{{if .IsNew}}{{t $.Lang "Создать"}} — {{.Entity.DisplayName $.Lang}}{{else}}{{if .RecordTitle}}{{.RecordTitle}}{{else}}{{t $.Lang "Редактировать"}} — {{.Entity.DisplayName $.Lang}}{{end}}{{end}}{{end}}
    <span style="font-size:11px;color:#10b981;background:#d1fae5;padding:2px 8px;border-radius:10px;vertical-align:middle;font-weight:500" title="Управляемая форма из forms/{{if .IsProcessor}}{{lower .Processor.Name}}{{else}}{{lower .Entity.Name}}{{end}}/">◇ managed</span>
  </h2>
  {{if .IsPopup}}
  <a href="#" data-ob-ref-cancel title="Закрыть" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
  {{else}}
  <a href="{{if .IsProcessor}}/ui/{{else}}/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}{{end}}" data-ob-close-tab title="Закрыть" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
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
        <span style="color:#16a34a;font-weight:600;font-size:13px">✓ {{t $.Lang "Проведён"}}</span>
      {{else}}
        <span style="color:#94a3b8;font-size:13px">{{t $.Lang "Не проведён"}}</span>
      {{end}}
    {{end}}
  {{end}}
  {{if .CanWrite}}<button class="btn btn-secondary" type="submit" name="_action" value="" form="main-form">{{t $.Lang "Записать"}}</button>{{end}}
  {{if .Entity.Posting}}
    {{if ne (index .Values "deletion_mark") "true"}}
      {{if .CanPost}}<button class="btn btn-primary" type="submit" name="_action" value="post" form="main-form">{{if .Entity.PostCaption}}{{.Entity.PostCaption}}{{else}}{{t $.Lang "Провести"}}{{end}}</button>{{end}}
      {{if and .CanPost (not .Entity.PostAndCloseHidden)}}<button class="btn btn-post" type="submit" name="_action" value="post_and_close" form="main-form">{{if .Entity.PostCaption}}{{.Entity.PostCaption}} и закрыть{{else}}{{t $.Lang "Провести и закрыть"}}{{end}}</button>{{end}}
    {{end}}
    {{if not .IsNew}}
      {{if eq (index .Values "posted") "true"}}
        {{if $.CanUnpost}}<button class="btn btn-sm" style="background:#e2e8f0;color:#374151" form="form-unpost" type="submit">{{t $.Lang "Отменить проведение"}}</button>{{end}}
      {{end}}
    {{end}}
  {{end}}
  {{if and .CanDelete (not .IsNew) (eq (index .Values "deletion_mark") "true")}}
    <form method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/delete?mark=0" style="display:inline">
      <button class="btn btn-sm btn-secondary" type="submit">{{t $.Lang "Снять пометку на удаление"}}</button>
    </form>
  {{end}}
  {{if not .IsNew}}
    <a href="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/history" class="btn btn-sm btn-secondary">{{t $.Lang "История"}}</a>
    {{if or .AllPrintForms .HasPrintProc}}
    <div style="position:relative">
      <button type="button" class="btn btn-sm btn-secondary" data-ob-toggle-next>{{t $.Lang "Печать"}} ▾</button>
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
      <button type="button" class="btn btn-sm btn-secondary" data-ob-toggle-next>{{t $.Lang "Ввести на основании"}} ▾</button>
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
          data-ob-confirm="{{if .IsAdmin}}Удалить запись навсегда?{{else}}Пометить запись на удаление?{{end}}" style="margin-left:auto">
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
<form id="main-form" method="POST" data-ob-grid-sync {{if .IsProcessor}}action="/ui/processor/{{lower .Processor.Name}}" enctype="multipart/form-data"{{end}}>
{{if and (not .IsNew) (index .Values "_version")}}<input type="hidden" name="_version" value="{{index .Values "_version"}}">{{end}}
{{if .IsPopup}}<input type="hidden" name="_popup" value="1">{{end}}

{{$ctx := .}}
{{if .FormCommands}}
<div class="managed-command-bar" style="display:flex;gap:6px;flex-wrap:wrap;margin:0 0 16px;padding-bottom:12px;border-bottom:1px solid #e2e8f0">
  {{range .FormCommands}}
  <button type="button" class="btn btn-secondary" style="margin:0" data-ob-fire-click="{{.Name}}">{{fieldTitleRU .Title .Name}}</button>
  {{end}}
</div>
{{end}}
{{range .Form.Elements}}
  {{template "managed-element" (dict "El" . "Ctx" $ctx)}}
{{end}}

<div style="margin-top:16px">
  {{if .IsPopup}}
  {{if .CanWrite}}<button class="btn btn-primary" type="submit" name="_action" value="" form="main-form">{{t $.Lang "Записать и выбрать"}}</button>{{end}}
  <a href="#" data-ob-ref-cancel class="btn btn-cancel">Отмена</a>
  {{else if .IsProcessor}}
  {{/* Кнопка «Выполнить» скрыта: managed-форма использует свои кнопки */}}
  <a href="/ui/" class="btn btn-cancel">Отмена</a>
  {{else}}
  {{/* «Записать» здесь НЕ дублируем: она уже есть в командной панели формы, и
       две одинаковые кнопки на одном экране заставляют гадать, чем они
       отличаются. Внизу остаётся только выход из формы. */}}
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" class="btn btn-cancel">Отмена</a>
  {{end}}
</div>
</form>
</div>

{{/* ── Рантайм событий managed-формы (план 37, этап 8) ──────────────────
     Статический код живёт в /static/managed.js; ниже только JSON bootstrap. */}}
{{if .IsProcessor}}
<script type="application/json" id="ob-managed-config">{{jsJSON (dict
  "kind" "processor"
  "entity" .Processor.Name
  "url" (printf "/ui/processor/%s/form-event" (lower .Processor.Name))
  "docId" ""
  "autoOpen" (hasFormHandler .Form "ПриОткрытии")
)}}</script>
{{else}}
<script type="application/json" id="ob-managed-config">{{jsJSON (dict
  "kind" (lower (str .Entity.Kind))
  "entity" .Entity.Name
  "url" (printf "/ui/%s/%s/form-event" (lower (str .Entity.Kind)) .Entity.Name)
  "docId" .ID
  "autoOpen" (hasFormHandler .Form "ПриОткрытии")
  "formAttrs" (formAttrNames .Form .Entity)
)}}</script>
{{end}}
<script type="application/json" id="ob-managed-tp-ref-opts">{{jsJSON .TPRefOptions}}</script>
<script type="application/json" id="ob-managed-tp-enum-labels">{{jsJSON .TPEnumLabels}}</script>
<script type="application/json" id="ob-managed-tp-enum-order">{{jsJSON .TPEnumOrder}}</script>


{{if hasGridTP .Form}}
<script src="/vendor/slickgrid/slick.core.js"></script>
<script src="/vendor/slickgrid/slick.interactions.js"></script>
<script src="/vendor/slickgrid/slick.grid.js"></script>
<script src="/vendor/slickgrid/slick.dataview.js"></script>
<script src="/vendor/slickgrid/slick.editors.js"></script>
<script src="/vendor/slickgrid/slick.formatters.js"></script>
<script src="/static/managed.js"></script>
{{else}}
<script src="/static/managed.js"></script>
{{end}}

{{/* Вложения к записи (issue #152) — тот же UI, что и в авто-форме. */}}
{{template "ob-attachments" .}}

{{template "form-shared-js" .}}

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

{{/* Запоминание активной вкладки живёт в /static/managed.js. */}}

</main>
</body></html>
{{end}}
`
