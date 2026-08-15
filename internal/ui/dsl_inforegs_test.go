package ui

// Программная запись регистров сведений (план 119A, issue #743). Читать регистр
// из конфигурации можно было всегда (СрезПоследних в языке запросов), а писать —
// нечем: движения умеют только регистры, подчинённые регистратору, независимый
// заполнялся руками через форму или обменом.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func stateInfoReg(periodic, recorder bool) *metadata.InfoRegister {
	return &metadata.InfoRegister{
		Name:       "СостояниеУзлов",
		Periodic:   periodic,
		Recorder:   recorder,
		Dimensions: []metadata.Field{{Name: "Узел", Type: metadata.FieldTypeString}},
		Resources: []metadata.Field{
			{Name: "Состояние", Type: metadata.FieldTypeString},
			{Name: "Попыток", Type: metadata.FieldTypeNumber},
		},
	}
}

// logInfoReg — регистр с двумя измерениями: отбор по одному из них должен
// задевать только его строки.
func logInfoReg() *metadata.InfoRegister {
	return &metadata.InfoRegister{
		Name:       "ЛогОбмена",
		Dimensions: []metadata.Field{{Name: "Узел", Type: metadata.FieldTypeString}, {Name: "Событие", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}},
	}
}

// runInfoRegDSL исполняет тело процедуры с доступным глобалом РегистрыСведений.
func runInfoRegDSL(t *testing.T, db *storage.DB, ir *metadata.InfoRegister, body string) ([]string, error) {
	t.Helper()
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}

	prog := mustParse(t, "Процедура Тест()\n"+body+"\nКонецПроцедуры")
	var proc *ast.ProcedureDecl
	for _, p := range prog.Procedures {
		proc = p
	}
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(context.Background(), nil, &msgs)
	defer interpreter.RollbackTxExecution(txState)
	err := s.interp.Run(proc, nil, vars)
	return msgs, err
}

// Запись, чтение и удаление независимого регистра — на обоих диалектах: SQL
// upsert и удаление по ключу у диалектов разные и разойтись могут молча.
func TestInfoRegDSL_WriteReadDeleteMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := stateInfoReg(false, false)
		if err := db.MigrateInfoRegisters(context.Background(), []*metadata.InfoRegister{ir}); err != nil {
			t.Fatalf("миграция: %v", err)
		}

		if _, err := runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Состояние = "Готов";
  Запись.Попыток = 3;
  Запись.Записать();`); err != nil {
			t.Fatalf("запись: %v", err)
		}

		msgs, err := runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Если Запись.Прочитать() Тогда
    Сообщить(Запись.Состояние + "/" + Строка(Запись.Попыток));
  Иначе
    Сообщить("не найдено");
  КонецЕсли;`)
		if err != nil {
			t.Fatalf("чтение: %v", err)
		}
		if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "Готов/3") {
			t.Fatalf("прочитано %v, ожидалось Готов/3", msgs)
		}

		if _, err := runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Удалить();`); err != nil {
			t.Fatalf("удаление: %v", err)
		}
		msgs, err = runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Сообщить(?(Запись.Прочитать(), "есть", "нет"));`)
		if err != nil {
			t.Fatalf("повторное чтение: %v", err)
		}
		if len(msgs) != 1 || msgs[0] != "нет" {
			t.Fatalf("после удаления: %v", msgs)
		}
	})
}

