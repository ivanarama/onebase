package ui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// detailPanelRecord отдаёт payload боковой панели для ОДНОЙ записи.
//
// Раньше payload уезжал в атрибут data-ob-detail КАЖДОЙ строки списка —
// безусловно, даже когда панель у пользователя выключена (её включение живёт в
// localStorage, сервер о нём не знает). В атрибут попадали ВСЕ реквизиты шапки,
// а не только показанные колонки: полная карточка каждой записи оказывалась в
// DOM, доступном расширению браузера и обычному «сохранить страницу» (#860).
//
// Ленивая загрузка убирает и то, и другое: данных нет в разметке вовсе, а
// запрос уходит только когда панель открыта и строка выбрана. Заодно это тот
// самый ленивый хендлер, которого ждал этап 118D плана.
//
// Границы прав здесь те же, что у карточки: getEntity → requirePerm(read) →
// чтение строки → rowAllowed (строковые политики) → maskRecord (маска ПДн).
// Ни одна из них не «наследуется» из списка: запрос приходит отдельный, и
// проверять его надо целиком.
func (s *Server) detailPanelRecord(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusNotFound)
		return
	}
	if !s.rowAllowed(w, r, entity, "read", row) {
		return
	}
	// Порядок тот же, что в списке (handlers_entity.go): маска ПДн ДО
	// разрешения ссылок, иначе защищённое значение уже стало бы подписью.
	s.maskRecord(r.Context(), entity, row)
	s.resolveRefs(r.Context(), entity, []map[string]any{row})

	lang := s.resolveLang(r)
	payload := detailPanelForEntity(entity, row, s.buildEnumLabels(entity, lang), lang,
		func(key string) string { return s.tr(lang, key) })

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if payload == "" {
		// Пустой состав панели — не ошибка: у сущности может не быть ни одного
		// показываемого реквизита. Клиент покажет «нет данных».
		_, _ = w.Write([]byte(`{"title":"","tabs":[]}`))
		return
	}
	// payload уже сериализован detailPanelForEntity — отдаём как есть, но
	// проверяем, что это валидный JSON: иначе клиент получит мусор молча.
	if !json.Valid([]byte(payload)) {
		s.serverError(w, r, errors.New("панель деталей: некорректный payload"))
		return
	}
	_, _ = w.Write([]byte(payload))
}
