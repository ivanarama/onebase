package launcher

// Редактор нумератора в конфигураторе (план 117, Д5).
//
// До этого блок `numerator:` конфигуратор только СОХРАНЯЛ при round-trip:
// задать префикс, разрядность или включить уникальность можно было лишь правкой
// YAML руками, хотя всё остальное в объекте редактируется мышью.

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguratorTree_RendersNumeratorSettings(t *testing.T) {
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "X", ConfigSource: "file"},
		Lang: "ru",
		Tab:  "tree",
		Catalogs: []cfgEntity{{
			Name: "Контрагенты",
			Kind: "Справочник",
			Fields: []cfgField{
				{Name: "Наименование", Type: "string"},
				{Name: "Организация", Type: "string"},
			},
			Numerator: &cfgNumerator{
				Present: true, Prefix: "К-", Length: 6, Period: "none",
				Scope: "Организация", Unique: true, BasePrefix: true,
			},
		}},
		AllEntityNames: []string{"Контрагенты"},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{
		`name="numerator_present" value="1"`,
		`name="numerator_enabled" value="1" checked`,
		`name="numerator_prefix" value="К-"`,
		`name="numerator_length" value="6"`,
		`name="numerator_period"`,
		`option value="Организация" selected`,
		`name="numerator_unique" value="1" checked`,
		`name="numerator_base_prefix" value="1" checked`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в редакторе нумератора нет %q", want)
		}
	}
}

