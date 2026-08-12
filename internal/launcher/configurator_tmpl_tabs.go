package launcher

// ── Converter tab ─────────────────────────────────────────────────────────────

const cfgTabConvert = `{{define "tab-convert"}}
<div class="pad">
<div class="convert-form">
  <h3>🔄 {{t $.Lang "Импорт конфигурации из выгрузки 1С:Предприятие 8.3"}}</h3>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/convert">
    <div class="fg">
      <label>{{t $.Lang "Путь к папке XML-выгрузки"}}</label>
      <div class="input-browse">
        <input type="text" id="convert-src-dir" name="src_dir" value="{{.ConvertSrcDir}}"
               placeholder="C:\Users\...\export\МояКонфигурация" autofocus>
        <button type="button" class="btn-browse" onclick="pickDir('convert-src-dir','{{t $.Lang "Выберите папку XML-выгрузки"}}')">📁</button>
      </div>
      <div class="hint">{{t $.Lang "Выгрузка делается в конфигураторе 1С:Предприятие: Конфигурация → Выгрузить конфигурацию в файлы"}}</div>
    </div>
    <div class="form-btns">
      <button class="btn-primary" type="submit" name="apply" value="0">{{t $.Lang "Просмотр"}}</button>
      <button class="btn-secondary" type="submit" name="apply" value="1">{{t $.Lang "Конвертировать и применить"}}</button>
    </div>
  </form>
</div>
{{if .ConvertApplied}}<div class="applied">{{t $.Lang "✓ Конфигурация применена к базе"}}</div>{{end}}
{{if .ConvertResult}}
<div class="convert-result">
  <h3>{{t $.Lang "Результат"}}</h3>
  <pre class="convert-out">{{.ConvertResult}}</pre>
</div>
{{end}}
</div>
{{end}}`

// ── Files tab ─────────────────────────────────────────────────────────────────

