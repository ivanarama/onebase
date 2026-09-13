package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Чтение регистра сведений через REST (issue #1423). Проверяем тем же путём,
// которым ходит внешний клиент: HTTP-запрос через смонтированный маршрут, а не
// вызов listInfoRegV2 напрямую с подсунутым chi-контекстом.
//
// Матрица диалектов обязательна: страница и счётчик собирают SQL с LIMIT/OFFSET
// и предикатом доступа, а такие вещи на SQLite и PostgreSQL расходятся молча.

func matrixInfoRegAPI(t *testing.T, db *storage.DB, ir *metadata.InfoRegister) http.Handler {
	t.Helper()
	ctx := context.Background()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("MigrateInfoRegisters: %v", err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	h := &handler{reg: registry, store: db}
	r := chi.NewRouter()
	h.mountV2(r)
	return r
}

func matrixTariffs() *metadata.InfoRegister {
	return &metadata.InfoRegister{
		Name: "ТарифыСтатей",
		Dimensions: []metadata.Field{
			{Name: "Профиль", Type: metadata.FieldTypeString},
			{Name: "Статья", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{
			{Name: "Лимит", Type: metadata.FieldTypeNumber},
		},
	}
}

func seedTariffs(t *testing.T, db *storage.DB, ir *metadata.InfoRegister, rows [][3]any) {
	t.Helper()
	for _, row := range rows {
		err := db.InfoRegSet(context.Background(), ir,
			map[string]any{"Профиль": row[0], "Статья": row[1]},
			map[string]any{"Лимит": row[2]}, nil)
		if err != nil {
			t.Fatalf("InfoRegSet(%v): %v", row, err)
		}
	}
}

type inforegResponse struct {
	Data []map[string]any `json:"data"`
	Meta struct {
		Total      int `json:"total"`
		Page       int `json:"page"`
		Limit      int `json:"limit"`
		TotalPages int `json:"total_pages"`
	} `json:"meta"`
}

func getInfoReg(t *testing.T, srv http.Handler, target string, user *auth.User) (*httptest.ResponseRecorder, inforegResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if user != nil {
		r = withUser(r, user)
	} else {
		r = r.WithContext(auth.ContextWithOpenAccess(r.Context()))
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	var resp inforegResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json: %v; тело %s", err, w.Body.String())
		}
	}
	return w, resp
}

// Заявка ровно про это: матрица «профиль × статья» лежит в регистре сведений, а
// снаружи её не прочитать, поэтому приходилось заводить справочник-обёртку.
func TestInfoRegV2ReadsDimensionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		srv := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{
			{"Базовый", "Аренда", 100.0},
			{"Базовый", "Связь", 200.0},
			{"Расширенный", "Аренда", 300.0},
		})

		w, resp := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
		}
		if len(resp.Data) != 3 || resp.Meta.Total != 3 {
			t.Fatalf("ожидались 3 записи, получено %d (total=%d): %v", len(resp.Data), resp.Meta.Total, resp.Data)
		}
		if got := resp.Data[0]["Профиль"]; got != "Базовый" {
			t.Errorf("порядок строк не по ключу: первая %v", resp.Data[0])
		}
	})
}

func TestInfoRegV2FiltersByDimension(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		srv := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{
			{"Базовый", "Аренда", 100.0},
			{"Базовый", "Связь", 200.0},
			{"Расширенный", "Аренда", 300.0},
		})

		w, resp := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей?filter[Профиль]=Базовый", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
		}
		if len(resp.Data) != 2 || resp.Meta.Total != 2 {
			t.Fatalf("отбор по измерению не сработал: %d строк, total=%d", len(resp.Data), resp.Meta.Total)
		}
		for _, row := range resp.Data {
			if row["Профиль"] != "Базовый" {
				t.Errorf("в выдаче чужой профиль: %v", row)
			}
		}
	})
}

