package ui

// Программная запись регистров сведений (план 119A, issue #743). Читать регистр
// из конфигурации можно было всегда (СрезПоследних в языке запросов), а писать —
// нечем: движения умеют только регистры, подчинённые регистратору, независимый
// заполнялся руками через форму или обменом.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
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

// Набор записей обязан идти тем же контрактом, что менеджер записи (#856).
//
// 119B звал storage.InfoRegSet напрямую, поэтому изменения набора не
// регистрировались в планах обмена: узлы о них не узнавали, репликация молча
// расходилась. Плюс строка набора была сырым MapThis без валидации — опечатка в
// имени измерения тихо писала мусор, тогда как менеджер записи для той же
// ошибки честно поднимает исключение.
func TestInfoRegSet_РегистрируетИзменениеВПланахОбмена(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-exchange.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ir := logInfoReg()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		t.Fatalf("схема обмена: %v", err)
	}

	plan := &metadata.ExchangePlan{
		Name:    "Обмен",
		Content: []string{"РегистрСведений." + ir.Name},
		Nodes:   []metadata.ExchangeNode{{Code: "center"}, {Code: "fil01"}},
	}
	plan.Normalize()
	if err := db.SaveExchangeThisNode(ctx, "Обмен", "fil01"); err != nil {
		t.Fatalf("узел: %v", err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	registry.LoadExchangePlans([]*metadata.ExchangePlan{plan})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}

	prog := mustParse(t, `Процедура Тест()
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С = Н.Добавить(); С.Событие = "Старт";
  Н.Записать();
КонецПроцедуры`)
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(ctx, nil, &msgs)
	defer interpreter.RollbackTxExecution(txState)
	if err := s.interp.Run(prog.Procedures[0], nil, vars); err != nil {
		t.Fatalf("запись набора: %v", err)
	}

	var changes int
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM _exchange_changes WHERE plan = ? AND object_type LIKE ?`,
		"Обмен", "%"+ir.Name+"%").Scan(&changes); err != nil {
		t.Fatalf("чтение регистрации: %v", err)
	}
	if changes == 0 {
		t.Fatal("запись набора не зарегистрирована в плане обмена — узлы о ней не узнают")
	}
}

// Опечатка в имени поля строки набора — ошибка, а не молча записанный мусор.
func TestInfoRegSet_ОпечаткаВИмениПоляОтклоняется(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-typo.db"))
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
  Н.Отбор.Узел = "N1";
  С = Н.Добавить(); С.Собитие = "Старт";
  Н.Записать();`)
	// Имя приходит уже в нижнем регистре: MapThis.Set нормализует ключи, и
	// исходное написание к моменту проверки не сохраняется.
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "собитие") {
		t.Fatalf("опечатка в имени поля принята: %v", err)
	}
}

