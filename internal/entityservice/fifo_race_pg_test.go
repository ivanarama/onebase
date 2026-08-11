//go:build integration

package entityservice

// Гоночный тест issue #458: два параллельных проведения расходных документов
// по одной номенклатуре не должны списать одни и те же остатки дважды.
//
// ОбработкаПроведения повторяет форму ФИФО из examples/trade: БлокировкаДанных
// по измерению → чтение остатков → проверка достаточности → расходное движение.
// Каждое проведение идёт через СВОЙ LockManager — как два отдельных серверных
// процесса над одной БД: внутрипроцессные мьютексы не пересекаются, и
// сериализовать конкурентов может только pg_advisory_xact_lock, взятый в момент
// Заблокировать(). До фикса оба проведения читали остаток 10, оба проходили
// проверку «6 ≤ 10» и итог уходил в минус.

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestSave_ConcurrentFIFOPostingsDoNotDoubleSpend(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	doc := &metadata.Entity{
		Name: "ФИФОГонкаСписание", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиФИФОГонка",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	onPost := mustParseProgramT(t, `Процедура OnPost()
  Блокировка = БлокировкаДанных();
  Эл = Блокировка.Добавить("ОстаткиФИФОГонка");
  Эл.УстановитьЗначение("Номенклатура", this.Номенклатура);
  Блокировка.Заблокировать();

  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ КоличествоОстаток
                  ИЗ РегистрНакопления.ОстаткиФИФОГонка.Остатки()
                  ГДЕ Номенклатура = &Ном";
  Запрос.УстановитьПараметр("Ном", this.Номенклатура);
  Результат = Запрос.Выполнить();
  Доступно = 0;
  Для Каждого Стр Из Результат Цикл
    Доступно = Доступно + Стр.КоличествоОстаток;
  КонецЦикла;
  Если Доступно < this.Количество Тогда
    ВызватьИсключение("Недостаточно остатков: нужно " + Строка(this.Количество)
      + ", доступно " + Строка(Доступно));
  КонецЕсли;

  Дв = Движения.ОстаткиФИФОГонка.Добавить();
  Дв.ВидДвижения  = "Расход";
  Дв.Номенклатура = this.Номенклатура;
  Дв.Количество   = this.Количество;
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: []*metadata.Register{reg},
		Programs:  map[string]*ast.Program{doc.Name: onPost},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	// newService — отдельный «серверный процесс»: свой LockManager, общая БД.
	// Проводка БлокировкаДанных повторяет ui.buildDSLVars (handlers_dsl.go).
	newService := func() *Service {
		lockMgr := runtime.NewLockManager()
		return &Service{
			Store: db, Reg: registry, Interp: interp,
			BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
				vars := dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build()
				vars["БлокировкаДанных"] = interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
					lo := runtime.NewLockObjectWithCollector(lockMgr, runtime.LockCollectorFromContext(c))
					lo.WithAdvisory(func(keys []string) {
						if !storage.HasTx(c) {
							return
						}
						if err := db.AdvisoryXactLock(c, keys); err != nil {
							interpreter.RaiseUserError(err.Error())
						}
					})
					return lo, nil
				})
				return vars, nil
			},
		}
	}

	// Номенклатура уникальна на прогон — тестовая БД персистентна.
	nom := "гонка-" + uuid.New().String()

	// Опорный приход 10 напрямую в регистр (вид движения по умолчанию «Приход»).
	if err := db.WriteMovements(ctx, reg.Name, "seed", uuid.New(),
		[]map[string]any{{"Номенклатура": nom, "Количество": float64(10)}}, reg, nil); err != nil {
		t.Fatal(err)
	}

	// Два конкурентных списания по 6: суммарно 12 > 10 — пройти должно ровно одно.
	type outcome struct {
		err    error
		dslErr string
	}
	results := make([]outcome, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := newService()
			<-start
			res, err := svc.Save(ctx, SaveRequest{
				Entity: doc,
				ID:     uuid.New(),
				IsNew:  true,
				Fields: map[string]any{"Номенклатура": nom, "Количество": float64(6)},
				Action: "post",
			})
			results[i] = outcome{err: err}
			if err == nil {
				results[i].dslErr = res.DSLError
			}
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i, r := range results {
		if r.err == nil && r.dslErr == "" {
			succeeded++
		} else {
			t.Logf("проведение %d отклонено: err=%v dsl=%q", i, r.err, r.dslErr)
		}
	}
	if succeeded != 1 {
		t.Errorf("провестись должно ровно 1 списание из 2, прошло %d", succeeded)
	}

	var balance float64
	if err := db.QueryRow(ctx,
		"SELECT COALESCE(SUM(CASE WHEN вид_движения = 'Расход' THEN -количество ELSE количество END), 0) FROM "+
			metadata.RegisterTableName(reg.Name)+" WHERE номенклатура = $1", nom).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 4 {
		t.Errorf("остаток после гонки = %v, ожидался 4 (10 прихода − одно списание 6)", balance)
	}
}
