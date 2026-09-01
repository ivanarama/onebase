package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/version"
)

// ── Admin panel handlers for configurator ────────────────────────────────────

func (h *handler) cfgAdminUsers(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	repo := auth.NewRepo(db)
	// Без схемы список всё равно будет пустым — честнее сказать об этом,
	// чем показать администратору «пользователей нет».
	if err := repo.EnsureSchema(r.Context()); err != nil {
		httpErrorDiv(w, "Не удалось подготовить схему пользователей", err)
		return
	}
	users, err := repo.List(r.Context())
	if err != nil {
		httpErrorDiv(w, "Не удалось прочитать список пользователей", err)
		return
	}
	lang := resolveLang(r)
	// JSON-литералы безопасно переживают кавычки, переводы строк и </script> в
	// переводе; строковая конкатенация с одинарными кавычками этого не гарантирует.
	sessionEndedJS, _ := json.Marshal(tr(lang, "Сессия конфигуратора завершена — войдите заново"))
	unexpectedResponseJS, _ := json.Marshal(tr(lang, "Неожиданный ответ сервера"))

	// Build language options for the user lang selector
	langOpts := `<option value="">—</option>`
	langOptsJS := ""
	if launcherBundle != nil {
		// jsEscape — экранирует апостроф и обратный слэш, чтобы Native-имя
		// языка (например узбекское «O'zbekcha» с апострофом) не закрывало
		// JS-строку и не ломало парсинг всего блока скриптов админки.
		jsEscape := func(s string) string {
			s = strings.ReplaceAll(s, `\`, `\\`)
			return strings.ReplaceAll(s, `'`, `\'`)
		}
		for _, l := range launcherBundle.Available() {
			langOpts += fmt.Sprintf(`<option value="%s">%s</option>`, l.Code, l.Native)
			langOptsJS += fmt.Sprintf(`,{v:'%s',l:'%s'}`, l.Code, jsEscape(l.Native))
		}
	}
	firstUserHint := ""
	adminChecked := ""
	if len(users) == 0 {
		firstUserHint = `<div style="background:#fff7ed;border:1px solid #fdba74;color:#9a3412;padding:10px 12px;border-radius:4px;margin-bottom:12px;font-size:12px">После создания первого пользователя вход потребуется всем. Первый пользователь должен быть администратором — иначе управлять учётными записями будет некому.</div>`
		adminChecked = " checked"
	}
	html := `<div style="padding:16px">
	<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px">
	  <h3 style="margin:0;font-size:15px">Пользователи</h3>
	  <button onclick="cfgUserNew()" style="background:#1a5fa8;color:#fff;border:none;padding:5px 14px;border-radius:3px;cursor:pointer;font-size:12px">+ Добавить</button>
	</div>
	` + firstUserHint + `
	<div id="cfg-user-new" style="display:none;margin-bottom:14px;padding:12px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px">
	  <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:end">
	    <div style="flex:1;min-width:120px"><label style="font-size:11px;color:#666">Логин</label><input id="cfg-un" style="width:100%;padding:5px 7px;border:1px solid #ccc;border-radius:3px;font-size:12px"></div>
	    <div style="flex:1;min-width:120px"><label style="font-size:11px;color:#666">Пароль</label><input id="cfg-up" type="password" style="width:100%;padding:5px 7px;border:1px solid #ccc;border-radius:3px;font-size:12px"></div>
	    <div style="flex:1;min-width:120px"><label style="font-size:11px;color:#666">Полное имя</label><input id="cfg-ufn" style="width:100%;padding:5px 7px;border:1px solid #ccc;border-radius:3px;font-size:12px"></div>
	    <label style="font-size:12px;display:flex;align-items:center;gap:4px"><input type="checkbox" id="cfg-ua"` + adminChecked + `> Админ</label>
	    <button onclick="cfgUserCreate()" style="background:#16a34a;color:#fff;border:none;padding:5px 12px;border-radius:3px;cursor:pointer;font-size:12px">Создать</button>
	    <button onclick="document.getElementById('cfg-user-new').style.display='none'" style="background:#e2e8f0;color:#333;border:none;padding:5px 10px;border-radius:3px;cursor:pointer;font-size:12px">Отмена</button>
	  </div>
	  <div id="cfg-user-err" style="color:#c00;font-size:11px;margin-top:6px;display:none"></div>
	</div>
	<table style="width:100%;border-collapse:collapse;font-size:12px">
	<tr style="background:#f1f5f9"><th style="text-align:left;padding:6px 8px;font-weight:600">Логин</th><th style="text-align:left;padding:6px 8px;font-weight:600">Имя</th><th style="text-align:center;padding:6px 8px;font-weight:600">Админ</th><th style="text-align:left;padding:6px 8px;font-weight:600">Создан</th><th style="text-align:left;padding:6px 8px;font-weight:600">Язык</th><th style="padding:6px 8px"></th></tr>`
	for i, u := range users {
		bg := ""
		if i%2 == 1 {
			bg = ` style="background:#f9fafb"`
		}
		admin := ""
		if u.IsAdmin {
			admin = `<span style="color:#16a34a;font-weight:600">Да</span>`
		}
		denyIcon := "🔓"
		denyTitle := "Запретить смену пароля"
		denyStyle := "background:#e2e8f0;color:#374151"
		if u.DenyPasswdChange {
			denyIcon = "🔒"
			denyTitle = "Снять запрет смены пароля"
			denyStyle = "background:#dc2626;color:#fff"
		}
		listIcon := "👁"
		listTitle := "Показывать в списке выбора"
		listStyle := "background:#e2e8f0;color:#374151"
		if u.ShowInList {
			listTitle = "Убрать из списка выбора"
			listStyle = "background:#2563eb;color:#fff"
		}
		aiIcon := "🤖"
		aiTitle := "Разрешить ИИ-чату доступ к данным (без прав администратора)"
		aiStyle := "background:#e2e8f0;color:#374151"
		if u.AIDataAccess {
			aiTitle = "Запретить ИИ-чату доступ к данным"
			aiStyle = "background:#7c3aed;color:#fff"
		}
		langLabel := "—"
		if u.Lang != "" {
			langLabel = u.Lang
		}
		html += fmt.Sprintf(`<tr%s><td style="padding:5px 8px">%s</td><td style="padding:5px 8px">%s</td><td style="padding:5px 8px;text-align:center">%s</td><td style="padding:5px 8px;color:#888">%s</td><td style="padding:5px 8px"><button onclick="cfgUserLang('%s','%s')" style="background:#7c3aed;color:#fff;border:none;padding:3px 8px;border-radius:3px;cursor:pointer;font-size:11px">%s</button></td><td style="padding:5px 8px;white-space:nowrap"><button onclick="cfgUserRoles('%s')" style="background:#0e7490;color:#fff;border:none;padding:3px 8px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:4px">Роли</button><button onclick="cfgUserPasswd('%s')" style="background:#f59e0b;color:#fff;border:none;padding:3px 8px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:4px">Пароль</button><button onclick="cfgUserDenyPasswd('%s',%v)" title="%s" style="%s;border:none;padding:3px 8px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:4px">%s</button><button onclick="cfgUserShowInList('%s',%v)" title="%s" style="%s;border:none;padding:3px 8px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:4px">%s</button><button onclick="cfgUserAIData('%s',%v)" title="%s" style="%s;border:none;padding:3px 8px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:4px">%s</button><button onclick="cfgUserDel('%s')" style="color:#c00;background:none;border:none;cursor:pointer;font-size:11px" title="Удалить">✕</button></td></tr>`,
			bg, escHTML(u.Login), escHTML(u.FullName), admin, u.CreatedAt.Format("02.01.2006"),
			u.ID, u.Lang, langLabel,
			u.ID,
			u.ID,
			u.ID, u.DenyPasswdChange, denyTitle, denyStyle, denyIcon,
			u.ID, u.ShowInList, listTitle, listStyle, listIcon,
			u.ID, u.AIDataAccess, aiTitle, aiStyle, aiIcon,
			u.ID)
	}
	if len(users) == 0 {
		html += `<tr><td colspan="6" style="padding:20px;text-align:center;color:#999">Нет пользователей</td></tr>`
	}
	html += `</table></div>
<script>
function cfgUserNew(){document.getElementById('cfg-user-new').style.display='block';document.getElementById('cfg-un').focus()}
function cfgUserRoles(id){cfgAdmin('users/roles?uid='+encodeURIComponent(id))}
// cfgPost — POST в админку с честным разбором ответа. Ответ обработчика — JSON,
// но при завершённой сессии до обработчика дело не доходит: middleware отдаёт
// редирект на форму входа, fetch его проходит и приносит HTML. Раньше r.json()
// на этом HTML бросал исключение, которое никто не ловил, — кнопка выглядела
// мёртвой («нажимаю Сохранить, ничего не происходит»). Ошибку обработчика
// (поле error) тоже превращаем в отказ, чтобы вызывающий не забыл её показать.
function cfgPost(path, body){
  return fetch('/bases/` + b.ID + `/configurator/admin/'+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
    .then(function(r){
      var ct=r.headers.get('content-type')||'';
      if(ct.indexOf('json')<0){
        throw new Error(r.redirected||(r.url||'').indexOf('/configurator/login')>=0
          ? ` + string(sessionEndedJS) + `
          : ` + string(unexpectedResponseJS) + `+' (HTTP '+r.status+')');
      }
      return r.json();
    })
    .then(function(d){
      if(d&&d.error){throw new Error(d.error)}
      return d;
    });
}
function cfgUserCreate(){
  var d={login:document.getElementById('cfg-un').value,password:document.getElementById('cfg-up').value,fullName:document.getElementById('cfg-ufn').value,isAdmin:document.getElementById('cfg-ua').checked};
  cfgPost('users/create',d)
    .then(function(){cfgAdmin('users')})
    .catch(function(e){
      var box=document.getElementById('cfg-user-err');
      box.textContent=e.message;box.style.display='block';
    });
}
// cfgConfirm — кастомный модал-подтверждение (WebView2 блокирует window.confirm).
function cfgConfirm(text, onOk){
  var ov=document.createElement('div');
  ov.style.cssText='position:fixed;inset:0;background:rgba(0,0,0,.35);z-index:10001;display:flex;align-items:center;justify-content:center';
  var box=document.createElement('div');
  box.style.cssText='background:#fff;padding:18px 22px;border-radius:8px;box-shadow:0 6px 28px rgba(0,0,0,.2);min-width:280px;font-size:13px';
  box.innerHTML='<div style="margin-bottom:14px">'+text+'</div>';
  var row=document.createElement('div');row.style.cssText='display:flex;gap:8px;justify-content:flex-end';
  var ok=document.createElement('button');ok.textContent='OK';ok.style.cssText='background:#c00;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer';
  var cancel=document.createElement('button');cancel.textContent='Отмена';cancel.style.cssText='background:#e2e8f0;color:#333;border:none;padding:5px 12px;border-radius:4px;cursor:pointer';
  ok.onclick=function(){document.body.removeChild(ov);onOk()};
  cancel.onclick=function(){document.body.removeChild(ov)};
  row.appendChild(ok);row.appendChild(cancel);box.appendChild(row);ov.appendChild(box);document.body.appendChild(ov);
}
// cfgInfo — кастомный alert (WebView2 блокирует window.alert).
function cfgInfo(text){
  var ov=document.createElement('div');
  ov.style.cssText='position:fixed;inset:0;background:rgba(0,0,0,.35);z-index:10001;display:flex;align-items:center;justify-content:center';
  var box=document.createElement('div');
  box.style.cssText='background:#fff;padding:18px 22px;border-radius:8px;box-shadow:0 6px 28px rgba(0,0,0,.2);min-width:240px;font-size:13px';
  box.innerHTML='<div style="margin-bottom:12px">'+text+'</div>';
  var ok=document.createElement('button');ok.textContent='OK';ok.style.cssText='background:#1a4a80;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer;float:right';
  ok.onclick=function(){document.body.removeChild(ov)};
  box.appendChild(ok);ov.appendChild(box);document.body.appendChild(ov);
}
function cfgUserDel(id){
  cfgConfirm('Удалить пользователя?', function(){
    // Отказ здесь штатный (последний админ, последний пользователь), а раньше
    // ответ вообще не читался: панель обновлялась и пользователь оставался на
    // месте без единого слова о причине.
    cfgPost('users/delete',{id:id})
      .then(function(){cfgAdmin('users')})
      .catch(function(e){cfgInfo('Ошибка: '+e.message)});
  });
}
function cfgUserPasswd(id){
  // Кастомный диалог: WebView2 не показывает window.prompt() — отсюда
  // «нет реакции на кнопку Пароль». HTML-popup отрисуется в любом движке.
  var ov=document.createElement('div');
  ov.style.cssText='position:fixed;inset:0;background:rgba(0,0,0,.35);z-index:10001;display:flex;align-items:center;justify-content:center';
  var box=document.createElement('div');
  box.style.cssText='background:#fff;padding:18px 22px;border-radius:8px;box-shadow:0 6px 28px rgba(0,0,0,.2);min-width:300px;font-size:13px;display:flex;flex-direction:column;gap:8px';
  box.innerHTML='<div style="font-weight:600">Новый пароль</div>';
  var i1=document.createElement('input');i1.type='password';i1.placeholder='Пароль';i1.style.cssText='padding:6px 10px;border:1px solid #c8d0de;border-radius:4px;font-size:13px';
  var i2=document.createElement('input');i2.type='password';i2.placeholder='Повторите пароль';i2.style.cssText='padding:6px 10px;border:1px solid #c8d0de;border-radius:4px;font-size:13px';
  var err=document.createElement('div');err.style.cssText='color:#c00;font-size:12px;min-height:1em';
  var row=document.createElement('div');row.style.cssText='display:flex;gap:8px;justify-content:flex-end;margin-top:4px';
  var ok=document.createElement('button');ok.textContent='Сохранить';ok.style.cssText='background:#1a4a80;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer';
  var cancel=document.createElement('button');cancel.textContent='Отмена';cancel.style.cssText='background:#e2e8f0;color:#333;border:none;padding:5px 12px;border-radius:4px;cursor:pointer';
  cancel.onclick=function(){document.body.removeChild(ov)};
  ok.onclick=function(){
    var pw=i1.value, pw2=i2.value;
    if(pw!==pw2){err.textContent='Пароли не совпадают';return}
    err.textContent='';
    cfgPost('users/passwd',{id:id,password:pw})
      .then(function(){
        document.body.removeChild(ov);
        cfgInfo('Пароль изменён');
      })
      .catch(function(e){err.textContent=e.message});
  };
  row.appendChild(ok);row.appendChild(cancel);
  box.appendChild(i1);box.appendChild(i2);box.appendChild(err);box.appendChild(row);
  ov.appendChild(box);document.body.appendChild(ov);
  setTimeout(function(){i1.focus()},50);
}
// Переключатели ниже сообщали об ошибке через window.alert, который WebView2
// не показывает: под лаунчером отказ был не виден вовсе.
function cfgUserDenyPasswd(id,current){
  cfgPost('users/deny-passwd',{id:id,deny:!current})
    .then(function(){cfgAdmin('users')})
    .catch(function(e){cfgInfo('Ошибка: '+e.message)});
}
function cfgUserShowInList(id,current){
  cfgPost('users/show-in-list',{id:id,show:!current})
    .then(function(){cfgAdmin('users')})
    .catch(function(e){cfgInfo('Ошибка: '+e.message)});
}
function cfgUserAIData(id,current){
  cfgPost('users/ai-data',{id:id,allow:!current})
    .then(function(){cfgAdmin('users')})
    .catch(function(e){cfgInfo('Ошибка: '+e.message)});
}
function cfgUserLang(id,current){
  var sel=document.createElement('select');
  sel.style.cssText='padding:4px 8px;border:1px solid #c8d0de;border-radius:4px;font-size:12px';
  var opts=[{v:'',l:'— по умолчанию —'}` + langOptsJS + `];
  for(var i=0;i<opts.length;i++){var o=document.createElement('option');o.value=opts[i].v;o.textContent=opts[i].l;if(opts[i].v===current)o.selected=true;sel.appendChild(o)}
  var popup=document.createElement('div');
  popup.style.cssText='position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);background:#fff;padding:16px;border-radius:8px;box-shadow:0 4px 24px rgba(0,0,0,.2);z-index:10001;display:flex;flex-direction:column;gap:8px';
  popup.innerHTML='<div style="font-weight:600;font-size:13px">Язык пользователя</div>';
  popup.appendChild(sel);
  var btns=document.createElement('div');btns.style.cssText='display:flex;gap:6px;justify-content:flex-end';
  var btnOK=document.createElement('button');btnOK.textContent='OK';btnOK.style.cssText='background:#1a4a80;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer;font-size:12px';
  var btnCancel=document.createElement('button');btnCancel.textContent='Отмена';btnCancel.style.cssText='background:#e2e8f0;color:#333;border:none;padding:5px 10px;border-radius:4px;cursor:pointer;font-size:12px';
  btns.appendChild(btnOK);btns.appendChild(btnCancel);popup.appendChild(btns);
  var bg=document.createElement('div');bg.style.cssText='position:fixed;inset:0;background:rgba(0,0,0,.3);z-index:10000';
  document.body.appendChild(bg);document.body.appendChild(popup);sel.focus();
  function close(){bg.remove();popup.remove()}
  btnCancel.onclick=close;bg.onclick=close;
  btnOK.onclick=function(){
    var lang=sel.value;close();
    cfgPost('users/lang',{id:id,lang:lang})
      .then(function(){cfgAdmin('users')})
      .catch(function(e){cfgInfo('Ошибка: '+e.message)});
  }
}
</script>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeBody(w, []byte(html))
}

func (h *handler) cfgAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	lang := resolveLang(r)
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
		FullName string `json:"fullName"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if req.Login == "" {
		writeJSON(w, 400, map[string]any{"error": tr(lang, "Логин обязателен")})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	hadUsers, err := repo.HasUsers(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	// Иначе первый обычный пользователь мгновенно включает авторизацию и
	// навсегда закрывает конфигуратор: войти в него может только admin.
	if !hadUsers && !req.IsAdmin {
		writeJSON(w, 400, map[string]any{"error": tr(lang, "Первый пользователь должен быть администратором")})
		return
	}
	user, err := repo.CreateManaged(r.Context(), req.Login, req.Password, req.FullName, req.IsAdmin)
	if err != nil {
		status := http.StatusInternalServerError
		if isPasswordPolicyError(err) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"error": passwordPolicyMessage(lang, err)})
		return
	}
	// До создания первого пользователя текущая страница была открыта без
	// cookie. Сразу начинаем configurator-сессию, чтобы cfgAdmin('users') после
	// AJAX не получил HTML логина внутри панели (его form submit давал HTTP 405).
	if !hadUsers {
		token, err := repo.CreateSession(r.Context(), user.ID, auth.SessionMeta{
			Kind: auth.SessionKindConfigurator, IP: r.RemoteAddr, UserAgent: r.UserAgent(),
		})
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		setConfiguratorSessionCookie(w, token)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "sessionStarted": !hadUsers})
}

