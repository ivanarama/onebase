package ui

import (
	"net/http"

	"github.com/ivantit66/onebase/internal/storage"
)

// setFormMode пишет режим открытия форм текущего пользователя (поле формы
// mode = pages | tabs | default) и уводит на вход выбранного режима.
// issue #129/#130.
//
// В безавторизационной базе (login == "") персонального режима не существует:
// эффективный режим анонимной сессии — это глобальный. Поэтому переключатель
// меняет глобальный режим (SaveFormOpenMode), иначе кнопка/радио были бы
// мёртвыми — клик ничего не делал бы (SaveUserFormOpenMode при пустом логине —
// no-op). Для авторизованного пользователя пишется персональный режим.
func (s *Server) setFormMode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	mode := r.FormValue("mode")
	login := currentUserLogin(r)
	var err error
	if login == "" {
		err = s.store.SaveFormOpenMode(r.Context(), mode)
	} else {
		err = s.store.SaveUserFormOpenMode(r.Context(), login, mode)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target := "/ui"
	if s.store.EffectiveFormOpenMode(r.Context(), login) == storage.FormModeTabs {
		target = "/ui/app"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
