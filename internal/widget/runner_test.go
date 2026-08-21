package widget

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestFormatKPI_Money(t *testing.T) {
	got := formatKPI(1234567.5, "money")
	// 1 234 567,50 ₽ — NBSP is replaced with regular space in groupDigits
	want := "1 234 567,50 ₽"
	if got != want {
		t.Errorf("money: got %q, want %q", got, want)
	}
}

func TestFormatKPI_Number(t *testing.T) {
	if got := formatKPI(42.0, "number"); got != "42" {
		t.Errorf("number 42: got %q", got)
	}
	if got := formatKPI(1000000.0, "number"); got != "1 000 000" {
		t.Errorf("number 1m: got %q", got)
	}
}

func TestFormatKPI_Percent(t *testing.T) {
	if got := formatKPI(12.345, "percent"); got != "12.3%" {
		t.Errorf("percent: got %q", got)
	}
}

func TestFormatKPI_DefaultInteger(t *testing.T) {
	if got := formatKPI(5.0, ""); got != "5" {
		t.Errorf("default int: got %q", got)
	}
}

func TestFormatKPI_DefaultFloat(t *testing.T) {
	got := formatKPI(3.14, "")
	if got != "3.14" {
		t.Errorf("default float: got %q", got)
	}
}

func TestGroupDigits(t *testing.T) {
	cases := map[int64]string{
		0:        "0",
		7:        "7",
		1234:     "1 234",
		12345:    "12 345",
		1000000:  "1 000 000",
		12345678: "12 345 678",
	}
	for in, want := range cases {
		if got := groupDigits(in); got != want {
			t.Errorf("groupDigits(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstScalar(t *testing.T) {
	row := map[string]any{"x": 42}
	if got := firstScalar(row); got != 42 {
		t.Errorf("firstScalar = %v", got)
	}
	if got := firstScalar(map[string]any{}); got != nil {
		t.Errorf("empty row should give nil, got %v", got)
	}
}

func TestToFloat(t *testing.T) {
	if toFloat(int64(42)) != 42 {
		t.Error("int64")
	}
	if toFloat("3.14") != 3.14 {
		t.Error("string")
	}
	if toFloat(nil) != 0 {
		t.Error("nil")
	}
}

func TestFormatMoney_Negative(t *testing.T) {
	got := formatMoney(-1500.25)
	if !strings.Contains(got, "-") {
		t.Errorf("expected leading -, got %q", got)
	}
	if !strings.Contains(got, "25") {
		t.Errorf("expected fractional 25 kop, got %q", got)
	}
}

func TestRunQuery_RowAccessFiltersRows(t *testing.T) {
	ctx, runner, entity := newRowAccessRunner(t)
	if err := runner.Store.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Наименование": "Allowed", "Owner": "u"}, entity); err != nil {
		t.Fatalf("upsert allowed: %v", err)
	}
	if err := runner.Store.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Наименование": "Hidden", "Owner": "other"}, entity); err != nil {
		t.Fatalf("upsert hidden: %v", err)
	}
	res := runner.Run(ctx, &metadata.Widget{
		Name:  "Товары",
		Type:  metadata.WidgetTypeList,
		Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар",
	})
	if res.Error != "" {
		t.Fatalf("Run list error: %s", res.Error)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected one visible row, got %#v", res.Rows)
	}
	if got := fmt.Sprint(res.Rows[0]["наименование"]); got != "Allowed" {
		t.Fatalf("visible row = %s, want Allowed; rows=%#v", got, res.Rows)
	}
}

func TestRunRecent_RowAccessFiltersHiddenRows(t *testing.T) {
	ctx, runner, entity := newRowAccessRunner(t)
	if err := runner.Store.EnsureAuditSchema(ctx); err != nil {
		t.Fatalf("EnsureAuditSchema: %v", err)
	}

	allowedID := uuid.New()
	hiddenID := uuid.New()
	if err := runner.Store.Upsert(ctx, entity.Name, allowedID, map[string]any{"Наименование": "Allowed", "Owner": "u"}, entity); err != nil {
		t.Fatalf("upsert allowed: %v", err)
	}
	if err := runner.Store.Upsert(ctx, entity.Name, hiddenID, map[string]any{"Наименование": "Hidden", "Owner": "other"}, entity); err != nil {
		t.Fatalf("upsert hidden: %v", err)
	}
	for _, id := range []uuid.UUID{hiddenID, allowedID} {
		if err := runner.Store.Log(ctx, &storage.AuditEntry{
			UserLogin:  "u",
			Action:     "update",
			EntityKind: string(entity.Kind),
			EntityName: entity.Name,
			RecordID:   id.String(),
		}); err != nil {
			t.Fatalf("Log(%s): %v", id, err)
		}
	}

	res := runner.Run(ctx, &metadata.Widget{
		Name:     "Недавние",
		Type:     metadata.WidgetTypeRecent,
		Limit:    10,
		Scope:    "all",
		Entities: []string{entity.Name},
	})
	if res.Error != "" {
		t.Fatalf("Run recent error: %s", res.Error)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected only one visible recent row, got %#v", res.Rows)
	}
	if got := fmt.Sprint(res.Rows[0]["record_id"]); got != allowedID.String() {
		t.Fatalf("visible recent row = %s, want %s; rows=%#v", got, allowedID, res.Rows)
	}
}

// Отказ в доступе к источнику — не «ошибка виджета», а признак AccessDenied:
// дашборд по нему прячет карточку, настоящие ошибки остаются видимыми.
func TestRun_AccessDeniedSource_Flagged(t *testing.T) {
	ctx, runner, _ := newRowAccessRunner(t)
	// Пользователь вообще без прав (пустая непустая секция processors, чтобы
	// не сработала opt-in семантика «нет секции — можно всё»).
	runner.User = &auth.User{Login: "u", Roles: []*auth.Role{{
		Permissions: auth.Permission{Processors: map[string][]string{}},
	}}}
	res := runner.Run(ctx, &metadata.Widget{
		Name:  "Т",
		Type:  metadata.WidgetTypeKPI,
		Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар",
	})
	if !res.AccessDenied {
		t.Fatalf("ожидался AccessDenied, получено %+v", res)
	}
	if !strings.Contains(res.Error, "нет доступа") {
		t.Fatalf("текст отказа должен сохраняться в Error, получено %q", res.Error)
	}
}

func TestRun_CompileError_NotAccessDenied(t *testing.T) {
	ctx, runner, _ := newRowAccessRunner(t)
	res := runner.Run(ctx, &metadata.Widget{
		Name:  "Битый",
		Type:  metadata.WidgetTypeKPI,
		Query: "ВЫБРАТЬ Наименование ИЗ", // синтаксически битый запрос
	})
	if res.Error == "" {
		t.Fatal("ожидалась ошибка компиляции")
	}
	if res.AccessDenied {
		t.Fatalf("ошибка компиляции не должна помечаться AccessDenied: %+v", res)
	}
}

// Кнопки actions-виджета фильтруются по праву write; если правами скрыто всё —
// карточка помечается AccessDenied и уходит с дашборда.
func TestRunActions_FilteredByWriteRights(t *testing.T) {
	ctx, runner, entity := newRowAccessRunner(t)
	w := &metadata.Widget{
		Name: "Создать",
		Type: metadata.WidgetTypeActions,
		Items: []metadata.WidgetAction{
			{Label: "Товар", Entity: entity.Name},
			{Label: "Ссылка", URL: "/ui/report/остатки"},
		},
	}

	// read-only пользователь: кнопка создания скрыта, URL-ссылка остаётся.
	res := runner.Run(ctx, w)
	if res.AccessDenied {
		t.Fatalf("частично доступный actions-виджет не должен прятаться: %+v", res)
	}
	if len(res.Actions) != 1 || res.Actions[0].Label != "Ссылка" {
		t.Fatalf("ожидалась только URL-кнопка, получено %+v", res.Actions)
	}

	// Виджет только из недоступных кнопок → AccessDenied.
	res = runner.Run(ctx, &metadata.Widget{
		Name:  "ТолькоСоздание",
		Type:  metadata.WidgetTypeActions,
		Items: []metadata.WidgetAction{{Label: "Товар", Entity: entity.Name}},
	})
	if !res.AccessDenied || len(res.Actions) != 0 {
		t.Fatalf("полностью отфильтрованный actions-виджет должен быть AccessDenied, получено %+v", res)
	}

	// Открытый деплой (без пользователя) — всё видно.
	runner.User = nil
	res = runner.Run(ctx, w)
	if len(res.Actions) != 2 {
		t.Fatalf("без пользователя должны быть обе кнопки, получено %+v", res.Actions)
	}
}

// new_tab доезжает от метаданных до результата. Признак нужен ссылкам НАРУЖУ
// (на публичный сайт, во внешнюю систему): открытые в той же вкладке, они
// оставляют пользователя без интерфейса и без пути назад.
func TestRunActions_NewTab(t *testing.T) {
	ctx, runner, entity := newRowAccessRunner(t)
	runner.User = nil // права здесь не проверяем

	res := runner.Run(ctx, &metadata.Widget{
		Name: "Ссылки",
		Type: metadata.WidgetTypeActions,
		Items: []metadata.WidgetAction{
			{Label: "Сайт", URL: "/hs/site/", NewTab: true},
			{Label: "Отчёт", URL: "/ui/report/остатки"},
			{Label: "Товар", Entity: entity.Name, NewTab: true},
		},
	})
	if len(res.Actions) != 3 {
		t.Fatalf("ожидались три кнопки, получено %+v", res.Actions)
	}
	if !res.Actions[0].NewTab {
		t.Errorf("внешняя ссылка должна открываться в новой вкладке: %+v", res.Actions[0])
	}
	// Умолчание — прежнее поведение: переход в текущей вкладке.
	if res.Actions[1].NewTab {
		t.Errorf("без new_tab ссылка обязана открываться в той же вкладке: %+v", res.Actions[1])
	}
	// Признак не теряется и на кнопке создания объекта.
	if !res.Actions[2].NewTab {
		t.Errorf("new_tab у entity-кнопки потерялся: %+v", res.Actions[2])
	}
}

func newRowAccessRunner(t *testing.T) (context.Context, *Runner, *metadata.Entity) {
	t.Helper()

	ctx := context.Background()
	entity := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "widgets-rls.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})

	runner := &Runner{
		Reg:         reg,
		Store:       db,
		CurrentUser: "u",
		User: &auth.User{Login: "u", Roles: []*auth.Role{{
			Permissions: auth.Permission{
				Catalogs: map[string][]string{entity.Name: {"read"}},
				RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
					entity.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "u"}}},
				}},
			},
		}}},
	}
	return ctx, runner, entity
}
