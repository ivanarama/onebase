package interpreter

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/equipment"
)

// dslDevice — DSL-обёртка над equipment.Device. Создаётся через
// ПодключитьОборудование("драйвер", Параметры) либо
// Новый ПодключаемоеОборудование("драйвер", Параметры).
type dslDevice struct {
	dev equipment.Device
}

func (d *dslDevice) CallMethod(name string, args []any) any {
	switch name {
	case "напечататьчек", "printreceipt":
		if err := d.asPrinter("НапечататьЧек").PrintReceipt(receiptFromArg(args, 0)); err != nil {
			panic(userError{Msg: "Оборудование.НапечататьЧек: " + err.Error()})
		}
		return nil
	case "открытьящик", "opendrawer":
		if err := d.asPrinter("ОткрытьЯщик").OpenDrawer(); err != nil {
			panic(userError{Msg: "Оборудование.ОткрытьЯщик: " + err.Error()})
		}
		return nil
	case "отрезать", "cutpaper":
		if err := d.asPrinter("Отрезать").CutPaper(); err != nil {
			panic(userError{Msg: "Оборудование.Отрезать: " + err.Error()})
		}
		return nil
	case "показать", "show":
		lines := make([]string, 0, len(args))
		for _, a := range args {
			lines = append(lines, equipStr(a))
		}
		if err := d.asDisplay("Показать").ShowLines(lines); err != nil {
			panic(userError{Msg: "Оборудование.Показать: " + err.Error()})
		}
		return nil
	case "очистить", "clear":
		if err := d.asDisplay("Очистить").Clear(); err != nil {
			panic(userError{Msg: "Оборудование.Очистить: " + err.Error()})
		}
		return nil
	case "получитьвес", "getweight":
		w, err := d.asScale("ПолучитьВес").Weight()
		if err != nil {
			panic(userError{Msg: "Оборудование.ПолучитьВес: " + err.Error()})
		}
		return w
	case "оплатитькартой", "pay":
		amount := 0.0
		if len(args) > 0 {
			amount, _ = toFloat(args[0])
		}
		res, err := d.asTerminal("ОплатитьКартой").Pay(amount)
		if err != nil {
			panic(userError{Msg: "Оборудование.ОплатитьКартой: " + err.Error()})
		}
		return &MapThis{M: map[string]any{
			"одобрено":  res.Approved,
			"approved":  res.Approved,
			"rrn":       res.RRN,
			"карта":     res.Card,
			"card":      res.Card,
			"сообщение": res.Message,
			"message":   res.Message,
		}}
	case "зарегистрироватьчек", "registerreceipt":
		res, err := d.asFiscal("ЗарегистрироватьЧек").RegisterReceipt(fiscalReceiptFromArg(args, 0))
		if err != nil {
			panic(userError{Msg: "Оборудование.ЗарегистрироватьЧек: " + err.Error()})
		}
		return &MapThis{M: map[string]any{
			"фн": res.FN, "fn": res.FN,
			"фд": res.FD, "fd": res.FD,
			"фп": res.FP, "fp": res.FP,
			"qr": res.QR, "штрихкод": res.QR,
		}}
	case "отключить", "disconnect":
		if err := d.dev.Disconnect(); err != nil {
			panic(userError{Msg: "Оборудование.Отключить: " + err.Error()})
		}
		return nil
	case "типоборудования", "kind":
		return d.dev.Kind()
	}
	panic(userError{Msg: "Оборудование: неизвестный метод " + name})
}

// asPrinter приводит устройство к ReceiptPrinter или останавливает выполнение
// понятной ошибкой, если драйвер не умеет печатать чеки.
func (d *dslDevice) asPrinter(method string) equipment.ReceiptPrinter {
	p, ok := d.dev.(equipment.ReceiptPrinter)
	if !ok {
		panic(userError{Msg: "Оборудование." + method + ": устройство «" + d.dev.Kind() + "» не печатает чеки"})
	}
	return p
}

