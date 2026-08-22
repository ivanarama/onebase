package launcher

// Лечение отказа запуска кнопкой (#1067).
//
// Проверяется то, что видит пользователь: ответ на неудавшийся запуск, ответ
// обработчика кнопки и разметка окна с ошибкой. Дочерний процесс подменён швом
// renumberBase — его собственный контракт держит тест на стороне команды
// (TestRenumberJSONMatchesLauncherContract).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/storage"
)

// startFailureError воспроизводит ошибку, с которой лаунчер приходит из
// WaitReady: процесс базы упал на миграции, и в ошибке лежит хвост его лога.
func startFailureError() error {
	return i18nerr.Errorf("процесс базы завершился при запуске — причина в конце лога (%s): %s",
		"/tmp/base.log",
		"\n\nОшибка запуска onebase:\n\nmigrate: migrate Контрагент indexes: Контрагент: уникальность Код "+
			"включена, но у 9 записей значение пусто; пустые значения уникальный индекс не ловит — "+
			"дозаполните их командой "+storage.RenumberHint)
}

// fakeRenumber подменяет дочерний процесс отчётом.
func fakeRenumber(t *testing.T, rep RenumberReport, err error) *[]bool {
	t.Helper()
	var writes []bool
	old := renumberBase
	renumberBase = func(_ *Runner, _ context.Context, _ *Base, write bool) (RenumberReport, error) {
		writes = append(writes, write)
		return rep, err
	}
	t.Cleanup(func() { renumberBase = old })
	return &writes
}

func renumberTestBase(t *testing.T) (*handler, *Base) {
	t.Helper()
	store := newTestStore(t)
	b := &Base{
		ID: "fix-me", Name: "Торговля", ConfigSource: "file",
		Path: t.TempDir(), DBType: "sqlite", DBPath: t.TempDir() + "/base.db", Port: freePort(t),
	}
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}
	return &handler{store: store, runner: NewRunner()}, b
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%s)", err, rec.Body.String())
	}
	return body
}

// Ради этого заведена вся связка: отказ запуска несёт не только текст, но и
// готовое действие — иначе пользователю остаётся кнопка OK.
func TestStartFailureOffersRenumberFix(t *testing.T) {
	h, b := renumberTestBase(t)
	fakeRenumber(t, RenumberReport{Objects: []RenumberObject{
		{Object: "Контрагент", Field: "Код", Empty: 9},
		{Object: "РеализацияТоваров", Field: "Номер", Empty: 0},
	}}, nil)

	rec := httptest.NewRecorder()
	h.startFailure(rec, httptest.NewRequest(http.MethodPost, "/bases/fix-me/start", nil), b, startFailureError())

	if rec.Code != 500 {
		t.Fatalf("код ответа = %d, ожидался 500", rec.Code)
	}
	body := decodeJSON(t, rec)
	if text, _ := body["error"].(string); !strings.Contains(text, "уникальность") {
		t.Errorf("в ответе нет причины отказа: %v", body["error"])
	}
	fix, ok := body["fix"].(map[string]any)
	if !ok {
		t.Fatalf("платформа знает лекарство, но не предложила его: %s", rec.Body.String())
	}
	if fix["kind"] != "renumber" {
		t.Errorf("kind = %v, ожидался renumber", fix["kind"])
	}
	if fix["empty"] != float64(9) {
		t.Errorf("empty = %v, ожидалось 9 — в диалоге показывается объём", fix["empty"])
	}
	objects, _ := fix["objects"].([]any)
	if len(objects) != 1 {
		t.Fatalf("в предложении %d объектов, ожидался один: объекты без пустых значений сюда не попадают", len(objects))
	}
	first, _ := objects[0].(map[string]any)
	if first["object"] != "Контрагент" || first["field"] != "Код" {
		t.Errorf("объект/поле = %v/%v, ожидались Контрагент/Код", first["object"], first["field"])
	}
}

