package ui

const tplQueryBuilder = `
{{define "page-query-builder"}}
{{template "head" .}}{{template "nav" .}}
<main style="max-width:100%">
<h2>{{t $.Lang "Конструктор запросов"}}</h2>
<div style="display:grid;grid-template-columns:400px 1fr;gap:20px;align-items:start">

<!-- LEFT: builder panels -->
<div>

<!-- Source -->
<div class="card" style="margin-bottom:12px">
<h3 style="margin-top:0">{{t $.Lang "Источник данных"}}</h3>
<select id="qb-src" data-ob-qb-source style="width:100%;margin-bottom:8px">
  <option value="">{{t $.Lang "— выбрать —"}}</option>
</select>
<div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">
  <span style="font-size:12px;color:var(--ob-text-3);flex-shrink:0;width:70px">{{t $.Lang "Псевдоним:"}}</span>
  <input id="qb-main-alias" type="text" placeholder="{{t $.Lang "напр. Прод"}}" data-ob-qb-main-alias
    style="width:110px;font-size:12px;border:1px solid var(--ob-border);border-radius:4px;padding:2px 6px">
  <span style="font-size:11px;color:var(--ob-text-4)">({{t $.Lang "обязателен при JOIN"}})</span>
</div>
<div id="qb-vt-param" style="display:none;margin-top:4px">
  <label style="font-size:12px;color:var(--ob-text-3)">{{t $.Lang "Параметры виртуальной таблицы"}}</label>
  <input id="qb-vt-param-val" type="text" data-ob-qb-vt-param style="width:100%;margin-top:4px" placeholder="{{t $.Lang "например: &НаДату"}}">
</div>
</div>

<!-- Joins -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Соединения (JOIN)"}}</h3>
  <button class="btn btn-sm" data-ob-qb-action="add-join"
    style="background:var(--ob-chip-hover);color:var(--ob-link-2);padding:2px 8px;font-size:12px">{{t $.Lang "+ Соединение"}}</button>
</div>
<div id="qb-joins">
  <p style="font-size:12px;color:var(--ob-text-4);margin:0" id="qb-joins-hint">{{t $.Lang "Нет соединений"}}</p>
</div>
</div>

<!-- Fields -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Поля (ВЫБРАТЬ)"}}</h3>
  <div style="display:flex;gap:4px">
    <button class="btn btn-sm" data-ob-qb-action="all-fields" data-ob-qb-all-fields="true" style="background:var(--ob-btn-2-bg);color:var(--ob-text-2);padding:2px 8px;font-size:12px">{{t $.Lang "Все"}}</button>
    <button class="btn btn-sm" data-ob-qb-action="all-fields" data-ob-qb-all-fields="false" style="background:var(--ob-btn-2-bg);color:var(--ob-text-2);padding:2px 8px;font-size:12px">{{t $.Lang "Сбросить"}}</button>
  </div>
</div>
<div id="qb-fields-list" style="max-height:260px;overflow-y:auto"></div>
</div>

<!-- Where -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Условия (ГДЕ)"}}</h3>
  <button class="btn btn-sm" data-ob-qb-action="add-cond"
    style="background:var(--ob-chip-hover);color:var(--ob-link-2);padding:2px 8px;font-size:12px">{{t $.Lang "+ Условие"}}</button>
</div>
<div id="qb-conds"></div>
</div>

<!-- Order -->
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Сортировка"}}</h3>
  <button class="btn btn-sm" data-ob-qb-action="add-order"
    style="background:var(--ob-chip-hover);color:var(--ob-link-2);padding:2px 8px;font-size:12px">{{t $.Lang "+ Поле"}}</button>
</div>
<div id="qb-orders"></div>
</div>

<!-- Params -->
<div class="card">
<h3 style="margin-top:0">{{t $.Lang "Параметры"}}</h3>
<p style="font-size:12px;color:var(--ob-text-3);margin-bottom:8px">{{t $.Lang "Автообнаружение из условий по &ИмяПараметра"}}</p>
<div id="qb-params" style="font-size:13px">—</div>
</div>
</div><!-- /LEFT -->

<!-- RIGHT: generated text -->
<div>
<div class="card" style="margin-bottom:12px">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "Текст запроса"}}</h3>
  <button data-ob-qb-action="copy-query"
    style="background:var(--ob-green-100);color:var(--ob-green-800);border:none;border-radius:5px;padding:3px 12px;cursor:pointer;font-size:12px">{{t $.Lang "Копировать"}}</button>
</div>
<textarea id="qb-query-out" rows="16" readonly
  style="width:100%;font-family:monospace;font-size:13px;border:1px solid var(--ob-border);border-radius:6px;padding:10px;background:var(--ob-surface-2);resize:vertical"></textarea>
</div>

<div class="card">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
  <h3 style="margin:0">{{t $.Lang "DSL-фрагмент (вставить в модуль)"}}</h3>
  <button data-ob-qb-action="copy-dsl"
    style="background:var(--ob-green-100);color:var(--ob-green-800);border:none;border-radius:5px;padding:3px 12px;cursor:pointer;font-size:12px">{{t $.Lang "Копировать"}}</button>
</div>
<textarea id="qb-dsl-out" rows="14" readonly
  style="width:100%;font-family:monospace;font-size:13px;border:1px solid var(--ob-border);border-radius:6px;padding:10px;background:var(--ob-surface-2);resize:vertical"></textarea>
</div>
</div><!-- /RIGHT -->

</div><!-- /grid -->
</main>

<script type="application/json" id="ob-query-builder-schema">{{.Schema}}</script>
<script src="/static/query-builder.js"></script>
</div></body></html>
{{end}}
`
