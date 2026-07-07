package ui

const tplAllFunctions = `
{{define "page-all-functions"}}
{{template "head" .}}{{template "nav" .}}
<main style="max-width:900px">
<h2>{{t $.Lang "Все функции"}}</h2>
<div style="margin-bottom:14px">
  <input id="af-search" type="text" placeholder="{{t $.Lang "Поиск по имени объекта..."}}" autofocus data-ob-af-search
    style="width:100%;padding:9px 14px;border:1px solid #d0d7e3;border-radius:6px;font-size:14px">
</div>

{{if .Catalogs}}
<div class="af-group" data-group="Справочники">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Справочники"}} <span class="af-cnt">{{len .Catalogs}}</span></div>
  <div class="af-group-body">
  {{range .Catalogs}}<a class="af-link" href="/ui/catalog/{{lower .Name}}" data-name="{{.Name}}">{{.Name}}</a>{{end}}
  </div>
</div>
{{end}}

{{if .Documents}}
<div class="af-group" data-group="Документы">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Документы"}} <span class="af-cnt">{{len .Documents}}</span></div>
  <div class="af-group-body">
  {{range .Documents}}<a class="af-link" href="/ui/document/{{lower .Name}}" data-name="{{.Name}}">{{.Name}}</a>{{end}}
  </div>
</div>
{{end}}

{{if .Registers}}
<div class="af-group" data-group="Регистры накопления">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Регистры накопления"}} <span class="af-cnt">{{len .Registers}}</span></div>
  <div class="af-group-body">
  {{range .Registers}}<a class="af-link" href="/ui/register/{{lower .Name}}" data-name="{{.Name}}">{{.Name}}</a>{{end}}
  </div>
</div>
{{end}}

{{if .InfoRegisters}}
<div class="af-group" data-group="Регистры сведений">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Регистры сведений"}} <span class="af-cnt">{{len .InfoRegisters}}</span></div>
  <div class="af-group-body">
  {{range .InfoRegisters}}<a class="af-link" href="/ui/inforeg/{{lower .Name}}" data-name="{{.Name}}">{{.Name}}</a>{{end}}
  </div>
</div>
{{end}}

{{if .Enums}}
<div class="af-group" data-group="Перечисления">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Перечисления"}} <span class="af-cnt">{{len .Enums}}</span></div>
  <div class="af-group-body">
  {{range .Enums}}<div class="af-link" data-name="{{.Name}}">{{.Name}}: {{range $i, $v := .Values}}{{if $i}}, {{end}}{{$v}}{{end}}</div>{{end}}
  </div>
</div>
{{end}}

{{if .Reports}}
<div class="af-group" data-group="Отчёты">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Отчёты"}} <span class="af-cnt">{{len .Reports}}</span></div>
  <div class="af-group-body">
  {{range .Reports}}<a class="af-link" href="/ui/report/{{lower .Name}}" data-name="{{.Name}}">{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</a>{{end}}
  </div>
</div>
{{end}}

{{if .Processors}}
<div class="af-group" data-group="Обработки">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Обработки"}} <span class="af-cnt">{{len .Processors}}</span></div>
  <div class="af-group-body">
  {{range .Processors}}<a class="af-link" href="/ui/processor/{{lower .Name}}" data-name="{{.Name}}">{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</a>{{end}}
  </div>
</div>
{{end}}

{{if .Constants}}
<div class="af-group" data-group="Константы">
  <div class="af-group-hd" data-ob-af-toggle>{{t $.Lang "Константы"}} <span class="af-cnt">{{len .Constants}}</span></div>
  <div class="af-group-body">
  {{range .Constants}}<a class="af-link" href="/ui/constants" data-name="{{.Name}}">{{.DisplayLabel $.Lang}}</a>{{end}}
  </div>
</div>
{{end}}

</main>
<style>
.af-group{margin-bottom:8px;border:1px solid #e2e8f0;border-radius:6px;overflow:hidden}
.af-group-hd{padding:10px 14px;background:#f0f3f8;font-weight:600;font-size:13px;color:#1a3a6a;cursor:pointer;display:flex;align-items:center;gap:6px}
.af-group-hd:hover{background:#e8eeff}
.af-cnt{font-size:11px;color:#94a3b8;font-weight:400}
.af-group-body{display:none;padding:4px 0}
.af-group.open .af-group-body{display:block}
.af-link{display:block;padding:7px 14px;font-size:13px;color:#334155;text-decoration:none}
.af-link:hover{background:#f0f4ff;color:#1a4a80}
.af-link.hidden{display:none}
</style>
<script>
// Open all groups by default
document.querySelectorAll('.af-group').forEach(function(g){g.classList.add('open');});

function afToggle(hd){
  hd.closest('.af-group').classList.toggle('open');
}

function afFilter(value){
  var q=value.toLowerCase().trim();
  document.querySelectorAll('.af-group').forEach(function(g){
    var any=false;
    g.querySelectorAll('.af-link').forEach(function(a){
      var name=(a.dataset.name||a.textContent).toLowerCase();
      var show=!q||name.indexOf(q)>=0;
      a.classList.toggle('hidden',!show);
      if(show)any=true;
    });
    g.style.display=any?'':'none';
    if(q&&any)g.classList.add('open');
  });
}

document.addEventListener('click', function(e) {
  var hd = e.target.closest && e.target.closest('[data-ob-af-toggle]');
  if (!hd) return;
  afToggle(hd);
});

document.addEventListener('input', function(e) {
  if (e.target.matches && e.target.matches('[data-ob-af-search]')) afFilter(e.target.value);
});
</script>
</div></body></html>
{{end}}
`