// Отказ другого происхождения лечить нечем — кнопки быть не должно, и лишний
// дочерний процесс ради этого запускаться не должен тоже.
func TestStartFailureWithoutRemedyOffersNothing(t *testing.T) {
	h, b := renumberTestBase(t)
	calls := fakeRenumber(t, RenumberReport{Objects: []RenumberObject{{Object: "Контрагент", Field: "Код", Empty: 9}}}, nil)

	rec := httptest.NewRecorder()
	h.startFailure(rec, httptest.NewRequest(http.MethodPost, "/bases/fix-me/start", nil), b,
		i18nerr.Errorf("порт %d уже занят другим процессом", 8080))

	if _, ok := decodeJSON(t, rec)["fix"]; ok {
		t.Errorf("предложено дозаполнение кодов на отказе по занятому порту: %s", rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Errorf("дочерний renumber запущен %d раз(а) на неподходящем отказе", len(*calls))
	}
}

// Разведка сорвалась — пользователь всё равно обязан увидеть причину отказа.
func TestStartFailureSurvivesProbeError(t *testing.T) {
	h, b := renumberTestBase(t)
	fakeRenumber(t, RenumberReport{}, errors.New("нет доступа к базе"))

	rec := httptest.NewRecorder()
	h.startFailure(rec, httptest.NewRequest(http.MethodPost, "/bases/fix-me/start", nil), b, startFailureError())

	body := decodeJSON(t, rec)
	if _, ok := body["fix"]; ok {
		t.Errorf("предложение построено на сорвавшейся разведке: %s", rec.Body.String())
	}
	if text, _ := body["error"].(string); !strings.Contains(text, "уникальность") {
		t.Errorf("причина отказа потерялась: %v", body["error"])
	}
}

// Разведка ничего не нашла (кто-то уже дозаполнил из консоли) — предлагать
// нечего.
func TestStartFailureWithoutEmptyValuesOffersNothing(t *testing.T) {
	h, b := renumberTestBase(t)
	fakeRenumber(t, RenumberReport{Objects: []RenumberObject{{Object: "Контрагент", Field: "Код", Empty: 0}}}, nil)

	rec := httptest.NewRecorder()
	h.startFailure(rec, httptest.NewRequest(http.MethodPost, "/bases/fix-me/start", nil), b, startFailureError())
	if _, ok := decodeJSON(t, rec)["fix"]; ok {
		t.Errorf("предложение построено на пустом отчёте: %s", rec.Body.String())
	}
}

func postRenumber(t *testing.T, h *handler, id, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bases/"+id+"/renumber"+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.renumber(rec, req)
	return rec
}

func TestRenumberHandlerWritesOnlyWhenAsked(t *testing.T) {
	h, b := renumberTestBase(t)
	calls := fakeRenumber(t, RenumberReport{Write: true, Objects: []RenumberObject{
		{Object: "Контрагент", Field: "Код", Empty: 9, Filled: 9},
	}}, nil)

	rec := postRenumber(t, h, b.ID, "?write=1")
	if rec.Code != 200 {
		t.Fatalf("код ответа = %d (%s)", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if body["filled"] != float64(9) {
		t.Errorf("filled = %v, ожидалось 9 — эту цифру видит пользователь", body["filled"])
	}
	if len(*calls) != 1 || !(*calls)[0] {
		t.Fatalf("запись не запрошена у команды: %v", *calls)
	}

	// Без флага — только подсчёт: тот же контракт, что у консольной команды.
	if rec := postRenumber(t, h, b.ID, ""); rec.Code != 200 {
		t.Fatalf("разведка вернула %d (%s)", rec.Code, rec.Body.String())
	}
	if len(*calls) != 2 || (*calls)[1] {
		t.Fatalf("разведка ушла в команду как запись: %v", *calls)
	}
}

func TestRenumberHandlerPreservesSkippedObjects(t *testing.T) {
	h, b := renumberTestBase(t)
	fakeRenumber(t, RenumberReport{Write: true, Objects: []RenumberObject{
		{Object: "Контрагент", Field: "Код", Empty: 2, Filled: 2},
		{Object: "Номенклатура", Field: "Код", Error: "нет колонок is_folder, parent_id"},
	}}, nil)

	rec := postRenumber(t, h, b.ID, "?write=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа = %d (%s)", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	skipped, ok := body["skipped"].([]any)
	if !ok || len(skipped) != 1 {
		t.Fatalf("skipped потерян в HTTP-ответе: %#v", body["skipped"])
	}
	obj, _ := skipped[0].(map[string]any)
	if obj["object"] != "Номенклатура" || obj["error"] != "нет колонок is_folder, parent_id" {
		t.Fatalf("неверный skipped: %#v", obj)
	}
}

// Запись в базу под работающим сервером недопустима: файл SQLite открыт им.
func TestRenumberHandlerRefusesRunningBase(t *testing.T) {
	h, b := renumberTestBase(t)
	calls := fakeRenumber(t, RenumberReport{}, nil)

	h.runner.procs[b.ID] = &managedProc{port: b.Port}

	rec := postRenumber(t, h, b.ID, "?write=1")
	if rec.Code != http.StatusConflict {
		t.Fatalf("код ответа = %d, ожидался 409 (%s)", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Errorf("команда запущена на работающей базе: %v", *calls)
	}
}

func TestRenumberHandlerUnknownBase(t *testing.T) {
	h, _ := renumberTestBase(t)
	if rec := postRenumber(t, h, "нет-такой", "?write=1"); rec.Code != 404 {
		t.Fatalf("код ответа = %d, ожидался 404 (%s)", rec.Code, rec.Body.String())
	}
}