// Значение date-измерения из JSON должно быть пригодно для точного повторного
// отбора. На SQLite дата физически хранится не в RFC3339, поэтому сырая строка
// из URL не совпадает с той же датой без типизации на границе API.
func TestInfoRegV2FiltersByReturnedDateDimension(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := &metadata.InfoRegister{
			Name: "ProbeMatrix",
			Dimensions: []metadata.Field{
				{Name: "Key", Type: metadata.FieldTypeDate},
				{Name: "OtherKey", Type: metadata.FieldTypeString},
			},
		}
		srv := matrixInfoRegAPI(t, db, ir)
		when := time.Date(2026, 3, 15, 12, 34, 56, 0, time.UTC)
		if err := db.InfoRegSet(context.Background(), ir,
			map[string]any{"Key": when, "OtherKey": "row"}, nil, nil); err != nil {
			t.Fatalf("InfoRegSet: %v", err)
		}

		w, all := getInfoReg(t, srv, "/api/v2/inforeg/ProbeMatrix", nil)
		if w.Code != http.StatusOK || len(all.Data) != 1 {
			t.Fatalf("чтение исходной строки: код %d, данные %v", w.Code, all.Data)
		}
		returned := toStr(all.Data[0]["Key"])
		if _, err := time.Parse(time.RFC3339, returned); err != nil {
			t.Fatalf("date-измерение %q не разбирается как RFC3339: %v", returned, err)
		}

		target := "/api/v2/inforeg/ProbeMatrix?filter[Key]=" + url.QueryEscape(returned)
		w, filtered := getInfoReg(t, srv, target, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("повторный отбор: код %d, тело %s", w.Code, w.Body.String())
		}
		if len(filtered.Data) != 1 || filtered.Meta.Total != 1 {
			t.Fatalf("повторный отбор по %q вернул %d строк, total=%d",
				returned, len(filtered.Data), filtered.Meta.Total)
		}
		if got := toStr(filtered.Data[0]["Key"]); got != returned {
			t.Fatalf("date-измерение после отбора = %q, ожидалось %q", got, returned)
		}

		limited := apiUser("limited", auth.Permission{
			InfoRegs: map[string][]string{"ProbeMatrix": {"read"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				"ProbeMatrix": {"Key": {Read: "hide"}},
			}},
		})
		w, _ = getInfoReg(t, srv, target, limited)
		if w.Code != http.StatusForbidden {
			t.Fatalf("отбор по защищённому date-измерению должен давать 403, получен %d: %s", w.Code, w.Body.String())
		}
	})
}

// Булевы значения из query string должны доходить до SQL типизированными:
// SQLite хранит bool как INTEGER, и сравнение с сырыми строками "true"/"false"
// не находит ни одну из существующих записей.
func TestInfoRegV2FiltersByBoolDimension(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := &metadata.InfoRegister{
			Name: "ReviewBooleanLiterals",
			Dimensions: []metadata.Field{
				{Name: "Flag", Type: metadata.FieldTypeBool},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
		}
		srv := matrixInfoRegAPI(t, db, ir)
		for _, flag := range []bool{false, true} {
			if err := db.InfoRegSet(context.Background(), ir,
				map[string]any{"Flag": flag, "Key": strconv.FormatBool(flag)}, nil, nil); err != nil {
				t.Fatalf("InfoRegSet(%t): %v", flag, err)
			}
		}

		for _, flag := range []bool{false, true} {
			literal := strconv.FormatBool(flag)
			target := "/api/v2/inforeg/ReviewBooleanLiterals?filter[Flag]=" + literal
			w, filtered := getInfoReg(t, srv, target, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("отбор по %s: код %d, тело %s", literal, w.Code, w.Body.String())
			}
			if len(filtered.Data) != 1 || filtered.Meta.Total != 1 {
				t.Fatalf("отбор по %s вернул %d строк, total=%d",
					literal, len(filtered.Data), filtered.Meta.Total)
			}
			if got := toStr(filtered.Data[0]["Key"]); got != literal {
				t.Fatalf("отбор по %s вернул строку с Key=%q", literal, got)
			}
		}

		w, _ := getInfoReg(t, srv,
			"/api/v2/inforeg/ReviewBooleanLiterals?filter[Flag]=not-a-bool", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("неверный bool должен давать 400, получен %d: %s", w.Code, w.Body.String())
		}
	})
}

// Опечатка в имени измерения не должна выглядеть как «отбор дал весь регистр»:
// клиент принял бы полную выдачу за отфильтрованную.
func TestInfoRegV2RejectsUnknownFilter(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		srv := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{{"Базовый", "Аренда", 100.0}})

		w, _ := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей?filter[Профилъ]=Базовый", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ожидался 400 на неизвестное измерение, получен %d: %s", w.Code, w.Body.String())
		}
	})
}

