package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Одна звёздочка раскрывает колонки каждого вхождения источника, включая
// self-JOIN. Карта уникальных имён источников/полей теряла эту кратность:
// SQLite в SQL-странице отдавал вторую защищённую колонку как телефон:1.
func TestAPIV2_ReportMaskJoinStarMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		left := &metadata.Entity{Name: "ЛевыйДубль", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Телефон", Type: metadata.FieldTypeString}}}
		right := &metadata.Entity{Name: "ПравыйДубль", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Телефон", Type: metadata.FieldTypeString}}}
		other := &metadata.Entity{Name: "БезДубля", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Комментарий", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{left, right, other}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		const rawLeft, rawRight = "1111111111", "2222222222"
		id := uuid.New()
		for _, e := range entities {
			fields := map[string]any{"Наименование": e.Name}
			switch e {
			case other:
				fields["Комментарий"] = "Открытый текст"
			case left:
				fields["Телефон"] = rawLeft
			default:
				fields["Телефон"] = rawRight
			}
			if err := db.Upsert(ctx, e.Name, id, fields, e); err != nil {
				t.Fatal(err)
			}
		}
		reports := []*reportpkg.Report{
			{Name: "ЗвездаСоединения", Query: `ВЫБРАТЬ * ИЗ Справочник.ЛевыйДубль КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ПравыйДубль КАК П ПО Л.Ссылка = П.Ссылка`},
			{Name: "ОбратноеСоединение", Query: `ВЫБРАТЬ * ИЗ Справочник.ПравыйДубль КАК П ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ЛевыйДубль КАК Л ПО П.Ссылка = Л.Ссылка`},
			{Name: "СамСоединение", Query: `ВЫБРАТЬ * ИЗ Справочник.ЛевыйДубль КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ЛевыйДубль КАК П ПО Л.Ссылка = П.Ссылка`},
			{Name: "ЗвездаЛевогоИсточника", Query: `ВЫБРАТЬ Л.* ИЗ Справочник.ЛевыйДубль КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ПравыйДубль КАК П ПО Л.Ссылка = П.Ссылка`},
			{Name: "ЗвездаСамСоединения", Query: `ВЫБРАТЬ Л.* ИЗ Справочник.ЛевыйДубль КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ЛевыйДубль КАК П ПО Л.Ссылка = П.Ссылка`},
			{Name: "ОдинИсточник", Query: `ВЫБРАТЬ * ИЗ Справочник.ЛевыйДубль`},
			{Name: "БезКоллизии", Query: `ВЫБРАТЬ * ИЗ Справочник.ЛевыйДубль КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.БезДубля КАК П ПО Л.Ссылка = П.Ссылка`},
			{Name: "БезКоллизииОбратно", Query: `ВЫБРАТЬ * ИЗ Справочник.БезДубля КАК П ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ЛевыйДубль КАК Л ПО П.Ссылка = Л.Ссылка`},
			{Name: "РазныеАлиасы", Query: `ВЫБРАТЬ Л.Телефон КАК Контакт, Л.Телефон КАК Резерв ИЗ Справочник.ЛевыйДубль КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ПравыйДубль КАК П ПО Л.Ссылка = П.Ссылка`},
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: entities, Reports: reports})
		h := &handler{reg: registry, store: db, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)
		runNames := map[string][]string{}
		for _, rep := range reports {
			runNames[rep.Name] = []string{"run"}
		}
		for _, policy := range []auth.FieldPolicy{{Read: "mask_tail", Keep: 4}, {Read: "hide"}} {
			for _, protection := range []string{"both", "left", "right"} {
				t.Run(policy.Read+"/"+protection, func(t *testing.T) {
					policies := map[string]auth.FieldPolicies{}
					if protection != "right" {
						policies[left.Name] = auth.FieldPolicies{"Телефон": policy}
					}
					if protection != "left" {
						policies[right.Name] = auth.FieldPolicies{"Телефон": policy}
					}
					user := apiUser("reader", auth.Permission{
						Reports:     runNames,
						Catalogs:    map[string][]string{left.Name: {"read"}, right.Name: {"read"}, other.Name: {"read"}},
						FieldAccess: auth.FieldAccess{Catalogs: policies},
					})
					for _, rep := range reports {
						t.Run(rep.Name, func(t *testing.T) {
							// self-JOIN без защиты левого источника остаётся разрешённым:
							// повтор обычных имён сам по себе не является основанием отказа.
							denied := rep.Name == "ЗвездаСоединения" || rep.Name == "ОбратноеСоединение" || rep.Name == "СамСоединение" && protection != "right"
							target := "/api/v2/report/" + url.PathEscape(rep.Name) + "?limit=1"
							rec := httptest.NewRecorder()
							router.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), user))
							status := http.StatusOK
							if denied {
								status = http.StatusForbidden
							}
							if rec.Code != status {
								t.Fatalf("код %d, ожидался %d; тело %s", rec.Code, status, rec.Body.String())
							}
							if denied {
								for _, raw := range []string{rawLeft, rawRight} {
									if strings.Contains(rec.Body.String(), raw) {
										t.Fatalf("отказ раскрыл %q: %s", raw, rec.Body.String())
									}
								}
								return
							}
							var response struct {
								Data []map[string]any `json:"data"`
								Meta restV2Meta       `json:"meta"`
							}
							if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
								t.Fatal(err)
							}
							if len(response.Data) != 1 || response.Meta.Total != 1 || response.Meta.Page != 1 || response.Meta.TotalPages != 1 || response.Meta.Limit != 1 || response.Meta.Truncated {
								t.Fatalf("неверная страница: %s", rec.Body.String())
							}
							// Контроли проверяют точную маску, а не только отсутствие сырой строки.
							keys := []string{"телефон"}
							if rep.Name == "РазныеАлиасы" {
								keys = []string{"контакт", "резерв"}
							}
							var want any = rawLeft
							if protection != "right" {
								want = nil
								if policy.Read == "mask_tail" {
									want = "••••••1111"
								}
							} else if rep.Name == "РазныеАлиасы" || rep.Name == "ЗвездаЛевогоИсточника" {
								// Существующий план объединяет политику одноимённых полей источников.
								want = nil
								if policy.Read == "mask_tail" {
									want = "••••••1111"
								}
							}
							for _, key := range keys {
								if got, present := response.Data[0][key]; !present || got != want {
									t.Fatalf("%q = %#v, present=%v; ожидалось %#v", key, got, present, want)
								}
							}
							if protection != "right" || rep.Name == "РазныеАлиасы" || rep.Name == "ЗвездаЛевогоИсточника" {
								if strings.Contains(rec.Body.String(), rawLeft) || strings.Contains(rec.Body.String(), rawRight) {
									t.Fatalf("контроль раскрыл исходное значение: %s", rec.Body.String())
								}
							}
						})
					}
				})
			}
		}
	})
}