func (h *handler) cfgAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.Delete(r.Context(), req.ID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, auth.ErrLastAdmin) || errors.Is(err, auth.ErrLastUser) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *handler) cfgAdminUserPasswd(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	lang := resolveLang(r)
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if req.ID == "" {
		writeJSON(w, 400, map[string]any{"error": tr(lang, "id обязателен")})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.UpdatePassword(r.Context(), req.ID, req.Password); err != nil {
		status := http.StatusInternalServerError
		if isPasswordPolicyError(err) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"error": passwordPolicyMessage(lang, err)})
		return
	}
	// Политика плана 78: смена пароля из конфигуратора — админское действие,
	// завершает все сессии пользователя (украденная сессия не переживёт смену).
	//
	// Сбой отзыва — это ровно провал той гарантии, ради которой отзыв и делают:
	// пароль уже сменён, а украденная сессия продолжает работать. Откатить
	// смену пароля нельзя, поэтому единственный честный вариант — сказать об
	// этом администратору и НЕ писать в аудит запись «сессии отозваны»: ложная
	// запись в журнале хуже отсутствия записи, по ней инцидент закроют как
	// отработанный.
	if err := repo.KickUserSessions(r.Context(), req.ID); err != nil {
		writeJSON(w, 500, map[string]any{
			"error": tr(resolveLang(r), "Пароль изменён, но сессии пользователя не завершены") + ": " + err.Error(),
		})
		return
	}
	targetLogin := ""
	if u, err := repo.GetByID(r.Context(), req.ID); err == nil {
		targetLogin = u.Login
	}
	logCfgSessionAudit(r, db, "password_change_sessions_revoked", targetLogin, req.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func isPasswordPolicyError(err error) bool {
	return errors.Is(err, auth.ErrPasswordRequired) ||
		errors.Is(err, auth.ErrPasswordTooShort) ||
		errors.Is(err, auth.ErrPasswordTooLong)
}

// passwordPolicyMessage добавляет к отказу политики паролей способ её смягчить.
// Сам текст ошибки говорит только «пароль не может быть пустым» — из
// конфигуратора не видно, что пустые пароли вообще включаются, и на тестовом
// стенде это тупик: админ упирается в запрет, о котором знает лишь исходный код.
func passwordPolicyMessage(lang string, err error) string {
	switch {
	case errors.Is(err, auth.ErrPasswordRequired):
		return err.Error() + ". " + tr(lang, "Пустые пароли включаются переменной окружения ONEBASE_ALLOW_EMPTY_PASSWORDS=true перед запуском лаунчера.")
	case errors.Is(err, auth.ErrPasswordTooShort):
		return err.Error() + ". " + tr(lang, "Минимальная длина задаётся переменной окружения ONEBASE_MIN_PASSWORD_LENGTH перед запуском лаунчера.")
	}
	return err.Error()
}

// logCfgSessionAudit пишет событие сессионного аудита от имени администратора
// конфигуратора. recordID — UUID (или пустая строка), логин цели — в entityName.
func logCfgSessionAudit(r *http.Request, db *storage.DB, action, targetLogin, targetUserID string) {
	var actorID, actorLogin string
	if u := cfgUserFromContext(r.Context()); u != nil {
		actorID, actorLogin = u.ID, u.Login
	}
	db.LogAction(r.Context(), action, "", targetLogin, targetUserID, actorID, actorLogin, r.RemoteAddr)
}

func (h *handler) cfgAdminUserDenyPasswd(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		ID   string `json:"id"`
		Deny bool   `json:"deny"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.SetDenyPasswdChange(r.Context(), req.ID, req.Deny); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *handler) cfgAdminUserShowInList(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		ID   string `json:"id"`
		Show bool   `json:"show"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.SetShowInList(r.Context(), req.ID, req.Show); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// cfgAdminUserAIData переключает доступ пользователя к данным ИИ-чата
// (инструменты описание_данных/выполнить_запрос без прав администратора).
func (h *handler) cfgAdminUserAIData(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.SetAIDataAccess(r.Context(), req.ID, req.Allow); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *handler) cfgAdminUserLang(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		ID   string `json:"id"`
		Lang string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.SetUserLang(r.Context(), req.ID, req.Lang); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *handler) cfgAdminSessions(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(r.Context()); err != nil {
		httpErrorDiv(w, "Не удалось подготовить схему сессий", err)
		return
	}
	sessions, err := repo.ActiveSessions(r.Context())
	if err != nil {
		httpErrorDiv(w, "Не удалось прочитать активные сессии", err)
		return
	}

	kindLabel := func(kind string) string {
		switch kind {
		case auth.SessionKindConfigurator:
			return "Конфигуратор"
		case auth.SessionKindEnterprise:
			return "Предприятие"
		}
		return "—"
	}
	fmtT := func(t time.Time, layout string) string {
		if t.IsZero() {
			return "—"
		}
		return t.Format(layout)
	}

	html := `<div style="padding:16px">
	<h3 style="margin:0 0 14px;font-size:15px">Активные пользователи</h3>
	<div style="margin:0 0 14px;display:flex;align-items:center;gap:8px;font-size:12px;color:#444">
	  <span>Максимум сессий на пользователя (0 — без ограничения):</span>
	  <input id="sess-limit" type="number" min="0" max="99" value="` + fmt.Sprintf("%d", db.GetMaxSessionsPerUser(r.Context())) + `" style="width:60px;padding:3px 6px;border:1px solid #ACA899;border-radius:2px">
	  <button onclick="cfgSaveSessLimit()" style="padding:3px 10px;border:1px solid #ACA899;border-radius:2px;background:#F5F4EE;cursor:pointer;font-size:12px">Сохранить</button>
	  <span id="sess-limit-msg" style="color:#16a34a"></span>
	</div>
	<table style="width:100%;border-collapse:collapse;font-size:12px">
	<tr style="background:#f1f5f9"><th style="text-align:left;padding:6px 8px;font-weight:600">Логин</th><th style="text-align:left;padding:6px 8px;font-weight:600">Имя</th><th style="text-align:left;padding:6px 8px;font-weight:600">Вид</th><th style="text-align:center;padding:6px 8px;font-weight:600">Админ</th><th style="text-align:left;padding:6px 8px;font-weight:600">Вход</th><th style="text-align:left;padding:6px 8px;font-weight:600">Активность</th><th style="text-align:left;padding:6px 8px;font-weight:600">Действует до</th><th style="text-align:left;padding:6px 8px;font-weight:600">IP</th><th style="padding:6px 8px"></th></tr>`
	for i, s := range sessions {
		bg := ""
		if i%2 == 1 {
			bg = ` style="background:#f9fafb"`
		}
		admin := ""
		if s.IsAdmin {
			admin = `<span style="color:#16a34a;font-weight:600">Да</span>`
		}
		kickOne := ""
		if s.PublicID != "" {
			kickOne = fmt.Sprintf(`<button onclick="cfgKickSession('%s','%s')" style="color:#c00;background:none;border:none;cursor:pointer;font-size:11px" title="Завершить сессию">✕</button>`,
				escHTML(s.PublicID), escHTML(s.Login))
		}
		html += fmt.Sprintf(`<tr%s><td style="padding:5px 8px">%s</td><td style="padding:5px 8px">%s</td><td style="padding:5px 8px;color:#666">%s</td><td style="padding:5px 8px;text-align:center">%s</td><td style="padding:5px 8px;color:#888">%s</td><td style="padding:5px 8px;color:#888">%s</td><td style="padding:5px 8px;color:#888">%s</td><td style="padding:5px 8px;color:#888">%s</td><td style="padding:5px 8px;white-space:nowrap">%s <button onclick="cfgKickAll('%s')" style="color:#c00;background:none;border:none;cursor:pointer;font-size:11px" title="Завершить все сессии пользователя">все</button></td></tr>`,
			bg, escHTML(s.Login), escHTML(s.FullName), kindLabel(s.Kind), admin,
			fmtT(s.CreatedAt, "02.01 15:04"), fmtT(s.LastSeenAt, "02.01 15:04"),
			s.ExpiresAt.Format("02.01.2006 15:04"), escHTML(s.IP), kickOne, escHTML(s.Login))
	}
	if len(sessions) == 0 {
		html += `<tr><td colspan="9" style="padding:20px;text-align:center;color:#999">Нет активных сессий</td></tr>`
	}
	html += `</table></div>
<script>
function cfgKickSession(publicID, login){
  if(!confirm('Завершить эту сессию '+login+'?'))return;
  fetch('/bases/` + b.ID + `/configurator/admin/sessions/kick',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({public_id:publicID})})
    .then(function(){cfgAdmin('sessions')})
}
function cfgKickAll(login){
  if(!confirm('Завершить все сессии '+login+'?'))return;
  fetch('/bases/` + b.ID + `/configurator/admin/sessions/kick',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({login:login})})
    .then(function(){cfgAdmin('sessions')})
}
function cfgSaveSessLimit(){
  var n = parseInt(document.getElementById('sess-limit').value, 10) || 0;
  fetch('/bases/` + b.ID + `/configurator/admin/sessions/limit',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({limit:n})})
    .then(function(r){return r.json()})
    .then(function(d){document.getElementById('sess-limit-msg').textContent = d.error ? ('Ошибка: '+d.error) : '✓';})
}
</script>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeBody(w, []byte(html))
}

// cfgAdminSessionKick завершает сессии: по public_id — одну (план 78), по
// login — все сессии пользователя (прежнее поведение).
func (h *handler) cfgAdminSessionKick(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		PublicID string `json:"public_id"`
		Login    string `json:"login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	switch {
	case req.PublicID != "":
		if err := repo.KickSession(r.Context(), req.PublicID); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		logCfgSessionAudit(r, db, "session_kick", req.Login, "")
	case req.Login != "":
		if err := repo.KickUser(r.Context(), req.Login); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		logCfgSessionAudit(r, db, "session_kick_all", req.Login, "")
	default:
		writeJSON(w, 400, map[string]any{"error": "нужен public_id или login"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// cfgAdminSessionLimit сохраняет политику «максимум сессий на пользователя»
// (план 78, п. 1.6; ключ auth.max_sessions_per_user в _settings).
func (h *handler) cfgAdminSessionLimit(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Limit int `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := db.SaveMaxSessionsPerUser(r.Context(), req.Limit); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *handler) cfgAdminAudit(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	s := db.GetAuditSettings(r.Context())
	ck := func(on bool) string {
		if on {
			return " checked"
		}
		return ""
	}

	// ── Блок настроек журнала ──
	html := `<div style="padding:16px">
	<h3 style="margin:0 0 14px;font-size:15px">Журнал регистрации</h3>
	<details style="margin-bottom:18px">
	  <summary style="cursor:pointer;font-size:13px;font-weight:600;padding:6px 0;user-select:none">⚙ Настройки журнала</summary>
	  <div style="margin-top:8px;padding:12px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px">
	  <label style="font-size:13px;font-weight:600;display:flex;align-items:center;gap:6px">
	    <input type="checkbox" id="au-enabled"` + ck(s.Enabled) + `> Вести журнал регистрации</label>
	  <div style="margin:10px 0 0 22px;display:flex;flex-direction:column;gap:5px">
	    <div style="font-size:11px;color:#666;margin-bottom:2px">Регистрировать события:</div>
	    <label style="font-size:12px;display:flex;align-items:center;gap:6px"><input type="checkbox" id="au-create"` + ck(s.Create) + `> Создание объектов</label>
	    <label style="font-size:12px;display:flex;align-items:center;gap:6px"><input type="checkbox" id="au-update"` + ck(s.Update) + `> Изменение объектов</label>
	    <label style="font-size:12px;display:flex;align-items:center;gap:6px"><input type="checkbox" id="au-delete"` + ck(s.Delete) + `> Удаление объектов</label>
	    <label style="font-size:12px;display:flex;align-items:center;gap:6px"><input type="checkbox" id="au-post"` + ck(s.Post) + `> Проведение / отмена</label>
	    <label style="font-size:12px;display:flex;align-items:center;gap:6px"><input type="checkbox" id="au-login"` + ck(s.Login) + `> Вход / выход пользователей</label>
	  </div>
	  <button onclick="cfgAuditSave()" style="margin-top:10px;background:#16a34a;color:#fff;border:none;padding:5px 14px;border-radius:3px;cursor:pointer;font-size:12px">Сохранить</button>
	  <span id="au-msg" style="font-size:11px;margin-left:8px"></span>
	  </div>
	</details>
	<div style="font-size:12px;color:#666;margin-bottom:8px">Последние записи:</div>
	<table style="width:100%;border-collapse:collapse;font-size:12px">
	<tr style="background:#f1f5f9"><th style="text-align:left;padding:6px 8px;font-weight:600">Время</th><th style="text-align:left;padding:6px 8px;font-weight:600">Пользователь</th><th style="text-align:left;padding:6px 8px;font-weight:600">Действие</th><th style="text-align:left;padding:6px 8px;font-weight:600">Объект</th></tr>`

	i := 0
	entries, _ := db.AuditSearch(r.Context(), storage.AuditFilter{}, 100, 0)
	for _, e := range entries {
		bg := ""
		if i%2 == 1 {
			bg = ` style="background:#f9fafb"`
		}
		obj := escHTML(e.EntityName)
		if e.EntityKind != "" {
			obj = escHTML(e.EntityKind) + ": " + obj
		}
		who := escHTML(e.UserLogin)
		if who == "" {
			who = `<span style="color:#999">(анонимно)</span>`
		}
		html += fmt.Sprintf(`<tr%s><td style="padding:5px 8px;color:#888;white-space:nowrap">%s</td><td style="padding:5px 8px">%s</td><td style="padding:5px 8px">%s</td><td style="padding:5px 8px">%s</td></tr>`,
			bg, e.At.Format("02.01.2006 15:04:05"), who, escHTML(e.Action), obj)
		i++
	}
	if i == 0 {
		html += `<tr><td colspan="4" style="padding:20px;text-align:center;color:#999">Журнал пуст</td></tr>`
	}
	html += `</table></div>
<script>
function cfgAuditSave(){
  var body={
    enabled:document.getElementById('au-enabled').checked,
    create:document.getElementById('au-create').checked,
    update:document.getElementById('au-update').checked,
    "delete":document.getElementById('au-delete').checked,
    post:document.getElementById('au-post').checked,
    login:document.getElementById('au-login').checked
  };
  fetch('/bases/` + b.ID + `/configurator/admin/audit/save',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
    .then(function(r){return r.json()})
    .then(function(d){
      var m=document.getElementById('au-msg');
      if(d.ok){m.textContent='Сохранено';m.style.color='#16a34a';}
      else{m.textContent=(d.error||'Ошибка');m.style.color='#c00';}
    })
    .catch(function(){var m=document.getElementById('au-msg');m.textContent='Ошибка сети';m.style.color='#c00';});
}
</script>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeBody(w, []byte(html))
}

// cfgAdminAuditSave сохраняет настройки журнала регистрации.
func (h *handler) cfgAdminAuditSave(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
		Create  bool `json:"create"`
		Update  bool `json:"update"`
		Delete  bool `json:"delete"`
		Post    bool `json:"post"`
		Login   bool `json:"login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	s := storage.AuditSettings{
		Enabled: req.Enabled, Create: req.Create, Update: req.Update,
		Delete: req.Delete, Post: req.Post, Login: req.Login,
	}
	if err := db.SaveAuditSettings(r.Context(), s); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// cfgAdminSettings — страница «Параметры базы» в админ-меню конфигуратора.
// Здесь хранятся настройки, специфичные для конкретной информационной базы
// (а не для git-версионируемой конфигурации): размер страницы списков и т.п.
func (h *handler) cfgAdminSettings(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	pageSize := db.GetListPageSize(r.Context())
	navChecked := ""
	if db.GetNavCollapsible(r.Context()) {
		navChecked = "checked"
	}
	netChecked := ""
	if db.GetNetworkEnabled(r.Context()) {
		netChecked = "checked"
	}
	execChecked := ""
	if db.GetExecEnabled(r.Context()) {
		execChecked = "checked"
	}
	formMode := db.GetFormOpenMode(r.Context())
	pagesSel, tabsSel := "", ""
	if formMode == storage.FormModeTabs {
		tabsSel = "selected"
	} else {
		pagesSel = "selected"
	}
	html := fmt.Sprintf(`<div style="padding:16px">
	<h3 style="margin:0 0 14px;font-size:15px">Параметры базы</h3>
	<div style="padding:12px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px;max-width:520px">
	  <div style="font-size:13px;font-weight:600;margin-bottom:8px">Списки</div>
	  <label style="font-size:12px;display:flex;align-items:center;gap:10px">
	    Строк на странице по умолчанию:
	    <input type="number" id="st-pagesize" min="1" max="%d" value="%d" style="width:90px;padding:3px 6px;border:1px solid #cbd5e1;border-radius:3px;font-size:12px">
	  </label>
	  <div style="font-size:11px;color:#666;margin-top:6px">Применяется к спискам справочников и документов. Допустимо от 1 до %d. URL-параметр <code>?limit=</code> по-прежнему переопределяет значение разово.</div>
	  <div style="font-size:13px;font-weight:600;margin:16px 0 8px">Меню</div>
	  <label style="font-size:12px;display:flex;align-items:center;gap:8px">
	    <input type="checkbox" id="st-collapsenav" %s>
	    Сворачиваемые группы в левом меню
	  </label>
	  <div style="font-size:11px;color:#666;margin-top:6px">Когда включено, группы меню (Отчёты, Регистры, Обработки…) можно сворачивать; тяжёлые группы свёрнуты по умолчанию, чтобы меню не растягивало страницу.</div>
	  <label style="font-size:12px;display:flex;align-items:center;gap:10px;margin-top:12px">
	    Режим открытия форм по умолчанию:
	    <select id="form_open_mode" style="padding:3px 6px;border:1px solid #cbd5e1;border-radius:3px;font-size:12px">
	      <option value="pages" %s>Отдельные страницы</option>
	      <option value="tabs" %s>Вкладки</option>
	    </select>
	  </label>
	  <div style="font-size:11px;color:#666;margin-top:6px">Дефолт для пользователей без личной настройки. «Вкладки» открывают формы в оболочке <code>/ui/app</code>, «Отдельные страницы» — как обычные страницы <code>/ui</code>. Каждый пользователь может переопределить это в «Параметрах».</div>
	  <div style="font-size:13px;font-weight:600;margin:16px 0 8px">Безопасность</div>
	  <label style="font-size:12px;display:flex;align-items:center;gap:8px">
	    <input type="checkbox" id="st-net" %s>
	    Разрешить сетевые операции конфигурации
	  </label>
	  <div style="font-size:11px;color:#666;margin-top:6px">Предохранитель. Пока выключен — блокируются исходящие веб-хуки, HTTP-клиент и отправка писем из DSL, входящие HTTP-сервисы. Защищает от того, чтобы восстановленная копия базы случайно слала уведомления в боевые системы. После восстановления из бэкапа сбрасывается в выключено — включайте осознанно.</div>
	  <label style="font-size:12px;display:flex;align-items:center;gap:8px;margin-top:12px">
	    <input type="checkbox" id="st-exec" %s>
	    Разрешить выполнение команд ОС
	  </label>
	  <div style="font-size:11px;color:#666;margin-top:6px">Опасно: DSL-функция <code>ВыполнитьКоманду</code> запускает процессы на сервере (исполнение кода). Включайте только на доверенной/локальной базе. По умолчанию и после восстановления из бэкапа — выключено.</div>
	  <button onclick="cfgSettingsSave()" style="margin-top:12px;background:#16a34a;color:#fff;border:none;padding:5px 14px;border-radius:3px;cursor:pointer;font-size:12px">Сохранить</button>
	  <span id="st-msg" style="font-size:11px;margin-left:8px"></span>
	</div>
</div>
<script>
function cfgSettingsSave(){
  var n=parseInt(document.getElementById('st-pagesize').value,10);
  var c=document.getElementById('st-collapsenav').checked;
  var net=document.getElementById('st-net').checked;
  var exec=document.getElementById('st-exec').checked;
  var fm=document.getElementById('form_open_mode').value;
  fetch('/bases/%s/configurator/admin/settings/save',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({list_page_size:n,collapsible_nav:c,network_enabled:net,exec_enabled:exec,form_open_mode:fm})})
    .then(function(r){return r.json()})
    .then(function(d){
      var m=document.getElementById('st-msg');
      if(d.ok){m.textContent='Сохранено';m.style.color='#16a34a';if(d.value){document.getElementById('st-pagesize').value=d.value;}}
      else{m.textContent=(d.error||'Ошибка');m.style.color='#c00';}
    })
    .catch(function(){var m=document.getElementById('st-msg');m.textContent='Ошибка сети';m.style.color='#c00';});
}
</script>`, storage.MaxListPageSize, pageSize, storage.MaxListPageSize, navChecked, pagesSel, tabsSel, netChecked, execChecked, b.ID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeBody(w, []byte(html))
}

// cfgAdminSettingsSave сохраняет общие параметры базы (пока — только размер
// страницы списков). Невалидное значение зажимается в [1; MaxListPageSize],
// 0/отрицательное считается «вернуть к дефолту».
func (h *handler) cfgAdminSettingsSave(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		ListPageSize   int    `json:"list_page_size"`
		CollapsibleNav *bool  `json:"collapsible_nav"`
		NetworkEnabled *bool  `json:"network_enabled"`
		ExecEnabled    *bool  `json:"exec_enabled"`
		FormOpenMode   string `json:"form_open_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := db.SaveListPageSize(r.Context(), req.ListPageSize); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if req.CollapsibleNav != nil {
		if err := db.SaveNavCollapsible(r.Context(), *req.CollapsibleNav); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.NetworkEnabled != nil {
		if err := db.SaveNetworkEnabled(r.Context(), *req.NetworkEnabled); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.ExecEnabled != nil {
		if err := db.SaveExecEnabled(r.Context(), *req.ExecEnabled); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.FormOpenMode != "" {
		if err := db.SaveFormOpenMode(r.Context(), req.FormOpenMode); err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "value": db.GetListPageSize(r.Context())})
}

func (h *handler) cfgAdminAbout(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}

	// Load app config for configuration name/version/logo
	type appCfg struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
		Logo    string `yaml:"logo"`
	}
	var cfg appCfg
	if err := readAppYAML(r.Context(), b, &cfg); err != nil {
		cfg = appCfg{}
	}

	// Load logo — use endpoint URL so it works for both file and database sources
	logoHTML := `<div style="font-size:48px;margin-bottom:8px">&#9889;</div>`
	if cfg.Logo != "" {
		logoHTML = fmt.Sprintf(`<img src="/bases/%s/configurator/logo" alt="Logo" style="max-height:140px;max-width:300px">`, b.ID)
	}

	cfgRows := ""
	if cfg.Name != "" {
		cfgRows += fmt.Sprintf(`<tr><td style="padding:6px 0;color:#888;width:140px">Конфигурация</td><td style="padding:6px 0;font-weight:600">%s</td></tr>`, escHTML(cfg.Name))
	}
	if cfg.Version != "" {
		cfgRows += fmt.Sprintf(`<tr><td style="padding:6px 0;color:#888">Версия конфигурации</td><td style="padding:6px 0">%s</td></tr>`, escHTML(cfg.Version))
	}

	userRow := ""
	if u := cfgUserFromContext(r.Context()); u != nil {
		label := escHTML(u.Login)
		if u.FullName != "" {
			label += " <span style='color:#888;font-weight:400'>" + escHTML(u.FullName) + "</span>"
		}
		userRow = fmt.Sprintf(`<tr><td style="padding:6px 0;color:#888;width:140px">Пользователь</td><td style="padding:6px 0">%s</td></tr>`, label)
	}

	platVer := escHTML(version.String())
	if d := version.CommitDate(); d != "" {
		platVer += ` <span style="color:#94a3b8">· ` + escHTML(d)
		if c := version.Commit(); c != "" {
			platVer += ` · ` + escHTML(c)
		}
		platVer += `</span>`
	}
	// Конфигуратор обслуживает тот же процесс, что и лаунчер, поэтому отсюда
	// можно сразу увести на страницу обновления (план 92). Само обновление
	// делается там: оно остановит все базы и перезапустит платформу.
	if upd := h.updatesState(); upd.ShowBadge() {
		platVer += fmt.Sprintf(
			`<div style="margin-top:6px;font-size:12px;color:#166534">%s: <b>%s</b> · <a href="/updates" target="_top" style="color:#1a5fa8">%s</a></div>`,
			escHTML(tr(resolveLang(r), "Доступна новая версия платформы")),
			escHTML(upd.LatestTag),
			escHTML(tr(resolveLang(r), "Обновление платформы")),
		)
	}

	dbLocation := baseDatabaseLocation(b)
	configMode := "Файлы"
	configLocation := b.Path
	if b.ConfigSource == "database" {
		configMode = "В базе данных"
		configLocation = dbLocation
	}
	if configLocation == "" {
		configLocation = "—"
	}
	if dbLocation == "" {
		dbLocation = "—"
	}

	html := fmt.Sprintf(`<div style="padding:24px;max-width:560px">
	<div style="text-align:center;margin-bottom:20px">
	  %s
	  <div style="font-size:18px;font-weight:600;color:#1a5fa8">OneBase</div>
	</div>
	<table style="width:100%%;border-collapse:collapse;font-size:13px">
	%s
	<tr><td style="padding:6px 0;color:#888;width:140px">Версия платформы</td><td style="padding:6px 0">%s</td></tr>
	%s
	<tr><td style="padding:6px 0;color:#888">Режим конфигурации</td><td style="padding:6px 0">%s</td></tr>
	<tr><td style="padding:6px 0;color:#888">Расположение конфигурации</td><td style="padding:6px 0;word-break:break-all">%s</td></tr>
	<tr><td style="padding:6px 0;color:#888">База данных</td><td style="padding:6px 0;word-break:break-all">%s</td></tr>
	<tr><td style="padding:6px 0;color:#888">Порт</td><td style="padding:6px 0">:%d</td></tr>
	</table>
	<div style="margin-top:16px;text-align:right">
	  <a href="/report-problem?base=%s" target="_top" style="color:#1a5fa8">%s</a>
	</div>
	</div>`,
		logoHTML,
		userRow,
		platVer,
		cfgRows,
		escHTML(configMode),
		escHTML(configLocation),
		escHTML(dbLocation),
		b.Port,
		escHTML(b.ID),
		escHTML(tr(resolveLang(r), "Сообщить об ошибке")))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeBody(w, []byte(html))
}

func (h *handler) configuratorLogo(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Load app config to find logo path
	type appCfg struct {
		Logo string `yaml:"logo"`
	}
	var cfg appCfg
	var logoData []byte

	if err := readAppYAML(r.Context(), b, &cfg); err != nil || cfg.Logo == "" {
		http.NotFound(w, r)
		return
	}

	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			http.NotFound(w, r)
			return
		}
		defer db.Close()
		if err := db.QueryRow(r.Context(), `SELECT content FROM _onebase_config WHERE path = $1`, cfg.Logo).Scan(&logoData); err != nil {
			http.NotFound(w, r)
			return
		}
	} else {
		data, err := os.ReadFile(filepath.Join(b.Path, cfg.Logo)) //nolint:gosec // G304: путь берётся из config/app.yaml базы, который правит её же администратор
		if err != nil {
			http.NotFound(w, r)
			return
		}
		logoData = data
	}

	ext := strings.ToLower(filepath.Ext(cfg.Logo))
	switch ext {
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-cache")
	writeBody(w, logoData)
}

func escHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// maskDSN — тонкая обёртка над общим редактором (internal/logging): тот же
// алгоритм используют экран «О программе» базы и отчёт об ошибке.
func maskDSN(dsn string) string { return oblog.RedactDSN(dsn) }

// baseDatabaseLocation returns the same database location the launcher uses,
// including the actual file path for SQLite and a password-masked PostgreSQL
// connection string. The empty DBType/DB fallback mirrors OpenDB.
func baseDatabaseLocation(b *Base) string {
	if b.DBType == "sqlite" || (b.DBType == "" && b.DB == "") {
		if b.DBPath != "" {
			return b.DBPath
		}
		return filepath.Join(os.TempDir(), "onebase_"+b.ID+".db")
	}
	return maskDSN(b.DB)
}