// Пагинация обязана считаться в SQL и не терять строк между страницами:
// порядок задан первичным ключом.
func TestInfoRegV2PaginatesWithoutOverlap(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		srv := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{
			{"Базовый", "Аренда", 100.0},
			{"Базовый", "Связь", 200.0},
			{"Расширенный", "Аренда", 300.0},
		})

		seen := map[string]bool{}
		for page := 1; page <= 3; page++ {
			w, resp := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей?limit=1&page="+strconv.Itoa(page), nil)
			if w.Code != http.StatusOK {
				t.Fatalf("страница %d: код %d, тело %s", page, w.Code, w.Body.String())
			}
			if resp.Meta.Total != 3 || resp.Meta.TotalPages != 3 {
				t.Fatalf("страница %d: total=%d total_pages=%d, ожидалось 3/3", page, resp.Meta.Total, resp.Meta.TotalPages)
			}
			if len(resp.Data) != 1 {
				t.Fatalf("страница %d: %d строк вместо одной", page, len(resp.Data))
			}
			key := toStr(resp.Data[0]["Профиль"]) + "|" + toStr(resp.Data[0]["Статья"])
			if seen[key] {
				t.Fatalf("страница %d повторила строку %s", page, key)
			}
			seen[key] = true
			// X-Total-Count обещает клиенту то же число, что meta.total.
			if got := w.Header().Get("X-Total-Count"); got != "3" {
				t.Errorf("страница %d: X-Total-Count=%q", page, got)
			}
		}
		if len(seen) != 3 {
			t.Fatalf("страницы покрыли %d строк из 3", len(seen))
		}
	})
}

func TestInfoRegV2RequiresReadPermission(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		srv := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{{"Базовый", "Аренда", 100.0}})

		stranger := apiUser("stranger", auth.Permission{
			Catalogs: map[string][]string{"Контрагент": {"read"}},
		})
		w, _ := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей", stranger)
		if w.Code != http.StatusForbidden {
			t.Fatalf("без права inforeg:read ожидался 403, получен %d: %s", w.Code, w.Body.String())
		}

		reader := apiUser("reader", auth.Permission{
			InfoRegs: map[string][]string{"ТарифыСтатей": {"read"}},
		})
		w, resp := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей", reader)
		if w.Code != http.StatusOK {
			t.Fatalf("с правом inforeg:read ожидался 200, получен %d: %s", w.Code, w.Body.String())
		}
		if len(resp.Data) != 1 {
			t.Fatalf("выдача пуста при разрешённом чтении: %v", resp.Data)
		}
	})
}

func TestInfoRegV2UnknownRegisterIs404(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		srv := matrixInfoRegAPI(t, db, matrixTariffs())
		w, _ := getInfoReg(t, srv, "/api/v2/inforeg/НетТакого", nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("ожидался 404, получен %d: %s", w.Code, w.Body.String())
		}
	})
}

// Период уходит клиенту машинной датой, а не строкой «02.01.2006» из
// HTML-списка: та зависит от локальной зоны процесса и снаружи неоднозначна.
func TestInfoRegV2PeriodIsMachineReadable(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := &metadata.InfoRegister{
			Name:       "КурсыВалют",
			Periodic:   true,
			Dimensions: []metadata.Field{{Name: "Валюта", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Курс", Type: metadata.FieldTypeNumber}},
		}
		srv := matrixInfoRegAPI(t, db, ir)
		when := time.Date(2026, 3, 15, 0, 0, 0, 0, time.Local)
		if err := db.InfoRegSet(context.Background(), ir,
			map[string]any{"Валюта": "USD"}, map[string]any{"Курс": 90.5}, &when); err != nil {
			t.Fatalf("InfoRegSet: %v", err)
		}

		w, resp := getInfoReg(t, srv, "/api/v2/inforeg/КурсыВалют", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
		}
		if len(resp.Data) != 1 {
			t.Fatalf("ожидалась одна запись: %v", resp.Data)
		}
		raw := toStr(resp.Data[0]["period"])
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			t.Fatalf("период %q не разбирается как RFC3339: %v", raw, err)
		}
		if _, present := resp.Data[0]["period_key"]; present {
			t.Errorf("period_key — ключ формы удаления, в REST-выдаче ему не место: %v", resp.Data[0])
		}
	})
}