const cfgTabFiles = `{{define "tab-files"}}
<div class="pad">
{{if .ConfigFileTree}}
<style>
.files-tree-wrap{display:flex;gap:14px;margin-bottom:22px;height:62vh;min-height:360px}
.files-tree{flex:0 0 340px;overflow:auto;background:#fff;border:1px solid #e2e8f0;border-radius:8px;padding:8px 10px}
.files-tree h3{font-size:13px;margin:0 0 8px;color:#374151}
.files-view{flex:1;display:flex;flex-direction:column;background:#fff;border:1px solid #e2e8f0;border-radius:8px;overflow:hidden;min-width:0}
.files-view-hd{padding:8px 12px;background:#f8fafc;border-bottom:1px solid #eef0f5;font-size:12px;color:#475569;font-family:monospace;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.files-view-body{flex:1;margin:0;padding:12px 14px;overflow:auto;font-family:'Cascadia Code','Fira Code',monospace;font-size:12px;line-height:1.5;white-space:pre;color:#1e293b}
.ftcat{border-top:1px solid #f1f5f9}
.ftcat:first-child{border-top:none}
.ftcat>summary{font-size:11px;font-weight:700;color:#64748b;text-transform:uppercase;letter-spacing:.4px;padding:7px 4px;cursor:pointer;list-style:none}
.ftcat>summary::-webkit-details-marker{display:none}
.ftobj{margin-left:8px}
.ftobj>summary{font-size:13px;color:#1a3a6a;font-weight:600;padding:3px 4px;cursor:pointer;list-style:none}
.ftobj>summary::marker{content:""}
.ftfile{display:block;padding:3px 4px 3px 24px;font-size:12px;color:#334155;text-decoration:none;border-radius:4px}
.ftfile:hover{background:#f0f4ff;color:#1a4a80}
.ftfile.sel{background:#e8eeff;color:#1a4a80;font-weight:600}
.ftfile.loose{padding-left:14px;color:#64748b}
.ftedit{text-decoration:none;color:#94a3b8;font-size:13px;margin-left:6px;opacity:.35;transition:opacity .12s,color .12s}
.ftobj>summary:hover .ftedit{opacity:1}
.ftedit:hover{color:#1a4a80}
</style>
<div class="files-tree-wrap">
  <div class="files-tree" id="files-tree">
    <h3>📂 {{t $.Lang "Файлы конфигурации"}}</h3>
    {{range .ConfigFileTree}}
    <details class="ftcat" open>
      <summary>{{.Name}}</summary>
      {{range .Objects}}
        {{if .Name}}
        <details class="ftobj">
          <summary>📄 {{.Name}}{{if .NodeID}} <a class="ftedit" href="/bases/{{$.Base.ID}}/configurator?tab=tree&select={{.NodeID}}" title="{{t $.Lang "Открыть в редакторе"}}">✎</a>{{end}}</summary>
          {{range .Files}}<a class="ftfile" href="#" data-path="{{.Path}}" onclick="cfgViewFile(this);return false" title="{{.Path}}">{{.Label}}</a>{{end}}
        </details>
        {{else}}{{range .Files}}<a class="ftfile loose" href="#" data-path="{{.Path}}" onclick="cfgViewFile(this);return false" title="{{.Path}}">{{.Path}}</a>{{end}}{{end}}
      {{end}}
    </details>
    {{end}}
  </div>
  <div class="files-view">
    <div class="files-view-hd" id="files-view-path">{{t $.Lang "Выберите файл слева для просмотра"}}</div>
    <pre class="files-view-body" id="files-view-body"></pre>
  </div>
</div>
<script>
function cfgViewFile(el){
  var path=el.getAttribute('data-path');
  document.querySelectorAll('#files-tree .ftfile.sel').forEach(function(x){x.classList.remove('sel');});
  el.classList.add('sel');
  document.getElementById('files-view-path').textContent=path;
  var body=document.getElementById('files-view-body'); body.textContent='…';
  fetch('/bases/{{.Base.ID}}/configurator/file?path='+encodeURIComponent(path))
    .then(function(r){ if(!r.ok)throw new Error('HTTP '+r.status); return r.text(); })
    .then(function(t){ body.textContent=t; })
    .catch(function(e){ body.textContent='Ошибка: '+e.message; });
}
</script>
{{end}}
<div class="files-grid">
  <div class="file-card">
    <h3>📤 {{t $.Lang "Выгрузить конфигурацию"}}</h3>
    <p>{{t $.Lang "Экспортирует файлы в"}}<br><code>~/.onebase/workspace/{{.Base.ID}}/</code><br>и открывает папку.</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/export">
      <button class="btn-primary" type="submit">{{t $.Lang "Выгрузить"}}</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">{{t $.Lang "Файловый режим — файлы в:"}}<br><code>{{.Base.Path}}</code></p>
    {{end}}
  </div>
  <div class="file-card">
    <h3>📥 {{t $.Lang "Загрузить конфигурацию"}}</h3>
    <p>{{t $.Lang "Загружает файлы из папки в базу данных и применяет миграцию."}}</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/import">
      <div class="fg">
        <label>{{t $.Lang "Путь к папке"}}</label>
        <div class="input-browse">
          <input type="text" id="config-import-dir" name="path" placeholder="~/.onebase/workspace/{{.Base.ID}}">
          <button type="button" class="btn-browse" onclick="pickDir('config-import-dir','{{t $.Lang "Выберите папку конфигурации"}}')">📁</button>
        </div>
      </div>
      <button class="btn-primary" type="submit">{{t $.Lang "Загрузить"}}</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">{{t $.Lang "Редактируйте файлы напрямую. Сервер перезагружает конфигурацию автоматически."}}</p>
    {{end}}
  </div>
</div>
</div>
{{end}}`