// Повторная запись по тому же ключу заменяет ресурсы, а не плодит строки.
func TestInfoRegDSL_UpsertByKeyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := stateInfoReg(false, false)
		if err := db.MigrateInfoRegisters(context.Background(), []*metadata.InfoRegister{ir}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, state := range []string{"Готов", "Ошибка"} {
			if _, err := runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Состояние = "`+state+`";
  Запись.Записать();`); err != nil {
				t.Fatalf("запись %s: %v", state, err)
			}
		}
		rows, err := db.InfoRegList(context.Background(), ir, storage.RegFilter{})
		if err != nil {
			t.Fatalf("список: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("строк %d, ожидалась одна (upsert по ключу)", len(rows))
		}
	})
}

// Периодический регистр требует период; непериодический — запрещает.
// Молча проигнорированный период положил бы запись не туда, где её ищет
// СрезПоследних.
func TestInfoRegDSL_PeriodRules(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "period.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)

	periodic := stateInfoReg(true, false)
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{periodic}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	_, err = runInfoRegDSL(t, db, periodic, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Состояние = "Готов";
  Запись.Записать();`)
	if err == nil || !strings.Contains(err.Error(), "период обязателен") {
		t.Errorf("периодический регистр без периода: %v", err)
	}

	_, err = runInfoRegDSL(t, db, periodic, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Период = Дата(2026, 8, 11, 12, 0, 0);
  Запись.Состояние = "Готов";
  Запись.Записать();`)
	if err != nil {
		t.Errorf("периодический регистр с периодом: %v", err)
	}
}

// Регистр, подчинённый регистратору, программной записью не трогаем: его
// строки принадлежат проведению, и перепроведение снесло бы их без
// предупреждения. Отказ приходит на СоздатьМенеджерЗаписи — там, где написана
// неверная строка.
func TestInfoRegDSL_RecorderRegisterRejected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "recorder.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := stateInfoReg(false, true)
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	_, err = runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();`)
	if err == nil {
		t.Fatal("подчинённый регистратору регистр принял программную запись")
	}
	for _, want := range []string{"подчинён регистратору", "ОбработкаПроведения"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
}

// Опечатка в имени измерения — ошибка, а не запись с пустым ключом: такая
// запись легла бы не туда, где её потом ищут.
func TestInfoRegDSL_UnknownFieldRejected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "typo.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := stateInfoReg(false, false)
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	_, err = runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узeл = "N1";`)
	if err == nil || !strings.Contains(err.Error(), "нет измерения или ресурса") {
		t.Errorf("опечатка в имени измерения прошла молча: %v", err)
	}
}

// Набор записей замещает содержимое ПО ОТБОРУ: строки вне отбора не трогаются.
// Матричный, потому что удаление по подмножеству измерений — новый SQL.
func TestInfoRegSet_ReplacesOnlyFilteredMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := logInfoReg()
		if err := db.MigrateInfoRegisters(context.Background(), []*metadata.InfoRegister{ir}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		// Две строки по N1 и одна по N2.
		if _, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С1 = Н.Добавить(); С1.Событие = "Старт";
  С2 = Н.Добавить(); С2.Событие = "Стоп";
  Н.Записать();
  М = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  М.Отбор.Узел = "N2";
  С3 = М.Добавить(); С3.Событие = "Чужое";
  М.Записать();`); err != nil {
			t.Fatalf("первичная запись: %v", err)
		}

		// Замещаем только N1 одной строкой.
		if _, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С = Н.Добавить(); С.Событие = "Перезапуск";
  Н.Записать();`); err != nil {
			t.Fatalf("замещение: %v", err)
		}

		rows, err := db.InfoRegList(context.Background(), ir, storage.RegFilter{})
		if err != nil {
			t.Fatalf("список: %v", err)
		}
		var n1, n2 []string
		for _, r := range rows {
			switch rowValueFold(r, "Узел") {
			case "N1":
				n1 = append(n1, fmtReportCell(rowValueFold(r, "Событие")))
			case "N2":
				n2 = append(n2, fmtReportCell(rowValueFold(r, "Событие")))
			}
		}
		if len(n1) != 1 || n1[0] != "Перезапуск" {
			t.Errorf("N1 = %v, ожидалась одна строка «Перезапуск» (замещение, а не добавление)", n1)
		}
		if len(n2) != 1 || n2[0] != "Чужое" {
			t.Errorf("строки вне отбора затронуты: N2 = %v", n2)
		}
	})
}

// Прочитать() поднимает содержимое по отбору — набор умеет не только писать.
func TestInfoRegSet_ReadByFilter(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-read.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := logInfoReg()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if _, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С = Н.Добавить(); С.Событие = "Старт";
  Н.Записать();`); err != nil {
		t.Fatalf("запись: %v", err)
	}
	msgs, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  Н.Прочитать();
  Сообщить(Строка(Н.Количество()));`)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "1" {
		t.Errorf("Прочитать/Количество = %v, ожидалось [1]", msgs)
	}
}

// Запись набора без отбора отклоняется: она снесла бы регистр целиком.
func TestInfoRegSet_EmptyFilterRejected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-empty.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := logInfoReg()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	_, err = runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  С = Н.Добавить(); С.Узел = "N1"; С.Событие = "Старт";
  Н.Записать();`)
	if err == nil || !strings.Contains(err.Error(), "не задан Отбор") {
		t.Errorf("набор без отбора прошёл: %v", err)
	}
}