// asDisplay приводит устройство к CustomerDisplay или останавливает выполнение,
// если драйвер не является дисплеем покупателя.
func (d *dslDevice) asDisplay(method string) equipment.CustomerDisplay {
	disp, ok := d.dev.(equipment.CustomerDisplay)
	if !ok {
		panic(userError{Msg: "Оборудование." + method + ": устройство «" + d.dev.Kind() + "» не является дисплеем"})
	}
	return disp
}

// asScale приводит устройство к Scale или останавливает выполнение, если
// драйвер не является весами.
func (d *dslDevice) asScale(method string) equipment.Scale {
	scale, ok := d.dev.(equipment.Scale)
	if !ok {
		panic(userError{Msg: "Оборудование." + method + ": устройство «" + d.dev.Kind() + "» не является весами"})
	}
	return scale
}

// asTerminal приводит устройство к PaymentTerminal или останавливает выполнение,
// если драйвер не является терминалом эквайринга.
func (d *dslDevice) asTerminal(method string) equipment.PaymentTerminal {
	term, ok := d.dev.(equipment.PaymentTerminal)
	if !ok {
		panic(userError{Msg: "Оборудование." + method + ": устройство «" + d.dev.Kind() + "» не является терминалом эквайринга"})
	}
	return term
}

// asFiscal приводит устройство к FiscalRegistrar или останавливает выполнение,
// если драйвер не является фискальным регистратором (ККТ).
func (d *dslDevice) asFiscal(method string) equipment.FiscalRegistrar {
	kkt, ok := d.dev.(equipment.FiscalRegistrar)
	if !ok {
		panic(userError{Msg: "Оборудование." + method + ": устройство «" + d.dev.Kind() + "» не является фискальным регистратором"})
	}
	return kkt
}

// receiptFromArg преобразует DSL-Структуру в equipment.Receipt.
// Ожидаемые поля: Заголовок (строка/Массив), Позиции (Массив структур
// Наименование/Количество/Цена/Сумма), Итого, Оплата, Подвал (строка/Массив).
func receiptFromArg(args []any, i int) equipment.Receipt {
	var r equipment.Receipt
	if i >= len(args) {
		return r
	}
	src, ok := args[i].(This)
	if !ok {
		panic(userError{Msg: "НапечататьЧек: ожидается Структура с данными чека"})
	}
	r.Header = equipStrings(src.Get("заголовок"))
	r.Footer = equipStrings(src.Get("подвал"))
	r.Payment = equipStr(src.Get("оплата"))
	r.Total, _ = toFloat(src.Get("итого"))
	for _, raw := range equipItems(src.Get("позиции")) {
		row, ok := raw.(This)
		if !ok {
			continue
		}
		it := equipment.ReceiptItem{Name: equipStr(row.Get("наименование"))}
		it.Qty, _ = toFloat(row.Get("количество"))
		it.Price, _ = toFloat(row.Get("цена"))
		it.Sum, _ = toFloat(row.Get("сумма"))
		if it.Sum == 0 {
			it.Sum = it.Qty * it.Price
		}
		r.Items = append(r.Items, it)
	}
	return r
}

