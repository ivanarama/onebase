package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// newDoctorTestServer поднимает сервер с одним документом, одним справочником и
// одним регистром — минимум, на котором видны находки диагностики.
//
// authRepo намеренно nil: тогда isAdmin пропускает прямой вызов обработчика
// (см. admin.go), и тесту не нужен сеанс.
func newDoctorTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "doctor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	partner := &metadata.Entity{Name: "Контрагенты", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	doc := &metadata.Entity{Name: "Реализация", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Сумма", Type: metadata.FieldTypeNumber},
	}}
	reg := &metadata.Register{
		Name:       "Остатки",
		Dimensions: []metadata.Field{{Name: "Контрагент", Type: "reference:Контрагенты", RefEntity: "Контрагенты"}},
		Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{partner, doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{partner, doc},
		Registers: []*metadata.Register{reg},
	})
	return &Server{store: db, reg: registry}
}

// addOrphanMovement вписывает движение документа, которого нет.
func addOrphanMovement(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.store.Exec(context.Background(),
		`INSERT INTO рег_остатки (id, recorder, recorder_type, line_number, period, вид_движения, сумма)
		 VALUES (?, ?, 'Реализация', 1, '2026-01-15T00:00:00Z', 'Приход', ?)`,
		uuid.New().String(), uuid.New().String(), "100"); err != nil {
		t.Fatal(err)
	}
}

func movementRows(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.store.QueryRow(context.Background(), `SELECT COUNT(*) FROM рег_остатки`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Открытие страницы — диагностика: показывает находки и ничего не меняет.
func TestAdminDoctorPageIsReadOnly(t *testing.T) {
	s := newDoctorTestServer(t)
	addOrphanMovement(t, s)

	w := httptest.NewRecorder()
	s.adminCleanup(w, httptest.NewRequest(http.MethodGet, "/ui/admin/cleanup", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("код ответа %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Диагностика базы", "Движения без документа-регистратора", "Ссылочная целостность"} {
		if !strings.Contains(body, want) {
			t.Errorf("на странице нет %q", want)
		}
	}
	if movementRows(t, s) != 1 {
		t.Fatal("открытие страницы удалило движения")
	}
}

// POST без отмеченных проверок тоже ничего не чинит: исправление запускается
// только по явному выбору.
func TestAdminDoctorPostWithoutSelectionFixesNothing(t *testing.T) {
	s := newDoctorTestServer(t)
	addOrphanMovement(t, s)

	req := httptest.NewRequest(http.MethodPost, "/ui/admin/cleanup", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.adminCleanup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("код ответа %d", w.Code)
	}
	if movementRows(t, s) != 1 {
		t.Fatal("отправка формы без выбора удалила движения")
	}
}

// А с отмеченной проверкой — чинит именно её.
func TestAdminDoctorFixesSelectedCheck(t *testing.T) {
	s := newDoctorTestServer(t)
	addOrphanMovement(t, s)

	form := url.Values{"fix": {"orphan-movements"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/cleanup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.adminCleanup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("код ответа %d", w.Code)
	}
	if got := movementRows(t, s); got != 0 {
		t.Fatalf("сирота не удалена: осталось строк %d", got)
	}
	if !strings.Contains(w.Body.String(), "Исправлено:") {
		t.Error("страница не сообщила об исправлении")
	}
}

func TestAdminDoctorFindsAndFixesOrphanAccountEntry(t *testing.T) {
	s := newDoctorTestServer(t)
	ctx := context.Background()
	reg := &metadata.AccountRegister{
		Name:      "БухУчёт",
		Accounts:  "Основной",
		Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
	}
	if err := s.store.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{reg}); err != nil {
		t.Fatal(err)
	}
	s.reg.LoadAccountRegisters([]*metadata.AccountRegister{reg}, nil)

	period := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{{"счётдт": "41", "счёткт": "60", "сумма": float64(100)}}
	if err := s.store.WriteAccountMovements(ctx, reg.Name, "Реализация", uuid.New(), rows, reg, &period); err != nil {
		t.Fatal(err)
	}

	countRows := func() int {
		var n int
		if err := s.store.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.AccountRegTableName(reg.Name)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	getW := httptest.NewRecorder()
	s.adminCleanup(getW, httptest.NewRequest(http.MethodGet, "/ui/admin/cleanup", nil))
	if getW.Code != http.StatusOK {
		t.Fatalf("GET: код ответа %d", getW.Code)
	}
	for _, want := range []string{reg.Name, "регистратор (Реализация) не существует"} {
		if !strings.Contains(getW.Body.String(), want) {
			t.Fatalf("GET не показал сиротскую проводку (%q): %s", want, getW.Body.String())
		}
	}
	if got := countRows(); got != 1 {
		t.Fatalf("GET изменил проводки бухрегистра: осталось %d", got)
	}

	form := url.Values{"fix": {"orphan-movements"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/cleanup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postW := httptest.NewRecorder()
	s.adminCleanup(postW, req)
	if postW.Code != http.StatusOK {
		t.Fatalf("POST: код ответа %d", postW.Code)
	}
	if got := countRows(); got != 0 {
		t.Fatalf("POST не удалил сиротскую проводку бухрегистра: осталось %d", got)
	}
}
