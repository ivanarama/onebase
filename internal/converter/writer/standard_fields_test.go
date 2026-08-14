package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
)

// Импортированный из 1С документ приезжал вообще без «Номера», «Даты», блока
// numerator: и признака posting — WriteDocuments звал convertFields напрямую,
// минуя стандартные реквизиты, которые справочник получал. Из-за этого жалоба
// «в документах нет НОМЕРА» (issue #658) относилась к конвертеру, а не к
// движку: автонумерацию документов платформа умеет и делает атомарно
// (план 117, Д6).

func readOut(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	return string(b)
}

func TestWriteDocuments_ДобавляетСтандартныеРеквизитыИНумератор(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	docs := []*parser1c.DocumentMeta{{
		Name:       "РеализацияТоваров",
		Attributes: []parser1c.Attribute{{Name: "Контрагент", Type: parser1c.FieldType{Primary: "xs:string"}}},
		Posting:    true,
		Number:     parser1c.Numbering{Auto: true, Length: 11, Type: "String", Periodicity: "Year"},
	}}
	if err := WriteDocuments(docs, out, notes); err != nil {
		t.Fatalf("WriteDocuments: %v", err)
	}
	got := readOut(t, filepath.Join(out, "documents", fileName("РеализацияТоваров")+".yaml"))
	for _, must := range []string{"name: Номер", "name: Дата", "posting: true", "numerator:", "length: 11", "period: year"} {
		if !strings.Contains(got, must) {
			t.Errorf("в YAML документа нет %q:\n%s", must, got)
		}
	}
	// Стандартные реквизиты идут первыми, как в 1С.
	if strings.Index(got, "name: Номер") > strings.Index(got, "name: Контрагент") {
		t.Errorf("«Номер» должен идти перед пользовательскими реквизитами:\n%s", got)
	}
}

// Документ без автонумерации получает «Номер» и «Дату», но без блока numerator:
// — иначе конвертер навязал бы нумерацию там, где её в 1С не было.
func TestWriteDocuments_БезАвтонумерацииНетБлока(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	docs := []*parser1c.DocumentMeta{{Name: "Заявка"}}
	if err := WriteDocuments(docs, out, notes); err != nil {
		t.Fatalf("WriteDocuments: %v", err)
	}
	got := readOut(t, filepath.Join(out, "documents", fileName("Заявка")+".yaml"))
	if strings.Contains(got, "numerator:") {
		t.Errorf("блок numerator: не должен появляться без автонумерации:\n%s", got)
	}
	if !strings.Contains(got, "name: Номер") || !strings.Contains(got, "name: Дата") {
		t.Errorf("стандартные реквизиты обязаны быть всегда:\n%s", got)
	}
}

// Уже объявленные пользователем «Номер»/«Дата» не дублируются.
func TestWriteDocuments_НеДублируетСуществующие(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	docs := []*parser1c.DocumentMeta{{
		Name:       "Заявка",
		Attributes: []parser1c.Attribute{{Name: "Номер", Type: parser1c.FieldType{Primary: "xs:string"}}},
	}}
	if err := WriteDocuments(docs, out, notes); err != nil {
		t.Fatalf("WriteDocuments: %v", err)
	}
	got := readOut(t, filepath.Join(out, "documents", fileName("Заявка")+".yaml"))
	if strings.Count(got, "name: Номер") != 1 {
		t.Errorf("«Номер» задвоился:\n%s", got)
	}
}

// Автонумерация кода справочника переносится блоком numerator: (#872).
//
// Раньше конвертер её отбрасывал и печатал предупреждение «платформа пока
// нумерует только документы». После планов 117B/117C/117E это перестало быть
// правдой: код справочника выдаётся той же единой точкой, что номер документа,
// и умеет контроль уникальности. Предупреждение пережило причину и посылало
// пользователя проставлять код руками из модуля.
func TestWriteCatalogs_АвтонумерацияКодаПереносится(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	cats := []*parser1c.CatalogMeta{{
		Name:         "Контрагенты",
		Hierarchical: true,
		Code:         parser1c.Numbering{Auto: true, Length: 9, Type: "String", CheckUnique: true},
	}}
	if err := WriteCatalogs(cats, out, notes); err != nil {
		t.Fatalf("WriteCatalogs: %v", err)
	}
	got := readOut(t, filepath.Join(out, "catalogs", fileName("Контрагенты")+".yaml"))
	if !strings.Contains(got, "hierarchical: true") {
		t.Errorf("иерархия не перенесена:\n%s", got)
	}
	for _, must := range []string{"numerator:", "length: 9", "unique: true"} {
		if !strings.Contains(got, must) {
			t.Errorf("в YAML нет %q — автонумерация кода потеряна:\n%s", must, got)
		}
	}
	// Периодичности у кода справочника в 1С нет: код живёт с элементом всю
	// жизнь. Писать period значило бы придумать сброс счётчика, которого не было.
	if strings.Contains(got, "period:") {
		t.Errorf("у кода справочника не должно быть period:\n%s", got)
	}
	if joined := strings.Join(notes.TypeWarnings, "\n"); joined != "" {
		t.Errorf("перенос полный, а в отчёте всё ещё предупреждения: %v", notes.TypeWarnings)
	}
}

// Справочник БЕЗ автонумерации блока numerator: не получает: иначе конвертер
// раздал бы коды там, где в 1С их выдавал человек.
func TestWriteCatalogs_БезАвтонумерацииБлокаНет(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	cats := []*parser1c.CatalogMeta{{
		Name: "Склады",
		Code: parser1c.Numbering{Auto: false, Length: 5, CheckUnique: true},
	}}
	if err := WriteCatalogs(cats, out, notes); err != nil {
		t.Fatalf("WriteCatalogs: %v", err)
	}
	got := readOut(t, filepath.Join(out, "catalogs", fileName("Склады")+".yaml"))
	if strings.Contains(got, "numerator:") {
		t.Errorf("блок numerator: без автонумерации в 1С:\n%s", got)
	}
	// Уникальность кода без автонумерации выразить нечем: numerator.unique
	// действует на автонумерацию. Это по-прежнему остаток — и он в отчёте.
	if !strings.Contains(strings.Join(notes.TypeWarnings, "\n"), "контроль уникальности кода без автонумерации") {
		t.Errorf("остаток не попал в отчёт: %v", notes.TypeWarnings)
	}
}