// fiscalReceiptFromArg преобразует DSL-Структуру в equipment.FiscalReceipt.
// Поля: Тип, Налогообложение/СНО, Email, Телефон, КлючИдемпотентности (необяз.);
// Позиции (Наименование, Количество, Цена, Сумма, НДС/СтавкаНДС,
// ПризнакПредмета/Предмет, СпособРасчёта/ПризнакСпособаРасчёта);
// Оплаты (Тип/ВидОплаты, Сумма).
func fiscalReceiptFromArg(args []any, i int) equipment.FiscalReceipt {
	var r equipment.FiscalReceipt
	if i >= len(args) {
		return r
	}
	src, ok := args[i].(This)
	if !ok {
		panic(userError{Msg: "ЗарегистрироватьЧек: ожидается Структура с данными чека"})
	}
	r.Type = equipStr(src.Get("тип"))
	r.Taxation = pickStr(src, "налогообложение", "сно")
	r.Email = pickStr(src, "email", "почта")
	r.Phone = pickStr(src, "телефон", "phone")
	// Необязательный ключ идемпотентности: если задан, драйвер использует его как
	// uuid задания ФН (повтор того же чека с тем же ключом не пробьёт дубль).
	// Имена-алиасы намеренно явные: алиас "uuid" не вводим — в DSL UUID это
	// идентичность ссылки, и поле вроде GUID документа-основания молча стало бы
	// ключом, заставив ФН вернуть старый чек вместо нового (см. #24).
	r.IdempotencyKey = pickStr(src, "ключидемпотентности", "idempotencykey", "ключзадания")
	for _, raw := range equipItems(src.Get("позиции")) {
		row, ok := raw.(This)
		if !ok {
			continue
		}
		it := equipment.FiscalItem{Name: equipStr(row.Get("наименование"))}
		it.Qty, _ = toFloat(row.Get("количество"))
		it.Price, _ = toFloat(row.Get("цена"))
		it.Sum, _ = toFloat(row.Get("сумма"))
		if it.Sum == 0 {
			it.Sum = it.Qty * it.Price
		}
		it.VAT = pickStr(row, "ндс", "ставкандс")
		it.ItemType = pickStr(row, "признакпредмета", "предмет")
		it.PaymentType = pickStr(row, "способрасчёта", "признакспособарасчёта", "способрасчета", "признакспособарасчета")
		r.Items = append(r.Items, it)
	}
	for _, raw := range equipItems(src.Get("оплаты")) {
		row, ok := raw.(This)
		if !ok {
			continue
		}
		p := equipment.FiscalPayment{Type: pickStr(row, "тип", "видоплаты")}
		p.Sum, _ = toFloat(row.Get("сумма"))
		r.Payments = append(r.Payments, p)
	}
	return r
}

// pickStr возвращает первое непустое значение перечисленных полей Структуры.
func pickStr(s This, keys ...string) string {
	for _, k := range keys {
		if v := equipStr(s.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

// NewEquipmentFunctions возвращает функции и фабрики подключаемого оборудования
// для инъекции в extraVars интерпретатора (аналогично NewHTTPFunctions).
func NewEquipmentFunctions() map[string]any {
	connect := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		return openDevice(args), nil
	})
	factory := func(args []any) any { return openDevice(args) }
	return map[string]any{
		"ПодключитьОборудование": connect,
		"ConnectEquipment": connect,
		"__factory_ПодключаемоеОборудование": factory,
		"__factory_ConnectedEquipment":       factory,
	}
}

func openDevice(args []any) *dslDevice {
	driver := strArg(args, 0)
	var params map[string]string
	if len(args) >= 2 {
		params = equipParams(args[1])
	}
	dev, err := equipment.Open(driver, params)
	if err != nil {
		panic(userError{Msg: "ПодключитьОборудование: " + err.Error()})
	}
	return &dslDevice{dev: dev}
}

// ─── конвертация DSL-значений ──────────────────────────────────────────────

// equipStr приводит значение DSL к строке; nil → "".
func equipStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// equipStrings принимает строку или Массив строк (шапка/подвал чека).
func equipStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case *Array:
		out := make([]string, 0, len(t.items))
		for _, e := range t.items {
			out = append(out, equipStr(e))
		}
		return out
	default:
		return []string{equipStr(v)}
	}
}

// equipItems разворачивает DSL Массив позиций в срез значений.
func equipItems(v any) []any {
	if a, ok := v.(*Array); ok {
		return a.items
	}
	return nil
}

// equipParams собирает параметры подключения из DSL Структуры.
// Ключи уже в нижнем регистре (как их хранит Структура), что и ждёт драйвер.
func equipParams(v any) map[string]string {
	out := map[string]string{}
	if s, ok := v.(*Struct); ok {
		for _, k := range s.keys {
			out[k] = equipStr(s.vals[k])
		}
	}
	return out
}