// Обычная колонка звёздочки тоже может занять выходной алиас защищённого
// поля. Проверяем HTTP-отказ до выдачи данных при любом порядке проекций и JOIN.
func TestAPIV2_ReportMaskStarAliasCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		secret := &metadata.Entity{Name: "СекретАлиаса", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Телефон", Type: metadata.FieldTypeString}}}
		public := &metadata.Entity{Name: "ПубличныйАлиас", Kind: metadata.KindCatalog, Hierarchical: true,
			Predefined: []*metadata.PredefinedItem{{Name: "Эталон"}},
			Fields:     []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{secret, public}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		const raw, publicText = "5555551234", "Открытый текст"
		id := uuid.New()
		if err := db.Upsert(ctx, secret.Name, id, map[string]any{"Телефон": raw}, secret); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, public.Name, id, map[string]any{"Комментарий": publicText}, public); err != nil {
			t.Fatal(err)
		}
		for _, join := range []struct{ name, from string }{
			{"secret_first", "Справочник.СекретАлиаса КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ПубличныйАлиас КАК П ПО Л.Ссылка = П.Ссылка"},
			{"public_first", "Справочник.ПубличныйАлиас КАК П ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.СекретАлиаса КАК Л ПО П.Ссылка = Л.Ссылка"},
		} {
			type projectionCase struct {
				name, projection string
				denied           bool
			}
			cases := []projectionCase{
				{"safe_star_first", "П.*, Л.Телефон КАК Контакт", false},
				{"safe_star_last", "Л.Телефон КАК Контакт, П.*", false},
			}
			// Служебные колонки входят в фактическую выдачу П.* наравне с
			// обычным Комментарий, хотя их нет в Entity.Fields.
			for _, alias := range []string{"Комментарий", "id", "deletion_mark", "_version", "parent_id", "is_folder", "_predefined_name", "_is_predefined"} {
				cases = append(cases,
					projectionCase{alias + "/star_first", "П.*, Л.Телефон КАК " + alias, true},
					projectionCase{alias + "/star_last", "Л.Телефон КАК " + alias + ", П.*", true})
			}
			for _, tc := range cases {
				for _, policy := range []auth.FieldPolicy{{Read: "mask_tail", Keep: 4}, {Read: "hide"}} {
					t.Run(join.name+"/"+tc.name+"/"+policy.Read, func(t *testing.T) {
						rep := &reportpkg.Report{Name: "КоллизияАлиаса", Query: "ВЫБРАТЬ " + tc.projection + " ИЗ " + join.from}
						registry := runtime.NewRegistry()
						registry.Load(runtime.LoadOptions{Entities: entities, Reports: []*reportpkg.Report{rep}})
						h := &handler{reg: registry, store: db, interp: interpreter.New()}
						router := chi.NewRouter()
						h.mountV2(router)
						user := apiUser("reader", auth.Permission{
							Reports:  map[string][]string{rep.Name: {"run"}},
							Catalogs: map[string][]string{secret.Name: {"read"}, public.Name: {"read"}},
							FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
								secret.Name: {"Телефон": policy},
							}},
						})
						target := "/api/v2/report/" + url.PathEscape(rep.Name) + "?limit=1"
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), user))
						if strings.Contains(rec.Body.String(), raw) {
							t.Errorf("ответ раскрыл исходное значение: %s", rec.Body.String())
						}
						if tc.denied {
							if rec.Code != http.StatusForbidden {
								t.Fatalf("код %d, ожидался Forbidden; тело %s", rec.Code, rec.Body.String())
							}
							return
						}
						if rec.Code != http.StatusOK {
							t.Fatalf("безопасный алиас: код %d; тело %s", rec.Code, rec.Body.String())
						}
						var response struct {
							Data []map[string]any `json:"data"`
							Meta restV2Meta       `json:"meta"`
						}
						if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						if len(response.Data) != 1 || response.Meta.Total != 1 || response.Meta.Page != 1 || response.Meta.TotalPages != 1 || response.Meta.Limit != 1 || response.Meta.Truncated {
							t.Fatalf("неверная страница: %s", rec.Body.String())
						}
						var want any
						if policy.Read == "mask_tail" {
							want = "••••••1234"
						}
						row := response.Data[0]
						if got, present := row["контакт"]; !present || got != want || row["комментарий"] != publicText {
							t.Fatalf("неверная маска или обычный текст: %s; ожидалась маска %#v", rec.Body.String(), want)
						}
					})
				}
			}
		}
	})
}
