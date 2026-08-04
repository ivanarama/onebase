package ui

// Страница результатов глобального поиска (план 82). Ссылка результата ведёт
// прямо в карточку объекта — это и есть смысл сценария «помню текст, не помню
// где искать».
const tplSearch = `
{{define "page-search"}}
{{template "head" .}}{{template "nav" .}}
<main style="max-width:900px">
<h2>{{t $.Lang "Поиск по базе"}}</h2>
<form method="get" action="/ui/search" style="margin-bottom:16px;display:flex;gap:8px">
  <input type="text" name="q" value="{{.Q}}" autofocus placeholder="{{t $.Lang "Что ищем?"}}"
    style="flex:1;padding:9px 14px;border:1px solid #d0d7e3;border-radius:6px;font-size:14px">
  <button class="btn btn-primary" type="submit">{{t $.Lang "Найти"}}</button>
</form>

{{if .Searched}}
  {{if .Results}}
  <div class="search-results">
    {{range .Results}}
    <a class="search-hit" href="/ui/{{.Kind}}/{{lower .Entity}}/{{.ID}}">
      <div class="search-hit-title{{if .DeletionMark}} search-hit-deleted{{end}}">{{.Title}}</div>
      <div class="search-hit-meta">
        <span class="search-hit-entity">{{.Entity}}</span>
        {{if .DeletionMark}}<span class="search-hit-flag">{{t $.Lang "Помечен на удаление"}}</span>{{end}}
        {{if .IsDocument}}{{if .Posted}}<span class="search-hit-posted">{{t $.Lang "Проведён"}}</span>{{end}}{{end}}
      </div>
    </a>
    {{end}}
  </div>
  {{if .HasMore}}
  <div style="margin-top:14px">
    <a class="btn" href="/ui/search?q={{.Q}}&amp;cursor={{.NextCursor}}">{{t $.Lang "Показать ещё"}}</a>
  </div>
  {{end}}
  {{else}}
  <p style="color:#64748b">{{t $.Lang "Ничего не найдено."}}</p>
  {{end}}
{{end}}
</main>
</div></body></html>
{{end}}
`
