package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Списки регистров накопления обязаны применять field_access.registers (#859).
//
// #767 вывел маскирование ПДн на границы журналов и регистров СВЕДЕНИЙ, а
// /ui/register/* остались без него: права на объект и строковый отбор
// применялись, маска полей — нет.
//
// При разборе выяснилось хуже, чем в заявке: секцию field_access.registers не
// читал НИКТО — ни UI, ни DSL, ни запросы. Конфигурация её принимала, роль
// выглядела настроенной, а защиты не было нигде. Поэтому тест проверяет оба
// пути чтения регистра: HTTP-список и объект DSL.

func maskedRegisterFixture(t *testing.T) (*Server, context.Context, *metadata.Register, *auth.User, string) {
	t.Helper()
	goods := &metadata.Entity{
		Name:   "Товары",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	reg := &metadata.Register{
		Name: "Продажи",
		Dimensions: []metadata.Field{
			{Name: "Товар", Type: "reference:Товары", RefEntity: goods.Name},
		},
		Resources: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Себестоимость", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{goods})
	if err := s.store.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{goods}, Registers: []*metadata.Register{reg}})

	productID := uuid.New()
	if err := s.store.Upsert(ctx, goods.Name, productID, map[string]any{"Наименование": "Стул"}, goods); err != nil {
		t.Fatal(err)
	}
	const secret = "СЕБЕСТОИМОСТЬ-754"
	period := time.Date(2026, time.August, 15, 0, 0, 0, 0, time.UTC)
	if err := s.store.WriteMovements(ctx, reg.Name, "Реализация", uuid.New(),
		[]map[string]any{{
			"Товар": productID.String(), "Количество": 3, "Себестоимость": secret, "тип": "+",
		}}, reg, &period); err != nil {
		t.Fatalf("движения: %v", err)
	}

	user := &auth.User{ID: "reg-reader", Login: "reg-reader", Roles: []*auth.Role{{
		Name: "Читатель регистра",
		Permissions: auth.Permission{
			Catalogs:  map[string][]string{goods.Name: {"read"}},
			Registers: map[string][]string{reg.Name: {"read"}},
			FieldAccess: auth.FieldAccess{Registers: map[string]auth.FieldPolicies{
				reg.Name: {"Себестоимость": {Read: "mask_all"}},
			}},
		},
	}}}
	return s, ctx, reg, user, secret
}

func TestUI_RegisterLists_ПрименяютМаскуПолей(t *testing.T) {
	s, _, reg, user, secret := maskedRegisterFixture(t)

	for _, c := range []struct {
		name string
		path string
		call func(w http.ResponseWriter, r *http.Request)
	}{
		{"движения", "/ui/register/" + reg.Name, s.registerMovements},
		{"остатки", "/ui/register/" + reg.Name + "/balances", s.registerBalances},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := reqWithChi(http.MethodGet, c.path, nil, map[string]string{"name": reg.Name})
			r = r.WithContext(auth.ContextWithUser(r.Context(), user))
			w := httptest.NewRecorder()
			c.call(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("код %d: %s", w.Code, w.Body.String())
			}
			page := w.Body.String()
			if strings.Contains(page, secret) {
				t.Fatalf("защищённое значение уехало в HTML списка регистра")
			}
			if !strings.Contains(page, "••••••") {
				t.Errorf("маски в выдаче нет вовсе — проверьте, что строка вообще отрисована:\n%.400s", page)
			}
		})
	}
}

// Тот же регистр, прочитанный из модуля: политика на регистр не должна зависеть
// от того, читают его глазами или кодом.
func TestDSL_РегистрыНакопления_ПрименяютМаскуПолей(t *testing.T) {
	s, ctx, reg, user, secret := maskedRegisterFixture(t)
	ctx = auth.ContextWithUser(ctx, user)

	rows, err := s.store.GetMovements(ctx, reg.Name, reg, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("подготовка: движений нет")
	}
	s.maskRegisterRecords(ctx, reg, rows)
	for _, row := range rows {
		for k, v := range row {
			if str, ok := v.(string); ok && strings.Contains(str, secret) {
				t.Fatalf("поле %q отдано модулю без маски: %v", k, v)
			}
		}
	}
}
