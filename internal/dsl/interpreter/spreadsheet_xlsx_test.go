package interpreter_test

import (
	"bytes"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/printform"
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

// Макет в духе «Партнеры» из А_ОтправкаНаПочту: шапка и строка Calibri 11,
// шапка жирная, рамки ячеек, ширины колонок.
const partnersLayout = `name: Партнеры
columns:
  - width: 90px
  - width: 250px
  - width: 100px
areas:
  Шапка:
    rows:
      - cells:
          - {text: "Номер", bold: true, fontFamily: Calibri, fontSize: 11, border: all, align: center}
          - {text: "Адрес", bold: true, fontFamily: Calibri, fontSize: 11, border: all, align: center}
          - {text: "Сумма", bold: true, fontFamily: Calibri, fontSize: 11, border: all, align: center}
  Строка:
    rows:
      - cells:
          - {parameter: Номер, fontFamily: Calibri, fontSize: 11, border: all}
          - {parameter: Адрес, fontFamily: Calibri, fontSize: 11, border: all}
          - {parameter: Сумма, fontFamily: Calibri, fontSize: 11, border: all, align: right}
`

// ТабДок.Вывести(Макет.ПолучитьОбласть(…)) → Записать(…, "xlsx") → вложение:
// оформление и ширины колонок макета доходят до книги, как XLS97 в 1С.
func TestSpreadsheetXLSXFromLayoutKeepsTemplate(t *testing.T) {
	lt, err := printform.ParseLayoutBytes([]byte(partnersLayout))
	require.NoError(t, err)
	sender := &profileSender{}
	vars := withSpreadsheet(interpreter.NewEmailFunctions(sender, nil))
	interpreter.InjectMaket(vars, lt)
	runHTTPSrc(t, `Процедура Тест()
  ТабДок = Новый ТабличныйДокумент;
  ТабДок.Вывести(Макет.ПолучитьОбласть("Шапка"));
  Строки = Новый Массив;
  Строки.Добавить(Новый Структура("Номер, Адрес, Сумма", "Пл8000641", "Москва, ул. Ленина, 1", 1250.5));
  Строки.Добавить(Новый Структура("Номер, Адрес, Сумма", "Пл8000642", "Химки, ул. Мира, 7", 980));
  Для Каждого Стр Из Строки Цикл
    Обл = Макет.ПолучитьОбласть("Строка");
    Обл.Параметры.Номер = Стр.Номер;
    Обл.Параметры.Адрес = Стр.Адрес;
    Обл.Параметры.Сумма = Стр.Сумма;
    ТабДок.Вывести(Обл);
  КонецЦикла;
  Письмо = Новый ПисьмоEmail;
  Письмо.Кому = "brigada@iceberg.test";
  Письмо.Тема = "Заявки на завтра";
  Письмо.ДобавитьВложение("заявки.xlsx", ТабДок.Записать("заявки.xlsx", "xlsx"), , Истина);
  Письмо.Отправить();
КонецПроцедуры`, vars)
	require.Len(t, sender.msg.Files, 1)
	f, err := excelize.OpenReader(bytes.NewReader(sender.msg.Files[0].Data))
	require.NoError(t, err)
	defer f.Close() //nolint:errcheck
	const sh = "Лист1"

	head, _ := f.GetCellStyle(sh, "B1")
	hs, err := f.GetStyle(head)
	require.NoError(t, err)
	assert.Equal(t, "Calibri", hs.Font.Family)
	assert.Equal(t, 11.0, hs.Font.Size)
	assert.True(t, hs.Font.Bold, "шапка жирная")
	assert.Len(t, hs.Border, 4, "рамки шапки со всех сторон")

	v, _ := f.GetCellValue(sh, "B3")
	assert.Equal(t, "Химки, ул. Мира, 7", v)
	raw, _ := f.GetCellValue(sh, "C2", excelize.Options{RawCellValue: true})
	assert.Equal(t, "1250.5", raw, "сумма — числом")
	row, _ := f.GetCellStyle(sh, "C3")
	rs, err := f.GetStyle(row)
	require.NoError(t, err)
	assert.False(t, rs.Font.Bold)
	assert.Equal(t, "right", rs.Alignment.Horizontal)
	assert.Len(t, rs.Border, 4, "рамки строки")

	for col, want := range map[string]float64{"A": (90 - 5) / 7.0, "B": (250 - 5) / 7.0, "C": (100 - 5) / 7.0} {
		w, _ := f.GetColWidth(sh, col)
		assert.InDelta(t, want, w, 0.5, "ширина колонки %s из макета", col)
	}
}
