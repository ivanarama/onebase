package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Интеграционный тест эталонной конфигурации examples/callcenter (план 75):
// входящий звонок (ringing) → адресный скрин-поп оператору через шину (план 74);
// завершение (hangup) → документ Звонок с клиентом/направлением/длительностью.
// Грузим реальный проект, чтобы тест проверял те самые .os/.yaml, что и поставка.
func newCallcenterServer(t *testing.T) (*Server, context.Context, *realtime.Hub) {
	t.Helper()
	ctx := context.Background()

	proj, err := project.Load(filepath.Join("..", "..", "examples", "callcenter"))
	if err != nil {
		t.Fatalf("load callcenter project: %v", err)
	}

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
		t.Fatal(err)
	}

	// Тест проверяет телефонную логику, а не подпись вебхука (hmac покрыт
	// services_*_test.go). Снимаем auth, чтобы слать событие без подписи.
	for _, svc := range proj.HTTPServices {
		svc.Auth = "none"
		svc.Secret = ""
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities:        proj.Entities,
		Programs:        proj.Programs,
		ManagerPrograms: proj.ManagerPrograms,
		ServicePrograms: proj.ServicePrograms,
		PagePrograms:    proj.PagePrograms,
		Registers:       proj.Registers,
		InfoRegs:        proj.InfoRegisters,
		Enums:           proj.Enums,
		Constants:       proj.Constants,
	})
	reg.LoadModules(proj.Modules)
	reg.LoadHTTPServices(proj.HTTPServices)

	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc

	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// /hs/-сервисы гейтятся предохранителем сети (план 62) — включаем.
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	hub := realtime.NewHub()
	s := &Server{
		store:            db,
		reg:              reg,
		interp:           interp,
		authRepo:         authRepo,
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
		loginLimit:       auth.NewLoginLimiter(5, time.Minute),
		hub:              hub,
	}

	// Демо-данные: оператор ivan на экстеншене 101 и клиент с известным номером.
	opEnt := reg.GetEntity("Оператор")
	if opEnt == nil {
		t.Fatal("entity Оператор не зарегистрирован")
	}
	if _, err := db.WriteCatalogRecord(ctx, opEnt, "", map[string]any{
		"наименование": "Иван Операторов",
		"логин":        "ivan",
		"экстеншен":    "101",
		"активный":     true,
	}); err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	clEnt := reg.GetEntity("Клиент")
	if _, err := db.WriteCatalogRecord(ctx, clEnt, "", map[string]any{
		"наименование": "ООО Ромашка",
		"телефон":      "+7 999 000-11-22",
	}); err != nil {
		t.Fatalf("seed client: %v", err)
	}

	return s, ctx, hub
}

func TestTelephony_RingingScreenPopAndHangupLogsCall(t *testing.T) {
	s, ctx, hub := newCallcenterServer(t)

	// Оператор ivan подписан на шину (как открытая вкладка панели).
	_, ch, cancel := hub.Subscribe("uid-ivan", "ivan", nil)
	defer cancel()

	// 1) Входящий звонок (ringing) → адресный скрин-поп оператору ivan.
	ring := `{"state":"ringing","direction":"in","from":"+7 (999) 000-11-22","to":"101","operator":"101","call_id":"call-1"}`
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("POST", "/hs/telephony/event", strings.NewReader(ring)))
	if w.Code != http.StatusOK {
		t.Fatalf("ringing: status=%d body=%s", w.Code, w.Body.String())
	}

	select {
	case ev := <-ch:
		if ev.Name != "звонок.входящий" {
			t.Fatalf("событие=%q, ждали звонок.входящий", ev.Name)
		}
		js, err := interpreter.MarshalDSLValue(ev.Data)
		if err != nil {
			t.Fatalf("сериализация данных события: %v", err)
		}
		body := string(js)
		if !strings.Contains(body, "ООО Ромашка") {
			t.Errorf("в скрин-попе нет имени клиента: %s", body)
		}
		if !strings.Contains(body, "call-1") {
			t.Errorf("в скрин-попе нет id звонка: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("скрин-поп не доставлен оператору ivan")
	}

	// 2) Завершение (hangup) → создан документ Звонок.
	hangup := `{"state":"hangup","direction":"in","from":"+79990001122","to":"101","operator":"101","call_id":"call-1","duration":125}`
	w = httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("POST", "/hs/telephony/event", strings.NewReader(hangup)))
	if w.Code != http.StatusOK {
		t.Fatalf("hangup: status=%d body=%s", w.Code, w.Body.String())
	}

	got := readCallsSummary(t, s, ctx)
	if !strings.HasPrefix(got, "1 ") {
		t.Fatalf("ждали ровно 1 документ Звонок, получили: %q", got)
	}
	if !strings.Contains(got, "call-1") || !strings.Contains(got, "Входящий") {
		t.Errorf("документ Звонок без верных реквизитов: %q", got)
	}
	if !strings.Contains(got, "клиент:да") {
		t.Errorf("в документе Звонок не проставлен клиент: %q", got)
	}
}

func TestTelephony_LookupByNumber(t *testing.T) {
	s, _, _ := newCallcenterServer(t)

	// Известный номер (в другом формате) → 200 + наименование клиента.
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/telephony/lookup?number=8(999)000-11-22", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("lookup известного: status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Ромашка") {
		t.Errorf("lookup не вернул клиента: %s", w.Body.String())
	}

	// Неизвестный номер → 404.
	w = httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/telephony/lookup?number=+70000000000", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("lookup неизвестного: status=%d, ждали 404", w.Code)
	}
}