// Строка, противоречащая отбору, «сбегает» из него и затирает чужой срез:
// удаление-то идёт по отбору. Отклоняем.
func TestInfoRegSet_СтрокаВнеОтбораОтклоняется(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rs-escape.db"))
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
  Н.Отбор.Узел = "N1";
  С = Н.Добавить(); С.Узел = "N2"; С.Событие = "Старт";
  Н.Записать();`)
	if err == nil || !strings.Contains(err.Error(), "не совпадает с отбором") {
		t.Fatalf("строка вне отбора принята: %v", err)
	}
}

// Набор может вызываться внутри явной DSL-транзакции, а исключение от
// Записать() — ловиться прикладным кодом. В этом случае операция всё равно
// обязана иметь собственный savepoint: внешний commit не должен фиксировать
// DELETE и только строки до первой ошибки.
func TestInfoRegSet_АтомаренВоВнешнейТранзакции(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := stateInfoReg(true, false)
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		oldPeriod := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
		if err := db.InfoRegSet(ctx, ir,
			map[string]any{"Узел": "N1"},
			map[string]any{"Состояние": "Старое", "Попыток": float64(0)},
			&oldPeriod); err != nil {
			t.Fatal(err)
		}

		_, err := runInfoRegDSL(t, db, ir, `
  НачатьТранзакцию();
  Попытка
    Н = РегистрыСведений.СостояниеУзлов.СоздатьНаборЗаписей();
    Н.Отбор.Узел = "N1";
    С1 = Н.Добавить();
    С1.Период = Дата(2026, 8, 2, 0, 0, 0);
    С1.Состояние = "Новое";
    С1.Попыток = 1;
    С2 = Н.Добавить();
    С2.Состояние = "Без периода";
    С2.Попыток = 2;
    Н.Записать();
  Исключение
    ОшибкаЗаписи = ОписаниеОшибки();
  КонецПопытки;
  ЗафиксироватьТранзакцию();`)
		if err != nil {
			t.Fatalf("DSL execution: %v", err)
		}

		rows, err := db.InfoRegList(ctx, ir,
			storage.RegFilter{Dims: map[string]string{"Узел": "N1"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || asString(rowValueFold(rows[0], "Состояние")) != "Старое" {
			t.Fatalf("ошибка вложенной записи оставила частичное замещение: %#v", rows)
		}
	})
}

// Записать() пустой набор — это физическое удаление прежнего среза. Отдельное
// право write не должно неявно выдавать delete.
func TestInfoRegSet_УдалениеТребуетDeletePermission(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name:       "Защищённый",
			Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir,
			map[string]any{"Ключ": "A"}, map[string]any{"Значение": "secret"}, nil); err != nil {
			t.Fatal(err)
		}

		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: registry}
		user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"write"}},
		}}}}
		rs := newInfoRegRecordSet(s,
			interpreter.NewTxState(auth.ContextWithUser(ctx, user)), ir)
		rs.filter.Set("Ключ", "A")

		var denied any
		func() {
			defer func() { denied = recover() }()
			rs.write()
		}()
		if denied == nil {
			t.Fatal("write-only роль удалила строку без права delete")
		}
		rows, err := db.InfoRegList(ctx, ir,
			storage.RegFilter{Dims: map[string]string{"Ключ": "A"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("отказ delete не откатил удаление: %#v", rows)
		}
	})
}

// Менеджер записи отклоняет Период у непериодического регистра; набор обязан
// соблюдать тот же контракт, а не молча выбрасывать поле.
func TestInfoRegSet_НепериодическийПериодОтклоняется(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := logInfoReg()
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		_, err := runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С = Н.Добавить();
  С.Событие = "Start";
  С.Период = Дата(2026, 8, 15);
  Н.Записать();`)
		if err == nil || !strings.Contains(err.Error(), "период указывать нельзя") {
			t.Fatalf("непериодический набор принял Период: %v", err)
		}
		rows, err := db.InfoRegList(ctx, ir,
			storage.RegFilter{Dims: map[string]string{"Узел": "N1"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Fatalf("ошибочная строка записана: %#v", rows)
		}
	})
}

func TestInfoRegRowPeriod_ОдноимённоеПолеНепериодическогоРегистраРазрешено(t *testing.T) {
	for _, name := range []string{"Период", "period"} {
		t.Run(name, func(t *testing.T) {
			ir := &metadata.InfoRegister{
				Name:       "Непериодический",
				Dimensions: []metadata.Field{{Name: name, Type: metadata.FieldTypeString}},
			}
			period, err := infoRegRowPeriod(ir, map[string]any{name: "обычное измерение"})
			if err != nil {
				t.Fatalf("поле метаданных %q принято за служебный период: %v", name, err)
			}
			if period != nil {
				t.Fatalf("период непериодического регистра = %v, ожидался nil", period)
			}
			record := newInfoRegRecord(nil, nil, ir)
			record.Set(name, "обычное измерение")
			if got := record.Get(name); got != "обычное измерение" {
				t.Fatalf("менеджер записи принял реальное поле %q за системный период: %T(%v)", name, got, got)
			}
		})
	}
}

// Tombstone строится по строкам, фактически удалённым тем же DELETE, а не по
// отдельному снимку до транзакции.
func TestInfoRegSet_РегистрируетTombstoneУдалённыхСтрок(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := logInfoReg()
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureExchangeSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir,
			map[string]any{"Узел": "N1", "Событие": "E1"},
			map[string]any{"Комментарий": "old"}, nil); err != nil {
			t.Fatal(err)
		}

		plan := &metadata.ExchangePlan{
			Name:    "Обмен",
			Content: []string{"РегистрСведений." + ir.Name},
			Nodes:   []metadata.ExchangeNode{{Code: "center"}, {Code: "fil01"}},
		}
		plan.Normalize()
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "fil01"); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		registry.LoadExchangePlans([]*metadata.ExchangePlan{plan})
		interp := interpreter.New()
		interp.LookupProc = registry.GetModuleProc
		s := &Server{store: db, reg: registry, interp: interp,
			lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}

		prog := mustParse(t, `Процедура Тест()
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  Н.Записать();
КонецПроцедуры`)
		var msgs []string
		vars, txState := s.buildDSLVarsWithMessagesTx(ctx, nil, &msgs)
		defer interpreter.RollbackTxExecution(txState)
		if err := s.interp.Run(prog.Procedures[0], nil, vars); err != nil {
			t.Fatalf("удаление набором: %v", err)
		}

		changes, err := db.PendingExchangeChanges(ctx, plan.Name, "center")
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 || !changes[0].Deletion ||
			changes[0].Kind != storage.ExchangeKindInfoReg ||
			changes[0].ObjectType != ir.Name ||
			!strings.Contains(changes[0].ObjectID, `"Событие":"E1"`) {
			t.Fatalf("неверный tombstone: %#v", changes)
		}
	})
}