func TestInfoRegV2FiltersByPeriod(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := &metadata.InfoRegister{
			Name:       "КурсыВалют",
			Periodic:   true,
			Dimensions: []metadata.Field{{Name: "Валюта", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Курс", Type: metadata.FieldTypeNumber}},
		}
		srv := matrixInfoRegAPI(t, db, ir)
		ctx := context.Background()
		for _, day := range []int{10, 20} {
			when := time.Date(2026, 3, day, 0, 0, 0, 0, time.Local)
			if err := db.InfoRegSet(ctx, ir,
				map[string]any{"Валюта": "USD"}, map[string]any{"Курс": float64(day)}, &when); err != nil {
				t.Fatalf("InfoRegSet: %v", err)
			}
		}

		w, resp := getInfoReg(t, srv, "/api/v2/inforeg/КурсыВалют?filter[period.to]=2026-03-15", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
		}
		if len(resp.Data) != 1 || resp.Meta.Total != 1 {
			t.Fatalf("отбор по периоду не сработал: %d строк, total=%d", len(resp.Data), resp.Meta.Total)
		}

		w, _ = getInfoReg(t, srv, "/api/v2/inforeg/КурсыВалют?filter[period.to]=15.03.2026", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("формат из HTML-формы снаружи не принимается; ожидался 400, получен %d", w.Code)
		}
	})
}

// Отбор по регистру без периода — ошибка запроса, а не тихо игнорируемый ключ.
func TestInfoRegV2PeriodFilterOnNonPeriodicIsRejected(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		srv := matrixInfoRegAPI(t, db, matrixTariffs())
		w, _ := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей?filter[period.from]=2026-01-01", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ожидался 400, получен %d: %s", w.Code, w.Body.String())
		}
	})
}

// Замаскированный ресурс не должен уехать в JSON настоящим значением, а отбор
// по замаскированному измерению обязан отказать: иначе значение подбирается
// перебором по тому, меняется ли набор строк.
func TestInfoRegV2MasksProtectedFields(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		srv := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{
			{"Базовый", "Аренда", 100.0},
			{"Расширенный", "Аренда", 300.0},
		})
		limited := apiUser("limited", auth.Permission{
			InfoRegs: map[string][]string{"ТарифыСтатей": {"read"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				"ТарифыСтатей": {"Лимит": {Read: "hide"}},
			}},
		})

		w, resp := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей", limited)
		if w.Code != http.StatusOK {
			t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
		}
		if len(resp.Data) != 2 {
			t.Fatalf("ожидались 2 записи: %v", resp.Data)
		}
		for _, row := range resp.Data {
			if v, present := row["Лимит"]; present {
				t.Errorf("скрытый ресурс попал в выдачу: %v (значение %v)", row, v)
			}
		}
	})
}

func TestInfoRegV2RejectsFilterOnProtectedDimension(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := matrixTariffs()
		srv := matrixInfoRegAPI(t, db, ir)
		seedTariffs(t, db, ir, [][3]any{
			{"Базовый", "Аренда", 100.0},
			{"Расширенный", "Аренда", 300.0},
		})
		limited := apiUser("limited", auth.Permission{
			InfoRegs: map[string][]string{"ТарифыСтатей": {"read"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				"ТарифыСтатей": {"Профиль": {Read: "hide"}},
			}},
		})

		w, _ := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей?filter[Профиль]=Базовый", limited)
		if w.Code != http.StatusForbidden {
			t.Fatalf("отбор по защищённому измерению должен давать 403, получен %d: %s", w.Code, w.Body.String())
		}

		// Без такого отбора чтение остаётся разрешённым — закрыт канал вывода,
		// а не сам регистр.
		w, resp := getInfoReg(t, srv, "/api/v2/inforeg/ТарифыСтатей", limited)
		if w.Code != http.StatusOK || len(resp.Data) != 2 {
			t.Fatalf("чтение без отбора сломано: код %d, строк %d", w.Code, len(resp.Data))
		}
	})
}

func toStr(v any) string {
	s, _ := v.(string)
	return s
}
