package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Границы навигации по ссылке (#705). Сам разворот `Источник.Ссылка.Поле`
// проверяет nested_ref_matrix_test.go; здесь — два случая, где он НЕ должен
// срабатывать: чужой квалификатор и путь глубже одного перехода.

func guardEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "Владелец", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}},
		{Name: "Профиль", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Владелец", Type: "reference:Владелец", RefEntity: "Владелец"},
		}},
		{Name: "Сигнал", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Профиль", Type: "reference:Профиль", RefEntity: "Профиль"},
		}},
	}
}

func compileGuard(t *testing.T, src string) (query.Result, error) {
	t.Helper()
	return query.Compile(src, query.CompileOpts{
		Entities: guardEntities(),
		Dialect:  storage.SQLiteDialect{},
	})
}

// Квалификатор снимается только у настоящего источника. Незнакомое имя перед
// ссылочным полем — это не навигация, и подменять его псевдонимом JOIN'а
// нельзя: неверный запрос ответил бы данными вместо отказа, а тихо подменённый
// результат хуже ошибки — ошибку видно сразу, подмену на сверке через месяц.
func TestRefNavigation_ЧужойКвалификаторНеПодменяется(t *testing.T) {
	res, err := compileGuard(t,
		`ВЫБРАТЬ Ссылка ИЗ Справочник.Сигнал ГДЕ Чужой.Профиль.Наименование = "х"`)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if strings.Contains(res.SQL, "WHERE ref_профиль.наименование") {
		t.Errorf("незнакомый квалификатор молча превращён в навигацию:\n%s", res.SQL)
	}
	if !strings.Contains(res.SQL, "чужой.") {
		t.Errorf("незнакомый квалификатор исчез из SQL — запрос стал другим:\n%s", res.SQL)
	}
}

// Алиас источника — настоящий квалификатор, его снимать нужно. Проверка рядом
// с предыдущей, чтобы «не подменяем чужое» не выродилось в «не подменяем
// ничего».
func TestRefNavigation_АлиасИсточникаСнимается(t *testing.T) {
	res, err := compileGuard(t,
		`ВЫБРАТЬ С.Профиль.Наименование ИЗ Справочник.Сигнал КАК С`)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if !strings.Contains(res.SQL, "ref_профиль.наименование") {
		t.Errorf("алиас источника не развернулся в навигацию:\n%s", res.SQL)
	}
	if strings.Contains(res.SQL, "с.профиль_id.") {
		t.Errorf("квалификатор остался в пути:\n%s", res.SQL)
	}
}

// Путь глубже одного перехода отклоняется ошибкой уровня языка запросов.
// Авто-JOIN строится только для ссылочных полей самого источника, поэтому
// второй переход уходил в SQL дословно и падал `no such column` — именем
// колонки, которой в схеме нет и быть не может. По такому сообщению не понять,
// что именно не поддерживается.
func TestRefNavigation_ГлубокийПутьДаётОшибкуЯзыка(t *testing.T) {
	for _, src := range []string{
		`ВЫБРАТЬ Профиль.Владелец.Наименование ИЗ Справочник.Сигнал`,
		`ВЫБРАТЬ Сигнал.Профиль.Владелец.Наименование ИЗ Справочник.Сигнал`,
		`ВЫБРАТЬ С.Профиль.Владелец.Наименование ИЗ Справочник.Сигнал КАК С`,
		`ВЫБРАТЬ Ссылка ИЗ Справочник.Сигнал ГДЕ Профиль.Владелец.Наименование = "х"`,
	} {
		res, err := compileGuard(t, src)
		if err == nil {
			t.Errorf("глубокий путь скомпилировался без ошибки: %s\nSQL: %s", src, res.SQL)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "на один уровень") {
			t.Errorf("сообщение не объясняет предел: %v", err)
		}
		if !strings.Contains(msg, "Профиль.Владелец") {
			t.Errorf("сообщение не показывает сам путь: %v", err)
		}
		if !strings.Contains(msg, "СОЕДИНЕНИЕ") {
			t.Errorf("сообщение не подсказывает обход: %v", err)
		}
	}
}

// Один переход по-прежнему разрешён — проверка, что диагностика глубокого пути
// не задела рабочий случай.
func TestRefNavigation_ОдинПереходРазрешён(t *testing.T) {
	for _, src := range []string{
		`ВЫБРАТЬ Профиль.Наименование ИЗ Справочник.Сигнал`,
		`ВЫБРАТЬ Сигнал.Профиль.Наименование ИЗ Справочник.Сигнал`,
		`ВЫБРАТЬ Ссылка ИЗ Справочник.Сигнал УПОРЯДОЧИТЬ ПО Сигнал.Профиль.Наименование`,
	} {
		if _, err := compileGuard(t, src); err != nil {
			t.Errorf("рабочий запрос отклонён: %s\n%v", src, err)
		}
	}
}
