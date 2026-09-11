package ui

import "net/http"

// agentSettings — настройки рабочего места: адрес и токен локального
// device-agent. Серверу хранить нечего (значения per-машина живут в localStorage
// браузера), поэтому страница статична — только рендер формы.
func (s *Server) agentSettings(w http.ResponseWriter, r *http.Request) {
	// Настройки агента оборудования (адрес/токен устройства) — конфигурация
	// уровня администратора. Без этой проверки страница была доступна любому
	// аутентифицированному пользователю (issue #149).
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	s.render(w, r, "page-agent-settings", map[string]any{})
}

// posPage — рабочее место кассира в основном UI: кнопки печати/веса/оплаты/
// фискализации и поле сканера обращаются к локальному агенту из браузера через
// onebaseDevice. Сервер onebase к агенту не ходит — страница статична.
func (s *Server) posPage(w http.ResponseWriter, r *http.Request) {
	// Приложение, не объявившее features.pos, этой страницы не имеет — прямой
	// адрес обязан отвечать так же, как любой несуществующий (issue #1331).
	// Иначе спрятанный пункт меню оставался бы доступен по ссылке, и «в этом
	// приложении РМК нет» было бы неправдой.
	if !s.cfg.POSEnabled {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "page-pos", map[string]any{})
}
