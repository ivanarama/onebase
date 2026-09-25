package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// coerceParams не должен превращать UUID-значение ссылочного параметра в
// число: fmt.Sscanf разбирал префикс "123e4567" из UUID как float → в SQL
// получалось "uuid = numeric" (SQLSTATE 42883).
func TestCoerceParams_UUIDStaysString(t *testing.T) {
	uid := "123e4567-e89b-12d3-a456-426614174000"
	params := map[string]any{
		"Ref":  uid,
		"Num":  "42.5",
		"Int":  "100",
		"Date": "15.03.2026",
		"Text": "Привет",
	}
	coerceParams(params)

	if params["Ref"] != uid {
		t.Errorf("UUID-параметр стал %#v — должен остаться строкой", params["Ref"])
	}
	if params["Num"] != 42.5 {
		t.Errorf("Num = %#v, ожидалось 42.5", params["Num"])
	}
	if params["Int"] != float64(100) {
		t.Errorf("Int = %#v, ожидалось 100", params["Int"])
	}
	if _, ok := params["Date"].(time.Time); !ok {
		t.Errorf("Date = %#v, ожидался time.Time", params["Date"])
	}
	if params["Text"] != "Привет" {
		t.Errorf("Text = %#v, ожидалась исходная строка", params["Text"])
	}
}

// queryConsoleAnalyze определял тип параметра по колонке перед плейсхолдером.
// Плейсхолдер искался жёстко как «$N» (PostgreSQL); на SQLite (файловая база)
// плейсхолдер — «?», поэтому детект всегда проваливался в fallback по имени.
// Здесь имя параметра (&П) НЕ совпадает с сущностью — тип обязан определиться
// именно по колонке.
func TestQueryConsoleAnalyze_SQLiteRefByColumn(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	nom := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{nom}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{nom}, Registers: []*metadata.Register{reg}})

	s := &Server{store: db, reg: registry}

	body := `{"query":"ВЫБРАТЬ Количество ИЗ РегистрНакопления.ОстаткиТоваров ГДЕ Номенклатура = &П"}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.queryConsoleAnalyze(w, r)

	if w.Code != 200 {
		t.Fatalf("код %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ParamTypes map[string]string `json:"paramTypes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v — тело: %s", err, w.Body.String())
	}
	if got := resp.ParamTypes["П"]; got != "reference:Номенклатура" {
		t.Errorf("параметр &П: тип %q, ожидался reference:Номенклатура (детект по колонке на SQLite)", got)
	}
}

// Консоль разворачивала перед плейсхолдером только обёртку COALESCE(поле, ”),
// а вторая служебная обёртка компилятора оставалась: на SQLite number-колонка
// в сравнении окружается CAST(поле AS NUMERIC), перед плейсхолдером оставался
// хвост «numeric)», и параметр уходил в name-based fallback строкой (#1537).
//
// Тест идёт тем же путём, что и пользователь, — через обработчик маршрута
// POST /ui/dev/query-analyze. Имя параметра нейтральное: тип обязан
// определиться именно по колонке. Матрица диалектов: на PostgreSQL обёртки
// CAST нет вовсе, и там тип не должен измениться.
func TestQueryConsoleAnalyze_NumberByColumnCastUnwrap(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		reg := &metadata.Register{
			Name:       "ОстаткиТоваров",
			Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}

		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Registers: []*metadata.Register{reg}})
		s := &Server{store: db, reg: registry}

		analyze := func(t *testing.T, q string) map[string]string {
			t.Helper()
			body, err := json.Marshal(map[string]string{"query": q})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
			w := httptest.NewRecorder()
			s.queryConsoleAnalyze(w, r)
			if w.Code != 200 {
				t.Fatalf("код %d: %s", w.Code, w.Body.String())
			}
			var resp struct {
				ParamTypes map[string]string `json:"paramTypes"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("json: %v — тело: %s", err, w.Body.String())
			}
			return resp.ParamTypes
		}

		t.Run("прямая колонка", func(t *testing.T) {
			pt := analyze(t, `ВЫБРАТЬ Количество ИЗ РегистрНакопления.ОстаткиТоваров ГДЕ Количество = &П`)
			if got := pt["П"]; got != "number" {
				t.Errorf("параметр &П: тип %q, ожидался number (обёртка CAST не развернулась)", got)
			}
		})

		t.Run("колонка с алиасом источника", func(t *testing.T) {
			pt := analyze(t, `ВЫБРАТЬ Ост.Количество ИЗ РегистрНакопления.ОстаткиТоваров КАК Ост ГДЕ Ост.Количество = &П`)
			if got := pt["П"]; got != "number" {
				t.Errorf("параметр &П: тип %q, ожидался number", got)
			}
		})

		t.Run("несколько параметров", func(t *testing.T) {
			pt := analyze(t, `ВЫБРАТЬ Количество ИЗ РегистрНакопления.ОстаткиТоваров ГДЕ Количество = &П1 И Количество > &П2`)
			for _, name := range []string{"П1", "П2"} {
				if got := pt[name]; got != "number" {
					t.Errorf("параметр &%s: тип %q, ожидался number", name, got)
				}
			}
		})
	})
}
