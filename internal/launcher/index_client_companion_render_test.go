package launcher

import (
	"strings"
	"testing"
)

// Разметка списка баз для клиентского подключения и сопутствующих приложений.
// Проверяется именно то, что иначе пришлось бы смотреть глазами: у записи, которой
// лаунчер не владеет, не должно быть ни «остановлена», ни кнопок управления.

func TestIndex_ClientBaseShowsServerAddressNotDSN(t *testing.T) {
	vm := &baseVM{Base: &Base{
		ID: "c1", Name: "Сервер КЦ", ServerURL: "https://onebase.example.local:8443",
	}}
	html := renderIndex(t, []*baseVM{vm}, vm)

	if !strings.Contains(html, "https://onebase.example.local:8443") {
		t.Error("в списке нет адреса сервера")
	}
	// Ни порта, ни маркеров локального хранения у такой записи быть не может.
	if strings.Contains(html, "· :0") {
		t.Error("у клиентской записи показан порт, которого у неё нет")
	}
	if strings.Contains(html, "💾") || strings.Contains(html, "🗄") {
		t.Error("у клиентской записи показан маркер локального хранения данных")
	}
}

// «Остановлена» про чужой сервер — ложь: останавливать нечего. Бейдж должен
// говорить об отзывчивости сервера.
func TestIndex_ClientBaseBadgeTalksAboutServerNotProcess(t *testing.T) {
	down := &baseVM{Base: &Base{ID: "c1", Name: "Сервер", ServerURL: "https://srv:8443"}}
	html := renderIndex(t, []*baseVM{down}, down)
	if !strings.Contains(html, "сервер не отвечает") {
		t.Error("нет бейджа «сервер не отвечает»")
	}
	if strings.Contains(html, "остановлена") {
		t.Error("у клиентской записи бейдж «остановлена» — останавливать нечего")
	}

	up := &baseVM{Base: &Base{ID: "c1", Name: "Сервер", ServerURL: "https://srv:8443"}, Running: true}
	html = renderIndex(t, []*baseVM{up}, up)
	if !strings.Contains(html, "сервер отвечает") {
		t.Error("нет бейджа «сервер отвечает»")
	}
	if strings.Contains(html, "работает</span>") && !strings.Contains(html, "сервер отвечает") {
		t.Error("у клиентской записи бейдж процесса вместо состояния сервера")
	}
}

// Конфигуратор лаунчера работает с базой, которой он владеет: для клиентской
// записи кнопки быть не должно, иначе она ведёт в никуда.
func TestIndex_ClientBaseHasNoConfiguratorButton(t *testing.T) {
	client := &baseVM{Base: &Base{ID: "c1", Name: "Сервер", ServerURL: "https://srv:8443"}}
	html := renderIndex(t, []*baseVM{client}, client)
	if strings.Contains(html, "/bases/c1/configurator\"") {
		t.Error("у клиентской записи осталась кнопка «Конфигуратор»")
	}

	// У обычной базы кнопка на месте — запрет адресный, а не глухой.
	local := &baseVM{Base: &Base{
		ID: "l1", Name: "Локальная", ConfigSource: "database",
		DBType: "postgres", DB: "postgres://localhost/x", Port: 8080,
	}}
	html = renderIndex(t, []*baseVM{local}, local)
	if !strings.Contains(html, "/bases/l1/configurator") {
		t.Error("у обычной базы пропала кнопка «Конфигуратор»")
	}
}

// Состояния сопутствующих приложений должны различаться в разметке: «не
// запустилось» и «не поставлено» нельзя показывать одинаково — это разные
// причины и разные действия пользователя.
func TestIndex_CompanionStatesAreDistinguishable(t *testing.T) {
	vm := &baseVM{
		Base: &Base{ID: "c1", Name: "Сервер", ServerURL: "https://srv:8443",
			Companions: []string{"softphone", "scanner", "signer", "idle"}},
		CompanionStates: []CompanionState{
			{Name: "softphone", Declared: true, Running: true},
			{Name: "scanner", Declared: true, Failed: true, Err: "файл не найден"},
			{Name: "signer", Declared: false},
			{Name: "idle", Declared: true},
		},
	}
	html := renderIndex(t, []*baseVM{vm}, vm)

	for _, want := range []string{
		"softphone", "scanner", "signer", "idle",
		"не запустилось", "файл не найден",
		"не поставлено с этим дистрибутивом", "не запущено",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в разметке нет %q", want)
		}
	}
}

// База без сопутствующих приложений не должна получать пустых строк состояния.
func TestIndex_NoCompanionsNoRows(t *testing.T) {
	vm := &baseVM{Base: &Base{
		ID: "l1", Name: "Локальная", ConfigSource: "database",
		DBType: "postgres", DB: "postgres://localhost/x", Port: 8080,
	}}
	html := renderIndex(t, []*baseVM{vm}, vm)
	if strings.Contains(html, "🎧") {
		t.Error("у базы без companion появилась строка состояния помощника")
	}
}
