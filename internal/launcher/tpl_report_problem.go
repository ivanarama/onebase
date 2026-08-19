package launcher

// Страница «Сообщить об ошибке» в лаунчере (план 116).
//
// Здесь она полнее, чем в Предприятии: лаунчер владеет журналами всех баз
// (~/.onebase/logs) и работает даже тогда, когда база не поднялась — а это как
// раз самый частый повод пожаловаться.

const tplReportProblem = `
{{define "page-report-problem"}}
{{template "lhead" .}}
<div class="result-page" style="max-width:820px">
  <h2>{{t $.Lang "Сообщить об ошибке"}}</h2>

  {{if .SavedPath}}
  <div style="background:#f0fdf4;border:1px solid #86efac;color:#166534;padding:8px 10px;border-radius:2px;margin-bottom:12px;font-size:13px;word-break:break-all">
    {{t $.Lang "Пакет сохранён"}}: <b>{{.SavedPath}}</b>
  </div>
  {{end}}
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}

  {{if .Preview}}
  <p style="margin-bottom:10px;font-size:13px;color:#555">
    {{t $.Lang "Проверьте текст: в сообщениях об ошибках могли оказаться данные, которые вы не хотите отправлять. Всё, что здесь написано, можно исправить или удалить."}}
  </p>
  <form method="post" action="/report-problem/save">
    <input type="hidden" name="base" value="{{.BaseID}}">
    <input type="hidden" name="attach_log" value="{{if .AttachLog}}1{{end}}">
    {{/* Описание едет с собой скрытыми полями: «Изменить описание» обязано
         вернуть форму заполненной, иначе текст пришлось бы набирать заново. */}}
    <input type="hidden" name="did" value="{{.Did}}">
    <input type="hidden" name="expected" value="{{.Expected}}">
    <input type="hidden" name="got" value="{{.Got}}">
    <textarea id="rp-text" name="report" rows="20" spellcheck="false"
      style="width:100%;padding:8px;border:1px solid #ACA899;border-radius:2px;font-family:Consolas,monospace;font-size:12px;line-height:1.5">{{.Preview}}</textarea>
    <div style="margin-top:12px;display:flex;gap:8px;flex-wrap:wrap;align-items:center">
      <button class="btn-ok" type="submit">{{t $.Lang "Сохранить пакет…"}}</button>
      <button class="btn-cancel" type="button" id="rp-copy">{{t $.Lang "Скопировать текст"}}</button>
      <button class="btn-cancel" type="submit" formaction="/report-problem/edit">{{t $.Lang "Изменить описание"}}</button>
      {{/* Выход отсюда есть только свой: страница отчёта рисуется без тулбара
           лаунчера, а нативное окно не даёт браузерного «назад». */}}
      <a class="btn-cancel" href="/">← {{t $.Lang "Назад к списку баз"}}</a>
      <span id="rp-copied" style="color:#166534;font-size:12px;display:none">{{t $.Lang "Скопировано"}}</span>
    </div>
    <div style="margin-top:8px;font-size:12px;color:#777">
      {{t $.Lang "В пакет попадёт этот текст и журналы — их содержимое видно в разделе «Журнал» выше."}}
    </div>
  </form>

  {{if .Contacts.Any}}
  <div style="margin-top:16px;padding-top:12px;border-top:1px solid #ddd">
    <div style="font-weight:600;margin-bottom:6px">{{t $.Lang "Куда отправить"}}</div>
    <ul style="margin:0;padding-left:18px;color:#555;line-height:1.8">
      {{if .Contacts.App}}<li>{{t $.Lang "Поддержка конфигурации"}}: <b>{{.Contacts.App}}</b></li>{{end}}
      {{if .Contacts.Platform}}<li>{{t $.Lang "Разработчик платформы"}}: <b>{{.Contacts.Platform}}</b></li>{{end}}
      {{if .Contacts.IssuesURL}}<li><a href="{{.Contacts.IssuesURL}}" target="_blank" rel="noopener">{{t $.Lang "Трекер платформы"}}</a> — {{t $.Lang "нужен аккаунт GitHub"}}</li>{{end}}
    </ul>
  </div>
  {{end}}

  <script>
  (function(){
    var b=document.getElementById('rp-copy'),t=document.getElementById('rp-text'),ok=document.getElementById('rp-copied');
    if(!b||!t)return;
    b.addEventListener('click',function(){
      var done=function(){if(ok){ok.style.display='inline';setTimeout(function(){ok.style.display='none';},2000);}};
      var fb=function(){t.focus();t.select();try{document.execCommand('copy');done();}catch(e){}};
      if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(t.value).then(done,fb);}else{fb();}
    });
  })();
  </script>

  {{else}}

  <form method="post" action="/report-problem">
    <p style="margin-bottom:12px;font-size:13px;color:#555">
      {{t $.Lang "Опишите, что случилось. Лаунчер добавит версию платформы, окружение и журналы — и соберёт всё в один файл. Отправить его нужно будет самостоятельно."}}
    </p>

    <div class="fg"><label>{{t $.Lang "Что вы делали"}}</label>
      <textarea name="did" rows="2" style="width:100%;padding:6px 8px;border:1px solid #ACA899;border-radius:2px;font-size:13px">{{.Did}}</textarea></div>
    <div class="fg"><label>{{t $.Lang "Что вы ожидали"}}</label>
      <textarea name="expected" rows="2" style="width:100%;padding:6px 8px;border:1px solid #ACA899;border-radius:2px;font-size:13px">{{.Expected}}</textarea></div>
    <div class="fg"><label>{{t $.Lang "Что получилось"}}</label>
      <textarea name="got" rows="3" style="width:100%;padding:6px 8px;border:1px solid #ACA899;border-radius:2px;font-size:13px">{{.Got}}</textarea></div>

    <div class="fg"><label>{{t $.Lang "База, с которой возникла проблема"}}</label>
      <select name="base" style="width:100%">
        <option value="">{{t $.Lang "— не выбрана —"}}</option>
        {{range .Bases}}<option value="{{.ID}}"{{if eq .ID $.BaseID}} selected{{end}}>{{.Name}}</option>{{end}}
      </select>
    </div>

    <label style="display:flex;align-items:center;gap:6px;margin:10px 0">
      <input type="checkbox" name="attach_log" value="1"{{if .AttachLog}} checked{{end}}>
      {{t $.Lang "Приложить журнал базы и след запусков платформы"}}
    </label>

    <div style="margin-top:12px;display:flex;gap:8px">
      <button class="btn-ok" type="submit">{{t $.Lang "Сформировать отчёт"}}</button>
      <a class="btn-cancel" href="/">← {{t $.Lang "Назад"}}</a>
    </div>
  </form>

  {{end}}
</div>
</body></html>
{{end}}
`
