package ui

// Регистрация инцидентов (план 116): каждая ошибка, отданная как 500, получает
// короткий код E-…, который пользователь видит на экране и может назвать в
// сообщении об ошибке. Без кода жалоба «нажал — вылезла ошибка» не связывается
// ни со строкой журнала, ни со стеком.

import (
	"net/http"

	"github.com/ivantit66/onebase/internal/incident"
)

// Incidents возвращает журнал инцидентов процесса. Нужен внешнему коду:
// api подключает по нему Recoverer, страница отчёта — список последних ошибок.
func (s *Server) Incidents() *incident.Store { return s.incidents }

// recordIncident регистрирует ошибку и возвращает её код. Пустая строка —
// хранилища нет: так Server собирают прямыми литералами тесты, и падать из-за
// диагностики они не должны.
func (s *Server) recordIncident(r *http.Request, err error) string {
	if s.incidents == nil || err == nil {
		return ""
	}
	return s.incidents.Record(incident.Record{
		Kind:  incident.KindError,
		Where: incident.WhereOf(r),
		Text:  err.Error(),
		User:  currentUserLogin(r),
	}).ID
}

// serverError отвечает 500: локализует ошибку, регистрирует инцидент и
// дописывает его код к тексту.
//
// Заменяет http.Error(w, s.errText(r, err), 500). Коды 400/403/404 через него
// НЕ проводим: несуществующая страница и отказ в доступе — не инциденты, и
// засорять ими журнал значит утопить в нём настоящие сбои.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	http.Error(w, incident.WithCode(s.errText(r, err), s.recordIncident(r, err)), http.StatusInternalServerError)
}

// recordBackgroundPanic регистрирует панику, перехваченную вне HTTP-цепочки
// (фоновые горутины экспорта и заданий — chi Recoverer их не прикрывает).
func (s *Server) recordBackgroundPanic(where, text, stack, user string) string {
	if s.incidents == nil {
		return ""
	}
	return s.incidents.Record(incident.Record{
		Kind:  incident.KindPanic,
		Where: where,
		Text:  text,
		Stack: stack,
		User:  user,
	}).ID
}
