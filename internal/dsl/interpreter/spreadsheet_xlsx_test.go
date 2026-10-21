package interpreter_test

import (
	"bytes"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

const tabDocMailSrc = `
  ТабДок = Новый ТабличныйДокумент;
  Шапка = ТабДок.Ячейка(1, 1);
  Шапка.Текст = "Заявки бригаде";
  Шапка.Жирный = Истина;
  Шапка.РазмерШрифта = 11;
  ТабДок.Объединить(1, 1, 1, 3);
  ТабДок.Ячейка(2, 1).Текст = "Пл8000641";
  Сумма = ТабДок.Ячейка(2, 2);
  Сумма.Значение = 1250.5;
  Сумма.Текст = "1 250,50";
  ТабДок.ШиринаКолонки(1, 145);
  Письмо = Новый ПисьмоEmail;
  Письмо.Кому = "brigada@iceberg.test";
  Письмо.Тема = "Заявки на завтра";
  Письмо.Текст = "Во вложении";
  Письмо.ДобавитьВложение("заявки.xlsx", ТабДок.Записать("заявки.xlsx", "xlsx"), , Истина);
  Письмо.Отправить();
`

// ТабличныйДокумент.Записать(…, "xlsx") → вложение письма: в тестовом профиле
// onebase test письмо ложится в Мок.Email с именем вложения.
func TestSpreadsheetXLSXAttachmentInMockEmail(t *testing.T) {
	profile := interpreter.NewTestProfile()
	profile.Reset()
	src := "Функция Тест()" + tabDocMailSrc + `  Возврат Строка(Мок.Email.Количество()) + "|" + Строка(Мок.Email[0].Вложений) + "|" + Мок.Email[0].ИменаВложений[0];
КонецФункции`
	vars := withSpreadsheet(profile.Vars())
	assert.Equal(t, "1|1|заявки.xlsx", runHTTPSrc(t, src, vars))
}

// Вложение — настоящая книга Excel с оформлением документа: шапка жирная,
// объединена, число — числом, ширина колонки сохранена.
func TestSpreadsheetXLSXAttachmentIsWorkbook(t *testing.T) {
	s := &profileSender{}
	runHTTPSrc(t, "Процедура Тест()"+tabDocMailSrc+"КонецПроцедуры", withSpreadsheet(interpreter.NewEmailFunctions(s, nil)))
	require.Equal(t, 1, s.sent)
	require.Len(t, s.msg.Files, 1)
	att := s.msg.Files[0]
	assert.Contains(t, att.MimeType, "spreadsheetml")
	f, err := excelize.OpenReader(bytes.NewReader(att.Data))
	require.NoError(t, err, "вложение не открывается как xlsx")
	defer f.Close() //nolint:errcheck
	v, _ := f.GetCellValue("Лист1", "A1")
	assert.Equal(t, "Заявки бригаде", v)
	id, _ := f.GetCellStyle("Лист1", "A1")
	st, err := f.GetStyle(id)
	require.NoError(t, err)
	require.NotNil(t, st.Font)
	assert.True(t, st.Font.Bold)
	assert.Equal(t, 11.0, st.Font.Size)
	merges, _ := f.GetMergeCells("Лист1")
	require.Len(t, merges, 1)
	assert.Equal(t, "C1", merges[0].GetEndAxis())
	raw, _ := f.GetCellValue("Лист1", "B2", excelize.Options{RawCellValue: true})
	assert.Equal(t, "1250.5", raw, "сумма — числом, не текстом")
	w, _ := f.GetColWidth("Лист1", "A")
	assert.InDelta(t, 20, w, 1)
}

// withSpreadsheet добавляет фабрику Новый ТабличныйДокумент — как её
// подмешивает исполнение модулей (ui/handlers_dsl.go).
func withSpreadsheet(vars map[string]any) map[string]any {
	for k, v := range interpreter.NewSpreadsheetFunctions() {
		vars[k] = v
	}
	return vars
}
