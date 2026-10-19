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

// Повторяющееся имя выходной колонки обходило полевую маску: план адресует
// имя колонки, а движок переименовывает дубль (`телефон:1` на SQLite) либо
// схлопывает его в карте строки. Первый ключ проходил проверку, второй уходил
// клиенту сырым. Тест матричный, потому что переименование — поведение
// конкретного диалекта, и текстовая проверка SQL его не поймала бы.
func TestAPIV2_ReportMaskDuplicateColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name: "КлиентМаски",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Телефон", Type: metadata.FieldTypeString},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		const raw = "1234567890"
		if err := db.Upsert(ctx, entity.Name, uuid.New(),
			map[string]any{"Наименование": "Клиент", "Телефон": raw}, entity); err != nil {
			t.Fatalf("запись: %v", err)
		}

		reports := []*reportpkg.Report{
			{Name: "ОдинТелефон", Query: `ВЫБРАТЬ Телефон ИЗ Справочник.КлиентМаски`},
			{Name: "ДваТелефона", Query: `ВЫБРАТЬ Телефон, Телефон ИЗ Справочник.КлиентМаски`},
			{Name: "ДваАлиаса", Query: `ВЫБРАТЬ Телефон КАК Контакт, Телефон КАК Контакт ИЗ Справочник.КлиентМаски`},
			{Name: "РазныеАлиасы", Query: `ВЫБРАТЬ Телефон КАК Контакт, Телефон КАК Резерв ИЗ Справочник.КлиентМаски`},
			{Name: "ПолеИАлиас", Query: `ВЫБРАТЬ Телефон, Телефон КАК Контакт ИЗ Справочник.КлиентМаски`},
			{Name: "КоллизияАлиасов", Query: `ВЫБРАТЬ Телефон КАК Контакт, Наименование КАК Контакт ИЗ Справочник.КлиентМаски`},
			{Name: "Звёздочка", Query: `ВЫБРАТЬ * ИЗ Справочник.КлиентМаски`},
			{Name: "ЗвёздочкаИТелефон", Query: `ВЫБРАТЬ *, Телефон ИЗ Справочник.КлиентМаски`},
			{Name: "ДваИмени", Query: `ВЫБРАТЬ Телефон, Наименование, Наименование КАК Наименование ИЗ Справочник.КлиентМаски`},
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Reports: reports})
		h := &handler{reg: registry, store: db, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)

		runNames := map[string][]string{}
		for _, rep := range reports {
			runNames[rep.Name] = []string{"run"}
		}
		maskUser := func(policy auth.FieldPolicy) *auth.User {
			return apiUser("reader", auth.Permission{
				Reports:  runNames,
				Catalogs: map[string][]string{entity.Name: {"read"}},
				FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
					entity.Name: {"Телефон": policy},
				}},
			})
		}

		for _, policy := range []auth.FieldPolicy{
			{Read: "mask_tail", Keep: 4},
			{Read: "hide"},
		} {
			user := maskUser(policy)
			for _, c := range []struct {
				report     string
				wantStatus int
			}{
				// Однозначные колонки по-прежнему обслуживаются и маскируются.
				{"ОдинТелефон", http.StatusOK},
				{"Звёздочка", http.StatusOK},
				{"ДваИмени", http.StatusOK},
				{"РазныеАлиасы", http.StatusOK},
				{"ПолеИАлиас", http.StatusOK},
				// Неоднозначная выдача отклоняется целиком, а не отдаётся
				// частично замаскированной.
				{"ДваТелефона", http.StatusForbidden},
				{"ДваАлиаса", http.StatusForbidden},
				{"КоллизияАлиасов", http.StatusForbidden},
				{"ЗвёздочкаИТелефон", http.StatusForbidden},
			} {
				target := "/api/v2/report/" + url.PathEscape(c.report) + "?limit=1"
				req := withUser(httptest.NewRequest(http.MethodGet, target, nil), user)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != c.wantStatus {
					t.Fatalf("%s/%s: код %d, ожидался %d; тело %s",
						policy.Read, c.report, rec.Code, c.wantStatus, rec.Body.String())
				}
				if rec.Code != http.StatusOK {
					continue
				}
				var response struct {
					Data []map[string]any `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatalf("%s/%s: разбор ответа: %v", policy.Read, c.report, err)
				}
				if len(response.Data) != 1 {
					t.Fatalf("%s/%s: строк %d, ожидалась одна", policy.Read, c.report, len(response.Data))
				}
				var maskedKeys []string
				switch c.report {
				case "РазныеАлиасы":
					maskedKeys = []string{"контакт", "резерв"}
				case "ПолеИАлиас":
					maskedKeys = []string{"телефон", "контакт"}
				}
				for _, key := range maskedKeys {
					var want any
					if policy.Read == "mask_tail" {
						want = "••••••7890"
					}
					if value, present := response.Data[0][key]; !present || value != want {
						t.Fatalf("%s/%s: колонка %q = %#v, present=%v; ожидалось %#v",
							policy.Read, c.report, key, value, present, want)
					}
				}
				for key, value := range response.Data[0] {
					if text, ok := value.(string); ok && strings.Contains(text, raw) {
						t.Fatalf("%s/%s: колонка %q отдала исходное значение %q",
							policy.Read, c.report, key, text)
					}
				}
			}
		}
	})
}

// Ссылочный вариант того же дефекта (круг 4 #1428): защищённая ссылка выводится
// под логическим именем (`секрет`), а «*» и авто-JOIN адресуют ту же колонку по
// физическому имени (`секрет_id`), поэтому сопоставление имён их дубль не
// видело: явная Секрет рядом со «*» ставила авто-JOIN связанного справочника, и
// обёртка пагинации отдавала его колонки как id:1/наименование:1 — сырую ссылку
// и имя скрытой записи при HTTP 200. Неоднозначная выдача теперь отклоняется
// целиком. Тест матричный по той же причине: раскладка дублей — поведение
// обёртки конкретного диалекта.
func TestAPIV2_ReportMaskDuplicateReferenceColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		secret := &metadata.Entity{
			Name: "Тайны",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
			},
		}
		source := &metadata.Entity{
			Name: "Скрытые",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Секрет", Type: "reference:Тайны", RefEntity: "Тайны"},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{secret, source}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		secretID := uuid.New()
		if err := db.Upsert(ctx, secret.Name, secretID,
			map[string]any{"Наименование": "Тайна-1"}, secret); err != nil {
			t.Fatalf("запись %s: %v", secret.Name, err)
		}
		if err := db.Upsert(ctx, source.Name, uuid.New(),
			map[string]any{"Наименование": "Строка", "Секрет": secretID}, source); err != nil {
			t.Fatalf("запись %s: %v", source.Name, err)
		}

		reports := []*reportpkg.Report{
			{Name: "СсылкаОдна", Query: `ВЫБРАТЬ Секрет ИЗ Справочник.Скрытые`},
			{Name: "РазныеАлиасыСсылки", Query: `ВЫБРАТЬ Секрет КАК Контакт, Секрет КАК Резерв ИЗ Справочник.Скрытые`},
			{Name: "ДублиАлиасовСсылки", Query: `ВЫБРАТЬ Секрет КАК Контакт, Секрет КАК Контакт ИЗ Справочник.Скрытые`},
			{Name: "ЗвёздочкаИАлиасСсылки", Query: `ВЫБРАТЬ *, Секрет КАК Контакт ИЗ Справочник.Скрытые`},
			{Name: "ЗвёздочкаСсылки", Query: `ВЫБРАТЬ * ИЗ Справочник.Скрытые`},
			{Name: "ЗвёздочкаИСсылка", Query: `ВЫБРАТЬ *, Секрет ИЗ Справочник.Скрытые`},
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{secret, source}, Reports: reports})
		h := &handler{reg: registry, store: db, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)

		runNames := map[string][]string{}
		for _, rep := range reports {
			runNames[rep.Name] = []string{"run"}
		}
		user := apiUser("reader", auth.Permission{
			Reports:  runNames,
			Catalogs: map[string][]string{secret.Name: {"read"}, source.Name: {"read"}},
			FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
				source.Name: {"Секрет": {Read: "hide"}},
			}},
		})

		for _, c := range []struct {
			report     string
			wantStatus int
		}{
			// Однозначная выдача обслуживается, ссылка под маской обнуляется.
			{"СсылкаОдна", http.StatusOK},
			{"РазныеАлиасыСсылки", http.StatusOK},
			{"ЗвёздочкаСсылки", http.StatusOK},
			// «*» и явная ссылка — дубль одной защищённой колонки: отказ до
			// выполнения запроса, а не частично замаскированный ответ с id:1.
			{"ЗвёздочкаИСсылка", http.StatusForbidden},
			{"ЗвёздочкаИАлиасСсылки", http.StatusForbidden},
			{"ДублиАлиасовСсылки", http.StatusForbidden},
		} {
			target := "/api/v2/report/" + url.PathEscape(c.report) + "?limit=1"
			req := withUser(httptest.NewRequest(http.MethodGet, target, nil), user)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != c.wantStatus {
				t.Fatalf("%s: код %d, ожидался %d; тело %s", c.report, rec.Code, c.wantStatus, rec.Body.String())
			}
			if rec.Code != http.StatusOK {
				continue
			}
			var response struct {
				Data []map[string]any `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("%s: разбор ответа: %v", c.report, err)
			}
			if len(response.Data) != 1 {
				t.Fatalf("%s: строк %d, ожидалась одна", c.report, len(response.Data))
			}
			maskedKeys := []string{"секрет"}
			switch c.report {
			case "ЗвёздочкаСсылки":
				maskedKeys = []string{"секрет_id"}
			case "РазныеАлиасыСсылки":
				maskedKeys = []string{"контакт", "резерв"}
			}
			for _, key := range maskedKeys {
				if value, present := response.Data[0][key]; !present || value != nil {
					t.Fatalf("%s: hidden reference %q = %#v, present=%v; want null", c.report, key, value, present)
				}
			}

			for key, value := range response.Data[0] {
				if text, ok := value.(string); ok && (text == secretID.String() || text == "Тайна-1") {
					t.Fatalf("%s: колонка %q отдала скрытое значение %q", c.report, key, text)
				}
			}
		}
	})
}
