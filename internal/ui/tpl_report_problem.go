package ui

// Страница «Сообщить об ошибке» (план 116).
//
// Два состояния в одном шаблоне: форма и предпросмотр готового отчёта.
// Предпросмотр — не украшение, а единственная защита от утечки: в текст
// ошибки могли попасть данные, введённые пользователем, вычистить их
// автоматически нельзя, поэтому человек видит отчёт целиком и правит его до
// отправки. Формулировки кнопок — только «Скачать» и «Скопировать»: платформа
// ничего никуда не шлёт, и пользователь не должен решить обратное.

const tplReportProblem = `
{{define "page-report-problem"}}
{{template "head" .}}{{template "nav" .}}
<main style="max-width:820px">
<h2>{{t $.Lang "Сообщить об ошибке"}}</h2>

{{if .Preview}}
<div class="card">
  <p style="color:#475569;font-size:14px;margin-bottom:10px">
    {{t $.Lang "Отчёт готов. Проверьте текст: в сообщениях об ошибках могли оказаться данные, которые вы не хотите отправлять. Всё, что здесь написано, можно исправить или удалить."}}
  </p>
  <form method="post" action="/ui/report-problem/download" style="margin:0">
    <textarea id="ob-report-text" name="report" rows="22" spellcheck="false"
      style="width:100%;padding:10px 12px;border:1px solid #d0d7e3;border-radius:6px;font-family:ui-monospace,Consolas,monospace;font-size:12.5px;line-height:1.5">{{.Preview}}</textarea>
    <div style="display:flex;gap:10px;margin-top:12px;flex-wrap:wrap">
      <button class="btn btn-primary" type="submit">{{t $.Lang "Скачать файл"}}</button>
      <button class="btn" type="button" id="ob-report-copy">{{t $.Lang "Скопировать текст"}}</button>
      <a class="btn" href="/ui/report-problem" style="background:#e2e8f0;color:#475569">{{t $.Lang "Изменить описание"}}</a>
      <span id="ob-report-copied" style="align-self:center;color:#166534;font-size:13px;display:none">{{t $.Lang "Скопировано"}}</span>
    </div>
  </form>
</div>

{{if .Contacts.Any}}
<div class="card" style="margin-top:14px">
  <div style="font-weight:600;font-size:14px;margin-bottom:8px">{{t $.Lang "Куда отправить"}}</div>
  <ul style="margin:0;padding-left:18px;color:#475569;font-size:14px;line-height:1.8">
    {{if .Contacts.App}}<li>{{t $.Lang "Поддержка конфигурации"}}: <b>{{.Contacts.App}}</b></li>{{end}}
    {{if .Contacts.Platform}}<li>{{t $.Lang "Разработчик платформы"}}: <b>{{.Contacts.Platform}}</b></li>{{end}}
    {{if .Contacts.IssuesURL}}<li><a href="{{.Contacts.IssuesURL}}" target="_blank" rel="noopener">{{t $.Lang "Трекер платформы"}}</a> <span style="color:#94a3b8">— {{t $.Lang "нужен аккаунт GitHub"}}</span></li>{{end}}
  </ul>
</div>
{{end}}

<script>
(function(){
  var btn = document.getElementById('ob-report-copy');
  var box = document.getElementById('ob-report-text');
  var ok  = document.getElementById('ob-report-copied');
  if (!btn || !box) return;
  btn.addEventListener('click', function(){
    var done = function(){ if (ok) { ok.style.display='inline'; setTimeout(function(){ ok.style.display='none'; }, 2000); } };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(box.value).then(done, fallback);
    } else { fallback(); }
    function fallback(){
      // Старые движки и небезопасный контекст: выделяем и копируем руками.
      box.focus(); box.select();
      try { document.execCommand('copy'); done(); } catch (e) { /* пользователь скопирует сам */ }
    }
  });
})();
</script>

{{else}}

<form method="post" action="/ui/report-problem" class="card" style="margin:0">
  <p style="color:#475569;font-size:14px;margin-bottom:14px">
    {{t $.Lang "Опишите, что случилось. Платформа добавит версию, окружение и техническую расшифровку ошибки, соберёт всё в один файл — отправить его нужно будет самостоятельно."}}
  </p>

  <div style="margin-bottom:12px">
    <label style="display:block;font-size:13px;color:#64748b;margin-bottom:4px">{{t $.Lang "Что вы делали"}}</label>
    <textarea name="did" rows="2" style="width:100%;padding:8px 10px;border:1px solid #d0d7e3;border-radius:6px;font-size:14px">{{.Did}}</textarea>
  </div>
  <div style="margin-bottom:12px">
    <label style="display:block;font-size:13px;color:#64748b;margin-bottom:4px">{{t $.Lang "Что вы ожидали"}}</label>
    <textarea name="expected" rows="2" style="width:100%;padding:8px 10px;border:1px solid #d0d7e3;border-radius:6px;font-size:14px">{{.Expected}}</textarea>
  </div>
  <div style="margin-bottom:12px">
    <label style="display:block;font-size:13px;color:#64748b;margin-bottom:4px">{{t $.Lang "Что получилось"}}</label>
    <textarea name="got" rows="3" style="width:100%;padding:8px 10px;border:1px solid #d0d7e3;border-radius:6px;font-size:14px">{{.Got}}</textarea>
  </div>

  <div style="margin-bottom:12px">
    <label style="display:block;font-size:13px;color:#64748b;margin-bottom:4px">{{t $.Lang "Ошибка, о которой идёт речь"}}</label>
    {{if .Incidents}}
    <select name="incident" style="width:100%;padding:8px 10px;border:1px solid #d0d7e3;border-radius:6px;font-size:14px">
      <option value="">{{t $.Lang "— не выбрана —"}}</option>
      {{range .Incidents}}
      <option value="{{.ID}}"{{if eq .ID $.IncidentID}} selected{{end}}>{{.ID}} · {{.When}} · {{.Short}}</option>
      {{end}}
    </select>
    <div style="color:#94a3b8;font-size:12px;margin-top:4px">{{t $.Lang "Список ведётся за текущий сеанс работы сервера — после перезапуска он пуст."}}</div>
    {{else}}
    <div style="color:#94a3b8;font-size:13px">{{t $.Lang "За текущий сеанс ошибок не зарегистрировано. Если код ошибки есть на экране, впишите его."}}</div>
    <input type="text" name="incident" value="{{.IncidentID}}" placeholder="E-3F7A2C"
      style="width:180px;margin-top:6px;padding:8px 10px;border:1px solid #d0d7e3;border-radius:6px;font-size:14px">
    {{end}}
  </div>

  {{if .CanAttachLog}}
  <label style="display:flex;align-items:center;gap:8px;font-size:14px;color:#475569;margin-bottom:6px">
    <input type="checkbox" name="attach_log" value="1"{{if .AttachLog}} checked{{end}}>
    {{t $.Lang "Приложить след запусков платформы"}}
  </label>
  <div style="color:#94a3b8;font-size:12px;margin-bottom:14px">
    {{t $.Lang "Полный журнал базы ведёт лаунчер — приложить его можно из окна списка баз."}}
  </div>
  {{end}}

  <button class="btn btn-primary" type="submit">{{t $.Lang "Сформировать отчёт"}}</button>
</form>

{{end}}
</main></div></body></html>
{{end}}
`