// readCallsSummary читает документы Звонок DSL-запросом и возвращает строку
// "<кол-во> <внешнийId>|<направление>|клиент:<да|нет>;…" для простых проверок.
func readCallsSummary(t *testing.T, s *Server, ctx context.Context) string {
	t.Helper()
	vars, _ := s.buildDSLVarsTx(ctx, nil)
	prog := mustParse(t, `Функция Проверка() Экспорт
  З = Новый Запрос;
  З.Текст = "ВЫБРАТЬ ВнешнийId, Направление, Клиент, ДлительностьМинут ИЗ Документ.Звонок";
  Кол = 0;
  Итог = "";
  Для Каждого Стр Из З.Выполнить() Цикл
    Кол = Кол + 1;
    КлиентЕсть = "нет";
    Если ЗначениеЗаполнено(Стр.Клиент) Тогда
      КлиентЕсть = "да";
    КонецЕсли;
    Итог = Итог + Строка(Стр.ВнешнийId) + "|" + Строка(Стр.Направление) + "|клиент:" + КлиентЕсть + ";";
  КонецЦикла;
  Возврат Строка(Кол) + " " + Итог;
КонецФункции`)
	var proc *ast.ProcedureDecl
	for _, p := range prog.Procedures {
		if strings.EqualFold(p.Name.Literal, "Проверка") {
			proc = p
			break
		}
	}
	if proc == nil {
		t.Fatal("процедура Проверка не распарсилась")
	}
	res, err := s.interp.Call(proc, nil, nil, vars)
	if err != nil {
		t.Fatalf("DSL-запрос документов: %v", err)
	}
	return fmt.Sprintf("%v", res)
}

// АдресОригинации собирает корректный ARI-запрос (endpoint оператора, extension
// = набираемый номер) и НЕ складывает числовые строки через "+" (готча DSL).
func TestTelephony_OriginateURL(t *testing.T) {
	s, ctx, _ := newCallcenterServer(t)
	vars, _ := s.buildDSLVarsTx(ctx, nil)
	proc := s.reg.GetModuleNamespacedProc("Телефония", "АдресОригинации")
	if proc == nil {
		t.Fatal("функция Телефония.АдресОригинации не найдена в модуле")
	}
	res, err := s.interp.Call(proc, nil, []any{"101", "79990001122", "from-internal", "ariuser", "aripass"}, vars)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	want := "/ari/channels?endpoint=PJSIP/101&extension=79990001122&context=from-internal&priority=1&api_key=ariuser:aripass"
	if got := fmt.Sprintf("%v", res); got != want {
		t.Fatalf("URL originate неверный:\n got=%s\nwant=%s", got, want)
	}
}

// Панель формируется целиком: ПриФормировании наполняет построитель блоками
// (KPI, таблица клиентов с засеянным клиентом, журнал с сортировкой УБЫВ, список
// ссылок). Проверяем блоки напрямую — без шаблонов/HTTP, — что заодно
// подтверждает: все запросы страницы (вкл. УБЫВ) и вызовы модуля выполнились.
func TestTelephony_PanelRenders(t *testing.T) {
	s, ctx, _ := newCallcenterServer(t)
	proc := s.reg.GetPageProcedure("Телефония", "ПриФормировании")
	if proc == nil {
		t.Fatal("ПриФормировании не найдена в Телефония.page.os")
	}
	builder := interpreter.NewPageBuilder()
	params := interpreter.NewStringMap(map[string]string{})
	vars, _ := s.buildDSLVarsTx(ctx, nil)
	vars["Страница"] = builder
	vars["Параметры"] = params
	if _, err := s.interp.Call(proc, builder, []any{builder, params}, vars); err != nil {
		t.Fatalf("ПриФормировании: %v", err)
	}

	var hasKPI, hasClient, hasJournal, hasLinks bool
	for _, b := range builder.Blocks() {
		switch {
		case b.Kind == "kpi" && strings.Contains(b.Label, "Звонков"):
			hasKPI = true
		case b.Kind == "table" && b.Title == "Клиенты":
			for _, row := range b.Rows {
				if strings.Contains(row.Cells["Наименование"].Text, "Ромашка") {
					hasClient = true
				}
			}
		case b.Kind == "table" && b.Title == "Последние звонки":
			hasJournal = true
		case b.Kind == "list" && b.Title == "Быстрые ссылки":
			hasLinks = true
		}
	}
	if !hasKPI || !hasClient || !hasJournal || !hasLinks {
		t.Fatalf("панель неполна: kpi=%v client=%v journal=%v links=%v", hasKPI, hasClient, hasJournal, hasLinks)
	}
}

// Общий layout содержит слушатель шины для входящего звонка (план 75): без него
// скрин-поп не виден ни на одной странице (страницы JS не инжектят).
func TestTelephony_LayoutHasCallToast(t *testing.T) {
	src := templateSource()
	if !strings.Contains(src, `src="/static/ui.js"`) {
		t.Fatalf("layout не подключает общий ui.js")
	}
	js := string(uiJS)
	for _, want := range []string{"onebase:звонок.входящий", "callToast"} {
		if !strings.Contains(js, want) {
			t.Errorf("ui.js не содержит %q (глобальная всплывашка входящего звонка)", want)
		}
	}
}