// Отбор принимает только измерения: отбор по ресурсу — ошибка, а не тихо
// проигнорированное условие (иначе замещение снесло бы больше, чем думал автор).
func TestInfoRegSet_FilterByResourceRejected(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-res.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := logInfoReg()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	_, err = runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Комментарий = "что-то";`)
	if err == nil || !strings.Contains(err.Error(), "не измерение регистра") {
		t.Errorf("отбор по ресурсу прошёл: %v", err)
	}
}

// Цикл «Прочитать → Добавить → Записать» на ПЕРИОДИЧЕСКОМ регистре (#857).
//
// InfoRegList отдаёт период двумя ключами и оба — для интерфейса: `period`
// («02.01.2006», ячейка списка) и `period_key` (машинный, round-trip формы
// удаления). Набор клал их в строку как есть, а запись ждёт дату — поэтому
// цикл падал ВСЯКИЙ раз, когда в регистре уже была хоть одна строка, то есть
// обычное «прочитал-дополнил-записал» не работало в принципе.
//
// Матрично: период хранится по-разному (SQLite — TEXT, PostgreSQL —
// timestamptz), и разойтись эти пути могут молча.
func TestInfoRegSet_ЦиклЧтениеЗаписьНаПериодическомMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := stateInfoReg(true, false)
		if err := db.MigrateInfoRegisters(context.Background(), []*metadata.InfoRegister{ir}); err != nil {
			t.Fatalf("миграция: %v", err)
		}

		// В регистре уже есть строка — именно этот случай и ломался.
		if _, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.СостояниеУзлов.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С = Н.Добавить();
  С.Период = Дата(2026, 8, 11, 12, 0, 0) + 0.5;
  С.Состояние = "Готов";
  Н.Записать();`); err != nil {
			t.Fatalf("первичная запись: %v", err)
		}

		msgs, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.СостояниеУзлов.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  Н.Прочитать();
  С = Н.Добавить();
  С.Период = Дата(2026, 8, 12, 12, 0, 0) + 0.75;
  С.Состояние = "Ошибка";
  Н.Записать();
  Н2 = РегистрыСведений.СостояниеУзлов.СоздатьНаборЗаписей();
  Н2.Отбор.Узел = "N1";
  Н2.Прочитать();
  Сообщить(Строка(Н2.Количество()));`)
		if err != nil {
			t.Fatalf("цикл Прочитать→Добавить→Записать: %v", err)
		}
		if len(msgs) != 1 || msgs[0] != "2" {
			t.Errorf("после дополнения в наборе %v строк, ожидалось [2] — прочитанная строка потеряна", msgs)
		}

		// Считаем не только строки: прочитанная обязана сохранить СВОЙ период,
		// а не приехать с чужим или обнулённым. Набор из DSL не итерируется,
		// поэтому смотрим в хранилище.
		rows, err := db.InfoRegList(context.Background(), ir, storage.RegFilter{Dims: map[string]string{"Узел": "N1"}})
		if err != nil {
			t.Fatalf("InfoRegList: %v", err)
		}
		got := make([]time.Time, 0, len(rows))
		for _, row := range rows {
			key, _ := row["period_key"].(string)
			p, ok := storage.ParseRegPeriod(key)
			if !ok {
				t.Fatalf("period_key %q не разбирается; row=%v", key, row)
			}
			got = append(got, p)
		}
		want := []time.Time{
			time.Date(2026, 8, 11, 12, 0, 0, int(500*time.Millisecond), time.Local),
			time.Date(2026, 8, 12, 12, 0, 0, int(750*time.Millisecond), time.Local),
		}
		// SQLite канонически хранит секунды, PostgreSQL — микросекунды. Важно,
		// чтобы read→write не терял точность, которую хранит конкретный dialect.
		if db.Dialect().Name() == "sqlite" {
			for i := range want {
				want[i] = want[i].Truncate(time.Second)
			}
		}
		for _, expected := range want {
			found := false
			for _, actual := range got {
				if actual.Equal(expected) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("точного периода %s нет среди %v — read→write изменил ключ записи", expected.Format(time.RFC3339Nano), got)
			}
		}
	})
}

// В непериодическом регистре period_key не является транспортным полем: это
// допустимое имя пользовательского измерения или ресурса. Прочитать→Записать
// обязано сохранить его значение.
func TestInfoRegSet_НепериодическийPeriodKeyНеТеряется(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "period-key-field.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	ir := &metadata.InfoRegister{
		Name:       "TransportFields",
		Dimensions: []metadata.Field{{Name: "Node", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "period_key", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if _, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.TransportFields.СоздатьНаборЗаписей();
  Н.Отбор.Node = "N1";
  С = Н.Добавить(); С.period_key = "business-value";
  Н.Записать();
  Н.Прочитать();
  Н.Записать();`); err != nil {
		t.Fatalf("цикл Прочитать→Записать: %v", err)
	}

	rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{Dims: map[string]string{"Node": "N1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || asString(rowValueFold(rows[0], "period_key")) != "business-value" {
		t.Fatalf("пользовательский period_key потерян: %v", rows)
	}
}