const cfgTabBackup = `{{define "tab-backup"}}
<div class="pad">
  <h2 style="margin:0 0 4px;font-size:18px">💾 {{t $.Lang "Бэкапы"}}</h2>
  <p style="font-size:12px;color:#64748b;margin:0 0 16px">{{t $.Lang "Резервное копирование и восстановление базы данных"}}</p>
  {{if .BackupMessage}}<div class="success-box">{{.BackupMessage}}</div>{{end}}
  <div style="font-size:12px;color:#64748b;margin-bottom:12px;padding:6px 10px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px">
    {{t $.Lang "Папка бэкапов"}}: <code style="background:#e2e8f0;padding:1px 4px;border-radius:3px">{{.BackupDir}}</code>
  </div>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/create" style="margin-bottom:16px" onsubmit="cfgBackupStart(this,'⏳ Создаю бэкап...')">
    <button class="btn-save" type="submit">{{t $.Lang "Создать бэкап сейчас"}}</button>
  </form>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/upload" enctype="multipart/form-data" style="margin-bottom:16px;display:flex;align-items:center;gap:8px" onsubmit="cfgBackupStart(this,'⏳ Загружаю...')">
    <input type="file" name="backup_file" accept=".sql.gz,.sql,.db,.sqlite,.sqlite3" required style="font-size:12px">
    <button class="btn-save" type="submit">{{t $.Lang "Загрузить файл бэкапа"}}</button>
  </form>
  <h3 style="font-size:13px;margin:0 0 8px;color:#374151">{{t $.Lang "Файлы бэкапов"}}</h3>
  <table class="fields-tbl">
  <tr><th>{{t $.Lang "Файл"}}</th><th>{{t $.Lang "Размер"}}</th><th>{{t $.Lang "Дата"}}</th><th></th></tr>
  {{range .BackupFiles}}
  <tr>
    <td style="font-size:12px">{{.Name}}</td>
    <td style="font-size:12px;color:#64748b">{{.Size}}</td>
    <td style="font-size:12px;color:#64748b">{{.Date}}</td>
    <td style="white-space:nowrap">
      <a href="/bases/{{$.Base.ID}}/configurator/backup/{{.Name}}/download" style="font-size:11px;color:#1a4a80;text-decoration:none">{{t $.Lang "Скачать"}}</a>
      <form method="POST" action="/bases/{{$.Base.ID}}/configurator/backup/{{.Name}}/restore" style="display:inline" onsubmit="if(!confirm('Восстановить {{.Name}}? Текущие данные будут заменены!'))return false;cfgBackupStart(this,'⏳ Восстановление...')">
        <button type="submit" style="font-size:11px;color:#16a34a;background:none;border:none;cursor:pointer;padding:0 4px">{{t $.Lang "Восстановить"}}</button>
      </form>
      <form method="POST" action="/bases/{{$.Base.ID}}/configurator/backup/{{.Name}}/delete" style="display:inline" onsubmit="return confirm('Удалить {{.Name}}?')">
        <button type="submit" style="font-size:11px;color:#dc2626;background:none;border:none;cursor:pointer;padding:0 4px">{{t $.Lang "Удалить"}}</button>
      </form>
    </td>
  </tr>
  {{else}}
  <tr><td colspan="4" style="color:#94a3b8;font-size:12px;padding:8px">{{t $.Lang "Нет бэкапов"}}</td></tr>
  {{end}}
  </table>
  <details style="margin-top:20px"><summary style="font-size:13px;font-weight:600;color:#374151;cursor:pointer;margin-bottom:8px">{{t $.Lang "Настройки автобэкапа"}}</summary>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/settings">
    <div style="margin-bottom:8px">
      <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
        <input type="checkbox" name="backup_enabled" {{if .BackupSettings.Enabled}}checked{{end}}>
        {{t $.Lang "Включить автобэкап"}}
      </label>
    </div>
    <div style="margin-bottom:8px">
      <label style="font-size:12px;color:#64748b;display:block;margin-bottom:4px">{{t $.Lang "Расписание"}} (cron)</label>
      <input type="text" name="backup_schedule" value="{{.BackupSettings.Schedule}}" placeholder="0 2 * * *" style="width:200px;padding:4px 8px;border:1px solid #e2e8f0;border-radius:4px;font-size:13px">
    </div>
    <div style="margin-bottom:8px">
      <label style="font-size:12px;color:#64748b;display:block;margin-bottom:4px">{{t $.Lang "Хранить последних"}}</label>
      <input type="number" name="backup_keep" value="{{.BackupSettings.KeepLast}}" placeholder="7" min="1" max="100" style="width:80px;padding:4px 8px;border:1px solid #e2e8f0;border-radius:4px;font-size:13px">
    </div>
    <div style="margin-bottom:8px">
      <label style="font-size:12px;color:#64748b;display:block;margin-bottom:4px">{{t $.Lang "Директория"}} ({{t $.Lang "пусто = по умолчанию"}})</label>
      <input type="text" name="backup_dir" value="{{.BackupSettings.Directory}}" placeholder="{{.BackupDir}}" style="width:100%;max-width:500px;padding:6px 10px;border:1px solid #d1d5db;border-radius:4px;font-size:13px;background:#fff">
    </div>
    <button class="btn-save" type="submit">{{t $.Lang "Сохранить настройки"}}</button>
  </form>
  </details>
  <details style="margin-top:20px"><summary style="font-size:13px;font-weight:600;color:#374151;cursor:pointer;margin-bottom:8px">{{t $.Lang "Полная выгрузка (база + конфигурация)"}}</summary>
  <p style="font-size:12px;color:#64748b;margin:0 0 12px">{{t $.Lang "Выгрузка базы данных и конфигурации в один файл (.obz). Позволяет полностью перенести базу на другой сервер."}}</p>
  <div style="display:flex;gap:12px;align-items:flex-start;flex-wrap:wrap">
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/full-export" style="display:flex;flex-direction:column;gap:6px">
      <input type="hidden" name="compatible" value="true">
      <div style="font-size:12px;color:#374151">{{t $.Lang "Совместимый формат (PostgreSQL ↔ SQLite)"}}</div>
      <button class="btn-save" type="submit" style="width:fit-content">{{t $.Lang "Выгрузить всё в .obz"}}</button>
    </form>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/full-import" enctype="multipart/form-data" style="display:flex;align-items:center;gap:8px;flex-wrap:wrap" onsubmit="if(!confirm('Восстановить из .obz файла? Все текущие данные будут заменены!'))return false;cfgBackupStart(this,'⏳ Восстановление из .obz...')">
      <input type="file" name="obz_file" accept=".obz" required style="font-size:12px">
      <select name="exchange_mode" title="{{t $.Lang "Состояние планов обмена"}}" style="font-size:12px;padding:5px 7px;border:1px solid #d1d5db;border-radius:4px;background:#fff">
        <option value="disaster_recovery">{{t $.Lang "Тот же узел (восстановить очередь обмена)"}}</option>
        <option value="clone">{{t $.Lang "Клон (сбросить узел и очередь)"}}</option>
      </select>
      <button class="btn-save" type="submit" style="background:#dc2626">{{t $.Lang "Загрузить из .obz"}}</button>
    </form>
  </div>
  </details>
  <details style="margin-top:12px"><summary style="font-size:13px;font-weight:600;color:#374151;cursor:pointer;margin-bottom:8px">{{t $.Lang "Перенос конфигурации (только метаданные)"}}</summary>
  <p style="font-size:12px;color:#64748b;margin:0 0 12px">{{t $.Lang "Экспортируйте конфигурацию в ZIP для переноса на другой сервер или импортируйте из архива."}}</p>
  <div style="display:flex;gap:12px;align-items:flex-start;flex-wrap:wrap">
    <a href="/bases/{{.Base.ID}}/configurator/config/export-zip" class="btn-save" style="text-decoration:none;display:inline-block">{{t $.Lang "Экспорт в ZIP"}}</a>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/config/import-zip" enctype="multipart/form-data" style="display:flex;align-items:center;gap:8px">
      <input type="file" name="config_zip" accept=".zip" required style="font-size:12px">
      <button class="btn-save" type="submit">{{t $.Lang "Импорт из ZIP"}}</button>
    </form>
  </div>
  </details>
</div>
{{end}}`
