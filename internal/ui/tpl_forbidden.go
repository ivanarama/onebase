package ui

const tplForbidden = `
{{define "page-forbidden"}}
{{template "head" .}}{{template "nav" .}}
<main style="display:flex;align-items:center;justify-content:center;min-height:60vh">
<div style="text-align:center;max-width:400px">
  <div style="font-size:56px;margin-bottom:16px">⛔</div>
  <h2 style="font-size:22px;font-weight:700;color:var(--ob-text);margin-bottom:8px">{{t $.Lang "Доступ запрещён"}}</h2>
  <p style="color:var(--ob-text-3);font-size:14px;margin-bottom:28px">{{if .Reason}}{{.Reason}}{{else}}{{t $.Lang "У вас нет прав для просмотра этого раздела."}}{{end}}</p>
  <div style="display:flex;gap:12px;justify-content:center">
    <a data-ob-history-back href="#" class="btn" style="background:var(--ob-btn-2-bg);color:var(--ob-text-2)">{{t $.Lang "← Назад"}}</a>
    <a href="/ui" class="btn btn-primary">{{t $.Lang "На главную"}}</a>
  </div>
</div>
</main>
<script>
document.addEventListener('click', function(e) {
  var back = e.target.closest && e.target.closest('[data-ob-history-back]');
  if (!back) return;
  e.preventDefault();
  history.back();
});
</script>
</div></body></html>
{{end}}
`
