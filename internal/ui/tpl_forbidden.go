package ui

const tplForbidden = `
{{define "page-forbidden"}}
{{template "head" .}}{{template "nav" .}}
<main style="display:flex;align-items:center;justify-content:center;min-height:60vh">
<div style="text-align:center;max-width:400px">
  <div style="font-size:56px;margin-bottom:16px">⛔</div>
  <h2 style="font-size:22px;font-weight:700;color:#1e293b;margin-bottom:8px">{{t $.Lang "Доступ запрещён"}}</h2>
  <p style="color:#64748b;font-size:14px;margin-bottom:28px">{{t $.Lang "У вас нет прав для просмотра этого раздела."}}</p>
  <div style="display:flex;gap:12px;justify-content:center">
    <a data-ob-history-back href="#" class="btn" style="background:#e2e8f0;color:#475569">{{t $.Lang "← Назад"}}</a>
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
