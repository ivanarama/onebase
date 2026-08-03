package deviceagent

import (
	"net"
	"net/http"
	"net/url"
)

// page отдаёт встроенную HTML-страницу рабочего места кассира. Страница и API —
// один origin (агент), поэтому fetch на /print, /drawer, /display, /weight идёт
// без CORS-ограничений. Это и есть «браузер кассы → агент → железо».
func (a *Agent) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(posPageHTML))
}

// cors разрешает вызовы агента и из стороннего origin (например, из основного UI
// onebase на другом порту) и отвечает на preflight.
//
// Когда задан токен — безопасность держится на X-Agent-Token, и кросс-origin
// доступ разрешён ("*"): без верного токена команда железа всё равно отклоняется.
// Когда токен НЕ задан (локальная отладка) — проверки нет, поэтому поверхность
// сокращается: разрешаем только same-origin/локальные origin'ы, чтобы
// произвольный сайт в браузере кассы не дёргал команды железа.
func (a *Agent) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if a.token != "" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin == "" || isLocalOrigin(origin) {
			// Отражаем только локальный origin (или его отсутствие — same-origin
			// запросы Origin не шлют), чужие origin'ы CORS-заголовок не получают.
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Agent-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalOrigin сообщает, указывает ли Origin на loopback/частный адрес
// (localhost, 127.0.0.1, ::1, 192.168.x и т.п.). Используется для сужения CORS
// при запуске агента без токена.
func isLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // непустое доменное имя (не localhost) считаем чужим
	}
	return isLocalIP(ip)
}

const posPageHTML = `<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>onebase — Рабочее место кассира</title>
<style>
  body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;margin:0;background:#f1f5f9;color:#0f172a}
  header{background:#0f172a;color:#fff;padding:14px 20px;font-size:18px;font-weight:600}
  .wrap{max-width:760px;margin:18px auto;padding:0 16px;display:grid;gap:16px}
  .card{background:#fff;border:1px solid #e2e8f0;border-radius:10px;padding:16px}
  .card h2{margin:0 0 12px;font-size:15px;color:#334155}
  label{display:block;font-size:12px;color:#64748b;margin:8px 0 3px}
  input{width:100%;box-sizing:border-box;padding:8px 10px;border:1px solid #cbd5e1;border-radius:6px;font-size:14px}
  .row{display:flex;gap:10px;flex-wrap:wrap}
  .row>div{flex:1;min-width:120px}
  .btns{margin-top:12px;display:flex;gap:8px;flex-wrap:wrap}
  button{background:#2563eb;color:#fff;border:none;border-radius:6px;padding:9px 16px;font-size:14px;cursor:pointer}
  button.ghost{background:#e2e8f0;color:#334155}
  #status{margin-top:10px;font-size:13px;min-height:18px}
  .res{font-size:22px;font-weight:700;margin-top:8px}
</style>
</head>
<body>
<header>onebase — Рабочее место кассира</header>
<div class="wrap">

  <div class="card">
    <label>Токен агента (X-Agent-Token, можно оставить пустым)</label>
    <input id="token" placeholder="секрет">
    <div id="status"></div>
  </div>

  <div class="card">
    <h2>Чек (принтер)</h2>
    <label>Порт принтера</label>
    <input id="p_port" value="127.0.0.1:9100">
    <div class="row">
      <div><label>Товар</label><input id="p_item" value="Хлеб"></div>
      <div><label>Количество</label><input id="p_qty" value="2"></div>
      <div><label>Цена</label><input id="p_price" value="30"></div>
      <div><label>Оплата</label><input id="p_pay" value="Наличные"></div>
    </div>
    <div class="btns">
      <button onclick="printReceipt()">Напечатать чек</button>
      <button class="ghost" onclick="openDrawer()">Открыть ящик</button>
    </div>
  </div>

  <div class="card">
    <h2>Дисплей покупателя</h2>
    <label>Порт дисплея</label>
    <input id="d_port" value="127.0.0.1:9101">
    <div class="row">
      <div><label>Строка 1</label><input id="d_l1" value="Хлеб 2 шт"></div>
      <div><label>Строка 2</label><input id="d_l2" value="ИТОГО: 60 руб"></div>
    </div>
    <div class="btns"><button onclick="showDisplay()">Показать</button></div>
  </div>

  <div class="card">
    <h2>Весы</h2>
    <label>Порт весов</label>
    <input id="s_port" value="127.0.0.1:9102">
    <div class="btns"><button onclick="getWeight()">Получить вес</button></div>
    <div class="res" id="s_res"></div>
  </div>

  <div class="card">
    <h2>Сканер штрих-кодов (события)</h2>
    <label>Порт сканера</label>
    <input id="sc_port" value="127.0.0.1:9103">
    <div class="btns">
      <button onclick="startScanner()">Подключить</button>
      <button class="ghost" onclick="stopScanner()">Отключить</button>
    </div>
    <div id="sc_log" style="margin-top:8px;font-size:14px"></div>
  </div>

</div>
<script>
  function val(id){ return document.getElementById(id).value; }
  function num(id){ return parseFloat(document.getElementById(id).value) || 0; }
  function setStatus(msg, ok){ var s=document.getElementById('status'); s.textContent=msg; s.style.color = ok ? '#16a34a' : '#dc2626'; }
  async function call(path, body){
    try{
      var r = await fetch(path, {method:'POST', headers:{'Content-Type':'application/json','X-Agent-Token':val('token')}, body:JSON.stringify(body)});
      var j = await r.json();
      if(!r.ok){ setStatus('Ошибка: ' + (j.error || r.status), false); return null; }
      setStatus('Выполнено', true); return j;
    }catch(e){ setStatus('Нет связи с агентом: ' + e, false); return null; }
  }
  function printReceipt(){
    var sum = num('p_qty') * num('p_price');
    call('/print', { driver:'escpos_tcp', params:{'порт':val('p_port')},
      receipt:{ header:['ООО Ромашка'],
        items:[{name:val('p_item'), qty:num('p_qty'), price:num('p_price'), sum:sum}],
        total:sum, payment:val('p_pay') } });
  }
  function openDrawer(){ call('/drawer', { driver:'escpos_tcp', params:{'порт':val('p_port')} }); }
  function showDisplay(){ call('/display', { driver:'display_tcp', params:{'порт':val('d_port')}, lines:[val('d_l1'), val('d_l2')] }); }
  async function getWeight(){
    var j = await call('/weight', { driver:'scale_tcp', params:{'порт':val('s_port')} });
    if(j){ document.getElementById('s_res').textContent = j.weight + ' кг'; }
  }
  var scannerES = null;
  function startScanner(){
    stopScanner();
    var url = '/events?driver=scanner_tcp&port=' + encodeURIComponent(val('sc_port')) + '&token=' + encodeURIComponent(val('token'));
    scannerES = new EventSource(url);
    scannerES.onmessage = function(e){
      var log = document.getElementById('sc_log');
      log.innerHTML = '<div>📷 ' + e.data + '</div>' + log.innerHTML;
    };
    scannerES.onerror = function(){ setStatus('Сканер: соединение потеряно', false); };
    setStatus('Сканер подключён', true);
  }
  function stopScanner(){ if(scannerES){ scannerES.close(); scannerES = null; } }
</script>
</body>
</html>`
