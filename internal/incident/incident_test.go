package incident

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStore_IDsAreUniqueAndFormatted(t *testing.T) {
	s := NewStore(100)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		rec := s.Record(Record{Text: "ошибка"})
		if !strings.HasPrefix(rec.ID, "E-") || len(rec.ID) != 8 {
			t.Fatalf("код инцидента %q не в формате E-XXXXXX", rec.ID)
		}
		if seen[rec.ID] {
			t.Fatalf("код инцидента %q повторился", rec.ID)
		}
		seen[rec.ID] = true
		if rec.Time.IsZero() {
			t.Fatal("время инцидента не проставлено")
		}
		if rec.Kind != KindError {
			t.Fatalf("вид по умолчанию = %q, ожидался %q", rec.Kind, KindError)
		}
	}
}

func TestStore_TruncatesToLimit(t *testing.T) {
	s := NewStore(3)
	var ids []string
	for i := 0; i < 10; i++ {
		ids = append(ids, s.Record(Record{Text: "ошибка"}).ID)
	}
	got := s.Recent("", 100)
	if len(got) != 3 {
		t.Fatalf("в буфере %d записей, ожидалось 3", len(got))
	}
	// Recent отдаёт свежие первыми.
	if got[0].ID != ids[9] || got[2].ID != ids[7] {
		t.Fatalf("порядок нарушен: %s…%s, ожидалось %s…%s", got[0].ID, got[2].ID, ids[9], ids[7])
	}
	// Вытесненный инцидент больше не найти.
	if _, ok := s.Get(ids[0]); ok {
		t.Fatal("вытесненный инцидент всё ещё находится по коду")
	}
}

func TestStore_RecentFiltersByUser(t *testing.T) {
	s := NewStore(10)
	s.Record(Record{Text: "чужая", User: "petrov"})
	mine := s.Record(Record{Text: "моя", User: "ivanov"})
	s.Record(Record{Text: "ничья"})

	got := s.Recent("ivanov", 10)
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("Recent(\"ivanov\") вернул %d записей, ожидалась одна своя", len(got))
	}
	if all := s.Recent("", 10); len(all) != 3 {
		t.Fatalf("Recent(\"\") вернул %d записей, ожидались все три", len(all))
	}
}

func TestStore_GetIsCaseAndSpaceTolerant(t *testing.T) {
	s := NewStore(10)
	rec := s.Record(Record{Text: "ошибка"})

	// Код пользователь переписывает с экрана руками.
	for _, typed := range []string{rec.ID, strings.ToLower(rec.ID), "  " + rec.ID + " "} {
		if got, ok := s.Get(typed); !ok || got.ID != rec.ID {
			t.Fatalf("Get(%q) не нашёл инцидент", typed)
		}
	}
	if _, ok := s.Get("E-000000"); ok {
		t.Fatal("Get нашёл несуществующий код")
	}
	if _, ok := s.Get(""); ok {
		t.Fatal("Get(\"\") должен возвращать false")
	}
}

func TestStore_StackIsTruncated(t *testing.T) {
	s := NewStore(10)
	rec := s.Record(Record{Kind: KindPanic, Stack: strings.Repeat("x", stackLimit*2)})
	if len(rec.Stack) > stackLimit+64 {
		t.Fatalf("стек не обрезан: %d байт", len(rec.Stack))
	}
	if !strings.HasSuffix(rec.Stack, "стек обрезан") {
		t.Fatal("нет пометки об обрезке стека")
	}
}

func TestWhereOf_DropsQueryString(t *testing.T) {
	// В строке запроса едут значения фильтров, введённые пользователем.
	r := httptest.NewRequest("GET", "/ui/catalog/контрагенты?поиск=Иванов&token=secret", nil)
	if got := WhereOf(r); got != "GET /ui/catalog/контрагенты" {
		t.Fatalf("WhereOf = %q, строка запроса должна отбрасываться целиком", got)
	}
	if WhereOf(nil) != "" {
		t.Fatal("WhereOf(nil) должен возвращать пустую строку")
	}
}

func TestRecoverer_RecordsPanicAndShowsCode(t *testing.T) {
	s := NewStore(10)
	userOf := func(*http.Request) string { return "ivanov" }
	h := Recoverer(s, userOf)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("нельзя делить на ноль")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/ui/doc/заказ/new", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа %d, ожидался 500", rr.Code)
	}
	recs := s.Recent("", 10)
	if len(recs) != 1 {
		t.Fatalf("зарегистрировано %d инцидентов, ожидался один", len(recs))
	}
	got := recs[0]
	if got.Kind != KindPanic {
		t.Fatalf("вид %q, ожидался %q", got.Kind, KindPanic)
	}
	if got.Where != "POST /ui/doc/заказ/new" {
		t.Fatalf("место %q", got.Where)
	}
	if !strings.Contains(got.Text, "нельзя делить на ноль") {
		t.Fatalf("текст паники потерян: %q", got.Text)
	}
	if !strings.Contains(got.Stack, "incident.TestRecoverer") {
		t.Fatalf("стек не похож на стек паники: %q", got.Stack)
	}
	if got.User != "ivanov" {
		t.Fatalf("пользователь %q, ожидался ivanov", got.User)
	}
	if !strings.Contains(rr.Body.String(), got.ID) {
		t.Fatalf("код инцидента %s не попал в ответ: %q", got.ID, rr.Body.String())
	}
}

func TestRecoverer_PassesThroughAbortHandler(t *testing.T) {
	s := NewStore(10)
	h := Recoverer(s, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Fatalf("ErrAbortHandler должен пробрасываться дальше, получено %v", rec)
		}
		if n := len(s.Recent("", 10)); n != 0 {
			t.Fatalf("обрыв соединения зарегистрирован как инцидент (%d записей)", n)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ui/", nil))
}

func TestRecoverer_LetsNormalResponsesThrough(t *testing.T) {
	s := NewStore(10)
	h := Recoverer(s, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/ui/", nil))
	if rr.Code != http.StatusTeapot {
		t.Fatalf("код ответа %d, ожидался 418", rr.Code)
	}
	if n := len(s.Recent("", 10)); n != 0 {
		t.Fatalf("успешный ответ зарегистрирован как инцидент (%d записей)", n)
	}
}

func TestWithCode(t *testing.T) {
	if got := WithCode("нет такой колонки", "E-3F7A2C"); !strings.Contains(got, "E-3F7A2C") {
		t.Fatalf("WithCode = %q", got)
	}
	if got := WithCode("нет такой колонки", ""); got != "нет такой колонки" {
		t.Fatalf("без кода текст должен оставаться исходным, получено %q", got)
	}
}