func numeratorCatalogFile(t *testing.T) (*handler, string) {
	t.Helper()
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "catalogs", "контрагенты.yaml")
	initial := `name: Контрагенты
fields:
  - name: Наименование
    type: string
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	return h, path
}

func numeratorForm() url.Values {
	form := url.Values{}
	form.Set("entity", "Контрагенты")
	form.Set("entity_kind", "Справочник")
	form.Set("field.0.name", "Наименование")
	form.Set("field.0.type", "string")
	form.Set("numerator_present", "1")
	return form
}

func TestConfiguratorSaveFields_NumeratorWritten(t *testing.T) {
	h, path := numeratorCatalogFile(t)

	form := numeratorForm()
	form.Set("numerator_enabled", "1")
	form.Set("numerator_prefix", "К-")
	form.Set("numerator_length", "6")
	form.Set("numerator_period", "none")
	form.Set("numerator_unique", "1")
	form.Set("numerator_base_prefix", "1")

	rec := saveFieldsForm(t, h, "test", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d: %s", rec.Code, rec.Body.String())
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"numerator:", "prefix: К-", "length: 6", "unique: true", "base_prefix: true"} {
		if !strings.Contains(got, want) {
			t.Errorf("в YAML нет %q:\n%s", want, got)
		}
	}
}

// Снятие галочки убирает блок целиком, а не оставляет пустой ключ без эффекта.
func TestConfiguratorSaveFields_NumeratorCleared(t *testing.T) {
	h, path := numeratorCatalogFile(t)

	on := numeratorForm()
	on.Set("numerator_enabled", "1")
	on.Set("numerator_prefix", "К-")
	on.Set("numerator_length", "6")
	if rec := saveFieldsForm(t, h, "test", on); rec.Code != http.StatusOK {
		t.Fatalf("включение: %d: %s", rec.Code, rec.Body.String())
	}

	off := numeratorForm() // numerator_enabled не задан — галочка снята
	if rec := saveFieldsForm(t, h, "test", off); rec.Code != http.StatusOK {
		t.Fatalf("выключение: %d: %s", rec.Code, rec.Body.String())
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "numerator:") {
		t.Errorf("блок numerator остался после снятия галочки:\n%s", out)
	}
}

// Сброс счётчика у справочника выдал бы уже занятый код — загрузчик такую
// конфигурацию отвергает, и собрать её мышью тоже нельзя.
func TestConfiguratorSaveFields_NumeratorCatalogPeriodRejected(t *testing.T) {
	h, path := numeratorCatalogFile(t)

	form := numeratorForm()
	form.Set("numerator_enabled", "1")
	form.Set("numerator_prefix", "К-")
	form.Set("numerator_length", "6")
	form.Set("numerator_period", "year")

	rec := saveFieldsForm(t, h, "test", form)
	body := rec.Body.String()
	if !strings.Contains(body, "занятый код") {
		t.Errorf("отказ не объясняет причину: %s", body)
	}
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "numerator:") {
		t.Errorf("отклонённая настройка всё же записана:\n%s", out)
	}
}

// Уникальность вместе со сбросом счётчика без маски даты: значение повторится в
// следующем периоде. Конфигуратор не даёт собрать конфигурацию, которую потом
// отвергнет `onebase check`.
func TestConfiguratorSaveFields_NumeratorUniqueNeedsMask(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	if err := os.MkdirAll(filepath.Join(cfgDir, "documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "documents", "реализация.yaml")
	if err := os.WriteFile(path, []byte("name: Реализация\nfields:\n  - name: Дата\n    type: date\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("entity", "Реализация")
	form.Set("entity_kind", "Документ")
	form.Set("field.0.name", "Дата")
	form.Set("field.0.type", "date")
	form.Set("numerator_present", "1")
	form.Set("numerator_enabled", "1")
	form.Set("numerator_prefix", "Р-")
	form.Set("numerator_length", "4")
	form.Set("numerator_period", "year")
	form.Set("numerator_unique", "1")

	rec := saveFieldsForm(t, h, "test", form)
	if !strings.Contains(rec.Body.String(), "маску даты") {
		t.Errorf("отказ не подсказывает маску: %s", rec.Body.String())
	}
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "unique: true") {
		t.Errorf("ловушка всё же записана:\n%s", out)
	}

	// С маской — записывается.
	form.Set("numerator_prefix", "Р-{YYYY}-")
	if rec := saveFieldsForm(t, h, "test", form); rec.Code != http.StatusOK {
		t.Fatalf("с маской: %d: %s", rec.Code, rec.Body.String())
	}
	out, _ = os.ReadFile(path)
	if !strings.Contains(string(out), "unique: true") {
		t.Errorf("с маской настройка не записана:\n%s", out)
	}

	// Для месячного сброса одного года недостаточно: номер повторился бы уже
	// в следующем месяце. Нужны и год, и месяц.
	form.Set("numerator_period", "month")
	form.Set("numerator_prefix", "Р-{YYYY}-")
	if rec := saveFieldsForm(t, h, "test", form); !strings.Contains(rec.Body.String(), "маску даты") {
		t.Fatalf("неполная месячная маска принята: %s", rec.Body.String())
	}
	form.Set("numerator_prefix", "Р-{YYYY}{MM}-")
	if rec := saveFieldsForm(t, h, "test", form); rec.Code != http.StatusOK {
		t.Fatalf("полная месячная маска отклонена: %d: %s", rec.Code, rec.Body.String())
	}
}

// Правка реквизитов НЕ должна стирать нумератор: форма объекта всегда присылает
// numerator_present, и блок пересобирается целиком.
func TestConfiguratorSaveFields_NumeratorSurvivesFieldEdit(t *testing.T) {
	h, path := numeratorCatalogFile(t)

	on := numeratorForm()
	on.Set("numerator_enabled", "1")
	on.Set("numerator_prefix", "К-")
	on.Set("numerator_length", "6")
	on.Set("numerator_unique", "1")
	if rec := saveFieldsForm(t, h, "test", on); rec.Code != http.StatusOK {
		t.Fatalf("включение: %d: %s", rec.Code, rec.Body.String())
	}

	// Добавляем реквизит — как это делает пользователь на вкладке «Реквизиты».
	edit := on
	edit.Set("field.1.name", "ИНН")
	edit.Set("field.1.type", "string")
	if rec := saveFieldsForm(t, h, "test", edit); rec.Code != http.StatusOK {
		t.Fatalf("правка реквизитов: %d: %s", rec.Code, rec.Body.String())
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "unique: true") || !strings.Contains(got, "prefix: К-") {
		t.Errorf("правка реквизитов потеряла настройки нумератора:\n%s", got)
	}
	if !strings.Contains(got, "ИНН") {
		t.Errorf("новый реквизит не сохранён:\n%s", got)
	}
}