// Менеджер записи: Прочитать() без Периода на периодическом регистре обязан
// отказать, а не читать произвольную строку. Проверка стояла у Записать() и
// Удалить(), а у Прочитать() её не было — и он молча отдавал чужие данные.
func TestInfoRegDSL_ПрочитатьБезПериодаОтклоняется(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "read-no-period.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := stateInfoReg(true, false)
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if _, err := runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Период = Дата(2026, 8, 11, 12, 0, 0);
  Запись.Состояние = "Готов";
  Запись.Записать();`); err != nil {
		t.Fatalf("запись: %v", err)
	}

	_, err = runInfoRegDSL(t, db, ir, `
  Запись = РегистрыСведений.СостояниеУзлов.СоздатьМенеджерЗаписи();
  Запись.Узел = "N1";
  Запись.Прочитать();`)
	if err == nil || !strings.Contains(err.Error(), "период обязателен") {
		t.Errorf("Прочитать без периода: %v — ожидался отказ «период обязателен»", err)
	}
}

// Прочитанные строки набора можно перебрать и поправить (#905).
//
// Раньше у набора были только Прочитать/Очистить/Добавить/Количество/Записать:
// прочитанные строки для прикладного кода были непрозрачны. Типичный цикл
// «прочитал срез, поправил один ресурс, записал» приходилось подменять на
// «прочитал запросом, собрал набор заново» — то есть набор годился только на
// полное замещение.
func TestInfoRegSet_ПереборИПравкаСтрок(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-iterate.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := logInfoReg()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if _, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С1 = Н.Добавить(); С1.Событие = "Старт"; С1.Комментарий = "было";
  С2 = Н.Добавить(); С2.Событие = "Стоп"; С2.Комментарий = "было";
  Н.Записать();`); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	// Прочитать → перебрать → поправить ресурс → записать.
	msgs, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  Н.Прочитать();
  Для Каждого Стр Из Н Цикл
      Сообщить(Стр.Событие);
      Стр.Комментарий = "стало";
  КонецЦикла;
  Н.Записать();`)
	if err != nil {
		t.Fatalf("перебор: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("перебор дал %d строк, ожидалось 2: %v", len(msgs), msgs)
	}

	rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("строк в регистре %d, ожидалось 2", len(rows))
	}
	for _, row := range rows {
		if got, _ := row["Комментарий"].(string); got != "стало" {
			t.Errorf("правка строки в цикле не доехала до записи: Комментарий = %q", got)
		}
	}
}

// Получить(Индекс) — то же, но точечно; выход за границы — внятный отказ, а не
// паника и не Неопределено.
func TestInfoRegSet_ПолучитьПоИндексу(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-get.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := logInfoReg()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}

	msgs, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С = Н.Добавить(); С.Событие = "Старт";
  Сообщить(Н.Получить(0).Событие);`)
	if err != nil {
		t.Fatalf("Получить(0): %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "Старт" {
		t.Fatalf("Получить(0) вернул %v, ожидалось [Старт]", msgs)
	}

	_, err = runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  Н.Получить(5);`)
	if err == nil || !strings.Contains(err.Error(), "вне набора") {
		t.Fatalf("выход за границы: %v", err)
	}
}
