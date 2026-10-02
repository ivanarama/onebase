package launcher

import (
	"strings"
	"testing"
)

// Блокер круга 1: у клиентской записи .Running означает «сервер отвечает», и
// кнопка «Остановить» по этому признаку предлагала остановить чужой процесс —
// нажатие давало отказ, а документация обещала, что кнопки нет.
func TestIndex_ClientBaseHasNoStopButtonWhenServerResponds(t *testing.T) {
	client := &baseVM{
		Base:    &Base{ID: "c1", Name: "Сервер", ServerURL: "https://srv:8443"},
		Running: true,
	}
	html := renderIndex(t, []*baseVM{client}, client)
	if strings.Contains(html, "/bases/c1/stop") {
		t.Error("у клиентской записи показана кнопка «Остановить»")
	}
	// Индикатор доступности при этом остаётся.
	if !strings.Contains(html, "сервер отвечает") {
		t.Error("пропал индикатор доступности сервера")
	}

	// У обычной работающей базы кнопка на месте — запрет адресный.
	local := &baseVM{
		Base: &Base{ID: "l1", Name: "Локальная", ConfigSource: "database",
			DBType: "postgres", DB: "postgres://localhost/x", Port: 8080},
		Running: true,
	}
	html = renderIndex(t, []*baseVM{local}, local)
	if !strings.Contains(html, "/bases/l1/stop") {
		t.Error("у обычной работающей базы пропала кнопка «Остановить»")
	}
}