// DSL-отбор хранит реальные значения, а не fmt.Sprintf-представления. Для
// ссылки это принципиально: String() возвращает display-name, тогда как ключом
// БД и обмена является UUID. Дата и число тоже должны дойти до драйвера в своём
// типе на обоих диалектах.
func TestInfoRegSet_ТипизированныйОтборМатрица(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name: "TypedFilter",
			Dimensions: []metadata.Field{
				{Name: "Owner", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары"},
				{Name: "Moment", Type: metadata.FieldTypeDate},
				{Name: "Seq", Type: metadata.FieldTypeNumber},
			},
			Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: registry}
		ref1 := &interpreter.Ref{
			UUID: "11111111-1111-1111-1111-111111111111", Name: "Одинаковое имя",
		}
		ref2 := &interpreter.Ref{
			UUID: "22222222-2222-2222-2222-222222222222", Name: "Одинаковое имя",
		}
		moment := time.Date(2026, 8, 15, 10, 11, 12, 123456000, time.UTC)

		rs := newInfoRegRecordSet(s, interpreter.NewTxState(ctx), ir)
		rs.filter.Set("Owner", ref1)
		rs.filter.Set("Moment", moment)
		rs.filter.Set("Seq", float64(7))
		row := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
		row.Set("Value", "ok")
		rs.write()

		filter := storage.RegFilter{DimValues: map[string]any{
			"Owner": ref1, "Moment": moment, "Seq": float64(7),
		}}
		rows, err := db.InfoRegList(ctx, ir, filter)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || asString(rows[0]["Owner"]) != ref1.UUID ||
			asString(rows[0]["Value"]) != "ok" {
			t.Fatalf("типизированный отбор не нашёл запись: %#v", rows)
		}
		wrong, err := db.InfoRegList(ctx, ir, storage.RegFilter{DimValues: map[string]any{
			"Owner": ref2, "Moment": moment, "Seq": float64(7),
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(wrong) != 0 {
			t.Fatalf("ссылка сравнилась по display-name вместо UUID: %#v", wrong)
		}

		// Две ссылки с одинаковым представлением, но разными UUID не равны и в
		// проверке «строка не должна сбегать из отбора».
		bad := newInfoRegRecordSet(s, interpreter.NewTxState(ctx), ir)
		bad.filter.Set("Owner", ref1)
		bad.filter.Set("Moment", moment)
		bad.filter.Set("Seq", float64(7))
		badRow := bad.CallMethod("Добавить", nil).(*interpreter.MapThis)
		badRow.Set("Owner", ref2)
		badRow.Set("Value", "escape")
		var rejected any
		func() {
			defer func() { rejected = recover() }()
			bad.write()
		}()
		if rejected == nil {
			t.Fatal("разные UUID с одинаковым именем признаны равными")
		}
	})
}

func TestInfoRegSet_ПустоеЗначениеОтбораОтклоняется(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "empty-filter.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	ir := logInfoReg()
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	_, err = runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "";
  Н.Прочитать();`)
	if err == nil || !strings.Contains(err.Error(), "пустое значение") {
		t.Fatalf("пустой отбор принят как несужающий: %v", err)
	}
}

func TestInfoRegSet_ExchangeKeyСсылкиИспользуетUUID(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "ref-key.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	ir := &metadata.InfoRegister{
		Name: "RefExchange",
		Dimensions: []metadata.Field{{
			Name: "Owner", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары",
		}},
		Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	plan := &metadata.ExchangePlan{
		Name:    "Обмен",
		Content: []string{"РегистрСведений." + ir.Name},
		Nodes:   []metadata.ExchangeNode{{Code: "center"}, {Code: "fil01"}},
	}
	plan.Normalize()
	if err := db.SaveExchangeThisNode(ctx, plan.Name, "fil01"); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	registry.LoadExchangePlans([]*metadata.ExchangePlan{plan})
	s := &Server{store: db, reg: registry}
	ref := &interpreter.Ref{UUID: uuid.NewString(), Name: "Витринное имя"}
	rs := newInfoRegRecordSet(s, interpreter.NewTxState(ctx), ir)
	rs.filter.Set("Owner", ref)
	row := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
	row.Set("Value", "ok")
	rs.write()

	changes, err := db.PendingExchangeChanges(ctx, plan.Name, "center")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !strings.Contains(changes[0].ObjectID, ref.UUID) ||
		strings.Contains(changes[0].ObjectID, ref.Name) {
		t.Fatalf("exchange key построен не по UUID ссылки: %#v", changes)
	}
}

func captureInfoRegRecordSetPanic(fn func()) (caught any) {
	defer func() { caught = recover() }()
	fn()
	return nil
}

func TestInfoRegRecordSet_ReadAppliesObjectAndRowAccessMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name: "ReadPolicy",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{
				{Name: "Owner", Type: metadata.FieldTypeString},
				{Name: "Value", Type: metadata.FieldTypeString},
			},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct{ key, owner string }{{"allowed", "mine"}, {"hidden", "other"}} {
			if err := db.InfoRegSet(ctx, ir,
				map[string]any{"Slice": "S", "Key": row.key},
				map[string]any{"Owner": row.owner, "Value": row.key}, nil); err != nil {
				t.Fatal(err)
			}
		}

		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: registry}
		role := &auth.Role{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"read"}},
			RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
				ir.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}}},
			}},
		}}
		userCtx := auth.ContextWithUser(ctx, &auth.User{Roles: []*auth.Role{role}})
		rs := newInfoRegRecordSet(s, interpreter.NewTxState(userCtx), ir)
		rs.filter.Set("Slice", "S")
		if got := rs.CallMethod("Прочитать", nil); got != float64(1) {
			t.Fatalf("RLS read count = %v, rows=%#v", got, rs.rows)
		}
		if len(rs.rows) != 1 || asString(rs.rows[0]["Owner"]) != "mine" {
			t.Fatalf("record-set read did not apply SQL row filter: %#v", rs.rows)
		}

		deniedUser := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{},
		}}}}
		denied := newInfoRegRecordSet(s,
			interpreter.NewTxState(auth.ContextWithUser(ctx, deniedUser)), ir)
		denied.filter.Set("Slice", "S")
		if caught := captureInfoRegRecordSetPanic(func() { denied.CallMethod("Прочитать", nil) }); caught == nil {
			t.Fatal("record-set read bypassed object read permission")
		}

		trusted := newInfoRegRecordSet(s, interpreter.NewTxState(
			trustedDSLContext(auth.ContextWithUser(ctx, deniedUser))), ir)
		trusted.filter.Set("Slice", "S")
		if got := trusted.CallMethod("Прочитать", nil); got != float64(2) {
			t.Fatalf("trusted DSL context must preserve unrestricted internal semantics: %v, rows=%#v", got, trusted.rows)
		}
	})
}

func TestInfoRegRecordSet_WritePreflightsObjectPermissionsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name:       "ObjectPreflight",
			Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "exists"}, map[string]any{"Value": "keep"}, nil); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: registry}

		for _, tc := range []struct {
			name string
			ops  []string
		}{{"missing delete", []string{"write"}}, {"missing write", []string{"delete"}}} {
			t.Run(tc.name, func(t *testing.T) {
				user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
					InfoRegs: map[string][]string{ir.Name: tc.ops},
				}}}}
				var outcomes []string
				for _, target := range []struct {
					key    string
					addRow bool
				}{{"exists", false}, {"absent", true}} {
					rs := newInfoRegRecordSet(s, interpreter.NewTxState(auth.ContextWithUser(ctx, user)), ir)
					rs.filter.Set("Key", target.key)
					if target.addRow {
						row := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
						row.Set("Value", "new")
					}
					caught := captureInfoRegRecordSetPanic(rs.write)
					if caught == nil {
						t.Fatalf("%s succeeded for key %q", tc.name, target.key)
					}
					outcomes = append(outcomes, fmt.Sprint(caught))
				}
				if outcomes[0] != outcomes[1] {
					t.Fatalf("object denial reveals existing vs absent/row count: %q != %q", outcomes[0], outcomes[1])
				}
			})
		}
		rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || asString(rows[0]["Key"]) != "exists" || asString(rows[0]["Value"]) != "keep" {
			t.Fatalf("object preflight mutated data: %#v", rows)
		}
	})
}

func TestInfoRegRecordSet_RowPolicyIsNoExistenceOracleMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name: "RowPolicyNoOracle",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{
				{Name: "Owner", Type: metadata.FieldTypeString},
				{Name: "Value", Type: metadata.FieldTypeString},
			},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir,
			map[string]any{"Slice": "exists", "Key": "K"},
			map[string]any{"Owner": "other", "Value": "hidden"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.EnsureExchangeSchema(ctx); err != nil {
			t.Fatal(err)
		}
		plan := &metadata.ExchangePlan{
			Name:    "NoOracleExchange",
			Content: []string{"РегистрСведений." + ir.Name},
			Nodes:   []metadata.ExchangeNode{{Code: "center"}, {Code: "branch"}},
		}
		plan.Normalize()
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "branch"); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		registry.LoadExchangePlans([]*metadata.ExchangePlan{plan})
		s := &Server{store: db, reg: registry}
		policy := auth.RowPolicy{Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}}
		user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"write", "delete"}},
			RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
				ir.Name: {"write": policy, "delete": policy},
			}},
		}}}}
		userCtx := auth.ContextWithUser(ctx, user)

		for _, slice := range []string{"exists", "absent"} {
			rs := newInfoRegRecordSet(s, interpreter.NewTxState(userCtx), ir)
			rs.filter.Set("Slice", slice)
			if caught := captureInfoRegRecordSetPanic(rs.write); caught != nil {
				t.Fatalf("hidden and absent slices must both be successful no-ops (%s): %v", slice, caught)
			}
		}

		// Even an allowed proposed row with the exact hidden PK must not turn
		// ON CONFLICT into an overwrite or an existence signal.
		rs := newInfoRegRecordSet(s, interpreter.NewTxState(userCtx), ir)
		rs.filter.Set("Slice", "exists")
		row := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
		row.Set("Key", "K")
		row.Set("Owner", "mine")
		row.Set("Value", "overwrite")
		if caught := captureInfoRegRecordSetPanic(rs.write); caught != nil {
			t.Fatalf("hidden same-key conflict disclosed itself: %v", caught)
		}

		rows, err := db.InfoRegList(ctx, ir,
			storage.RegFilter{DimValues: map[string]any{"Slice": "exists", "Key": "K"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || asString(rows[0]["Owner"]) != "other" || asString(rows[0]["Value"]) != "hidden" {
			t.Fatalf("hidden row was deleted or overwritten: %#v", rows)
		}
		changes, err := db.PendingExchangeChanges(ctx, plan.Name, "center")
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 0 {
			t.Fatalf("no-op leaked existence through exchange outbox: %#v", changes)
		}
	})
}

func TestInfoRegRecordSet_ProposedNullUsesSQLThreeValuedPolicyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cases := []struct {
			name   string
			reg    string
			policy auth.RowPolicy
		}{
			{
				name: "ne",
				reg:  "NullPolicyNe",
				policy: auth.RowPolicy{
					Field: "Owner", Op: "ne", Value: auth.RowValue{Literal: "other"},
				},
			},
			{
				name: "not_in",
				reg:  "NullPolicyNotIn",
				policy: auth.RowPolicy{
					Field: "Owner", Op: "not_in", Value: auth.RowValue{List: []any{"other"}},
				},
			},
			{
				name: "not_eq",
				reg:  "NullPolicyNotEq",
				policy: auth.RowPolicy{Not: &auth.RowPolicy{
					Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "other"},
				}},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ir := &metadata.InfoRegister{
					Name: tc.reg,
					Dimensions: []metadata.Field{
						{Name: "Slice", Type: metadata.FieldTypeString},
						{Name: "Key", Type: metadata.FieldTypeString},
					},
					Resources: []metadata.Field{
						{Name: "Owner", Type: metadata.FieldTypeString},
						{Name: "Value", Type: metadata.FieldTypeString},
					},
				}
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				registry := runtime.NewRegistry()
				registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
				s := &Server{store: db, reg: registry}
				user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
					InfoRegs: map[string][]string{ir.Name: {"write", "delete"}},
					RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
						ir.Name: {"write": tc.policy},
					}},
				}}}}

				rs := newInfoRegRecordSet(s,
					interpreter.NewTxState(auth.ContextWithUser(ctx, user)), ir)
				rs.filter.Set("Slice", "S")
				row := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
				row.Set("Key", "K")
				row.Set("Value", "must-not-be-written")
				// Owner is deliberately absent. SQL comparisons with NULL are
				// UNKNOWN, including through NOT, and therefore must not admit
				// the proposed row during the in-memory preflight.
				if caught := captureInfoRegRecordSetPanic(rs.write); caught == nil {
					t.Fatal("record-set write admitted a NULL row rejected by the equivalent SQL policy")
				}

				rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{})
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 0 {
					t.Fatalf("denied record-set write persisted rows: %#v", rows)
				}
			})
		}
	})
}

func TestInfoRegRecordSet_ProposedTypedValuesUseSQLPolicyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		instant := time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC)
		offsetInstant := instant.In(time.FixedZone("+03", 3*60*60))
		cases := []struct {
			name       string
			ir         *metadata.InfoRegister
			policy     auth.RowPolicy
			fill       func(*interpreter.MapThis)
			wantDenied bool
			sqlFilter  *storage.Predicate
		}{
			{
				name: "date_eq_same_instant",
				ir: &metadata.InfoRegister{
					Name: "TypedDatePolicyEq", Periodic: true,
					Dimensions: []metadata.Field{
						{Name: "Slice", Type: metadata.FieldTypeString},
						{Name: "Key", Type: metadata.FieldTypeString},
					},
					Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
				},
				policy: auth.RowPolicy{
					Field: "period", Op: "eq", Value: auth.RowValue{Literal: instant},
				},
				fill: func(row *interpreter.MapThis) { row.Set("Период", offsetInstant) },
				sqlFilter: &storage.Predicate{
					Field: "period", Op: "eq", Value: instant,
				},
			},
			{
				name: "date_ne_same_instant",
				ir: &metadata.InfoRegister{
					Name: "TypedDatePolicyNe", Periodic: true,
					Dimensions: []metadata.Field{
						{Name: "Slice", Type: metadata.FieldTypeString},
						{Name: "Key", Type: metadata.FieldTypeString},
					},
					Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
				},
				policy: auth.RowPolicy{
					Field: "period", Op: "ne", Value: auth.RowValue{Literal: instant},
				},
				fill:       func(row *interpreter.MapThis) { row.Set("Период", offsetInstant) },
				wantDenied: true,
			},
			{
				name: "bool_rejects_non_binary_number",
				ir: &metadata.InfoRegister{
					Name: "TypedBoolPolicy",
					Dimensions: []metadata.Field{
						{Name: "Slice", Type: metadata.FieldTypeString},
						{Name: "Key", Type: metadata.FieldTypeString},
					},
					Resources: []metadata.Field{
						{Name: "Flag", Type: metadata.FieldTypeBool},
						{Name: "Value", Type: metadata.FieldTypeString},
					},
				},
				policy: auth.RowPolicy{
					Field: "Flag", Op: "eq", Value: auth.RowValue{Literal: true},
				},
				fill:       func(row *interpreter.MapThis) { row.Set("Flag", float64(2)) },
				wantDenied: true,
			},
			{
				name: "sql_postcheck_rolls_back_type_mismatch",
				ir: &metadata.InfoRegister{
					Name: "TypedDateSQLPostcheck",
					Dimensions: []metadata.Field{
						{Name: "Slice", Type: metadata.FieldTypeString},
						{Name: "Key", Type: metadata.FieldTypeString},
					},
					Resources: []metadata.Field{
						{Name: "EventAt", Type: metadata.FieldTypeDate},
						{Name: "Value", Type: metadata.FieldTypeString},
					},
				},
				policy: auth.RowPolicy{
					Field: "EventAt", Op: "ne", Value: auth.RowValue{Literal: instant},
				},
				// The in-memory comparator deliberately does not reinterpret a
				// string as a typed date. SQLite stores this exact canonical form,
				// so the authoritative SQL postcheck must reject and roll it back.
				fill: func(row *interpreter.MapThis) {
					row.Set("EventAt", instant.Format("2006-01-02 15:04:05-07:00"))
				},
				wantDenied: true,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{tc.ir}); err != nil {
					t.Fatal(err)
				}
				registry := runtime.NewRegistry()
				registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{tc.ir}})
				s := &Server{store: db, reg: registry}
				user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
					InfoRegs: map[string][]string{tc.ir.Name: {"write", "delete"}},
					RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
						tc.ir.Name: {"write": tc.policy},
					}},
				}}}}

				rs := newInfoRegRecordSet(s,
					interpreter.NewTxState(auth.ContextWithUser(ctx, user)), tc.ir)
				rs.filter.Set("Slice", "S")
				row := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
				row.Set("Key", "K")
				row.Set("Value", "candidate")
				tc.fill(row)
				caught := captureInfoRegRecordSetPanic(rs.write)
				if tc.wantDenied && caught == nil {
					t.Fatal("record-set write admitted a typed value rejected by the equivalent SQL policy")
				}
				if !tc.wantDenied && caught != nil {
					t.Fatalf("record-set write rejected a typed value admitted by the equivalent SQL policy: %v", caught)
				}

				rows, err := db.InfoRegList(ctx, tc.ir, storage.RegFilter{})
				if err != nil {
					t.Fatal(err)
				}
				wantRows := 1
				if tc.wantDenied {
					wantRows = 0
				}
				if len(rows) != wantRows {
					t.Fatalf("persisted rows = %#v, want %d", rows, wantRows)
				}
				if tc.sqlFilter != nil {
					sqlRows, err := db.InfoRegList(ctx, tc.ir, storage.RegFilter{RowFilter: tc.sqlFilter})
					if err != nil {
						t.Fatal(err)
					}
					if len(sqlRows) != wantRows {
						t.Fatalf("SQL policy rows = %#v, want %d", sqlRows, wantRows)
					}
				}
			})
		}

		t.Run("sql_postcheck_uses_exact_empty_dimension_key", func(t *testing.T) {
			ir := &metadata.InfoRegister{
				Name: "TypedDateSQLPostcheckEmptyKey",
				Dimensions: []metadata.Field{
					{Name: "Slice", Type: metadata.FieldTypeString},
					{Name: "Key", Type: metadata.FieldTypeString},
				},
				Resources: []metadata.Field{
					{Name: "EventAt", Type: metadata.FieldTypeDate},
					{Name: "Value", Type: metadata.FieldTypeString},
				},
			}
			if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
				t.Fatal(err)
			}
			registry := runtime.NewRegistry()
			registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
			s := &Server{store: db, reg: registry}
			user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
				InfoRegs: map[string][]string{ir.Name: {"write", "delete"}},
				RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
					ir.Name: {"write": {
						Field: "EventAt", Op: "ne", Value: auth.RowValue{Literal: instant},
					}},
				}},
			}}}}

			rs := newInfoRegRecordSet(s,
				interpreter.NewTxState(auth.ContextWithUser(ctx, user)), ir)
			rs.filter.Set("Slice", "S")
			allowed := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
			allowed.Set("Key", "A")
			allowed.Set("EventAt", instant.Add(time.Hour))
			allowed.Set("Value", "allowed sibling")
			denied := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
			denied.Set("Key", "")
			// The in-memory comparator deliberately leaves strings untyped, while
			// SQLite compares this canonical form as the same stored timestamp.
			// The exact SQL postcheck for Key="" must not be satisfied by Key="A".
			denied.Set("EventAt", instant.Format("2006-01-02 15:04:05-07:00"))
			denied.Set("Value", "must roll back")

			if caught := captureInfoRegRecordSetPanic(rs.write); caught == nil {
				t.Fatal("record-set SQL postcheck substituted an allowed sibling for the denied empty-key row")
			}
			rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 0 {
				t.Fatalf("denied multi-row write was not rolled back atomically: %#v", rows)
			}
		})
	})
}

func TestInfoRegRecordSet_DeletePeriodRLSUsesTypedPeriodMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name:       "PeriodPolicy",
			Periodic:   true,
			Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		period := time.Date(2026, 8, 15, 11, 22, 33, 123456000, time.UTC)
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "A"}, map[string]any{"Value": "keep"}, &period); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: registry}
		user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"write", "delete"}},
			RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
				ir.Name: {"delete": {Field: "period", Op: "ne", Value: auth.RowValue{Literal: period}}},
			}},
		}}}}
		rs := newInfoRegRecordSet(s,
			interpreter.NewTxState(auth.ContextWithUser(ctx, user)), ir)
		rs.filter.Set("Key", "A")
		if caught := captureInfoRegRecordSetPanic(rs.write); caught != nil {
			t.Fatalf("period-policy hidden row should be a no-op: %v", caught)
		}
		rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{DimValues: map[string]any{"Key": "A"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || asString(rows[0]["Value"]) != "keep" {
			t.Fatalf("display-string period bypassed delete RLS: %#v", rows)
		}
	})
}

func TestInfoRegRecordSet_PostgresConcurrentInsertCheckedByRLS(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if !db.IsPostgres() {
			t.Skip("PostgreSQL table-lock coordination")
		}
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name: "ConcurrentReplacePolicy",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{
				{Name: "Owner", Type: metadata.FieldTypeString},
				{Name: "Value", Type: metadata.FieldTypeString},
			},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: registry}
		policy := auth.RowPolicy{Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}}
		user := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"write", "delete"}},
			RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
				ir.Name: {"write": policy, "delete": policy},
			}},
		}}}}
		rs := newInfoRegRecordSet(s,
			interpreter.NewTxState(auth.ContextWithUser(ctx, user)), ir)
		rs.filter.Set("Slice", "S")
		row := rs.CallMethod("Добавить", nil).(*interpreter.MapThis)
		row.Set("Key", "K")
		row.Set("Owner", "mine")
		row.Set("Value", "replacement")

		// A concurrent writer inserts the previously absent PK and keeps its
		// RowExclusive table lock until we explicitly commit it.
		concurrentTx, concurrentCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = concurrentTx.Rollback(concurrentCtx)
			}
		}()
		if err := db.InfoRegSet(concurrentCtx, ir,
			map[string]any{"Slice": "S", "Key": "K"},
			map[string]any{"Owner": "other", "Value": "concurrent"}, nil); err != nil {
			t.Fatal(err)
		}

		result := make(chan any, 1)
		go func() { result <- captureInfoRegRecordSetPanic(rs.write) }()
		lockSQL := "LOCK TABLE " + metadata.InfoRegTableName(ir.Name) + " IN SHARE ROW EXCLUSIVE MODE"
		deadline := time.Now().Add(10 * time.Second)
		lockObserved := false
		for time.Now().Before(deadline) {
			var waiting bool
			if err := db.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM pg_stat_activity
  WHERE query LIKE $1 AND wait_event_type = 'Lock'
)`, lockSQL+"%").Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				lockObserved = true
				break
			}
			select {
			case got := <-result:
				t.Fatalf("replacement finished before concurrent transaction resolved: %v", got)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !lockObserved {
			_ = concurrentTx.Rollback(concurrentCtx)
			committed = true
			t.Fatal("record-set replacement did not wait on the PostgreSQL table write lock")
		}
		if err := concurrentTx.Commit(concurrentCtx); err != nil {
			t.Fatal(err)
		}
		committed = true

		select {
		case caught := <-result:
			if caught != nil {
				t.Fatalf("hidden concurrent conflict disclosed itself: %v", caught)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("record-set replacement did not finish after concurrent commit")
		}
		rows, err := db.InfoRegList(ctx, ir,
			storage.RegFilter{DimValues: map[string]any{"Slice": "S", "Key": "K"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || asString(rows[0]["Owner"]) != "other" || asString(rows[0]["Value"]) != "concurrent" {
			t.Fatalf("record-set overwrote concurrent hidden row: %#v", rows)
		}
	})
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
	С1 = Н.Добавить(); С1.Событие = "Старт";
	С2 = Н.Добавить(); С2.Событие = "Стоп";
	Сообщить(Н.Получить(0).Событие);
	Сообщить(Н.Получить("1").Событие);`)
	if err != nil {
		t.Fatalf("Получить по числовому/строковому индексу: %v", err)
	}
	if len(msgs) != 2 || msgs[0] != "Старт" || msgs[1] != "Стоп" {
		t.Fatalf("Получить вернул %v, ожидалось [Старт Стоп]", msgs)
	}

	_, err = runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  Н.Получить(5);`)
	if err == nil || !strings.Contains(err.Error(), "вне набора") {
		t.Fatalf("выход за границы: %v", err)
	}

	// Нечисловая строка не должна молча превращаться в индекс 0 и отдавать
	// первую запись набора.
	_, err = runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.Отбор.Узел = "N1";
  С = Н.Добавить(); С.Событие = "Старт";
  Н.Получить("не-число");`)
	if err == nil || !strings.Contains(err.Error(), "индекс -1 вне набора") {
		t.Fatalf("нечисловой индекс: %v", err)
	}

	// Диагностика неизвестного метода перечисляет и новый публичный метод.
	_, err = runInfoRegDSL(t, db, ir, `
  Н = РегистрыСведений.ЛогОбмена.СоздатьНаборЗаписей();
  Н.НетТакогоМетода();`)
	if err == nil || !strings.Contains(err.Error(), "Получить") {
		t.Fatalf("диагностика неизвестного метода не называет Получить: %v", err)
	}
}
