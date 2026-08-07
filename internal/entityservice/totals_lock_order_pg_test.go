//go:build integration

package entityservice

// Регрессия к issue #626: параллельное проведение документов, пишущих движения
// в НЕСКОЛЬКО регистров с включёнными итогами, не должно упираться в
// «deadlock detected».
//
// До фикса каждый Write*Movements брал свой advisory-лок сам, а обход mc.All()
// — обычная Go-map со случайным порядком: одно проведение захватывало
// «register-totals|A», потом «register-totals|B», параллельное — наоборот, и
// PostgreSQL снимал одно из них с SQLSTATE 40P01. AdvisoryXactLock сортирует
// ключи, но только внутри одного вызова, а вызовов было несколько.
//
// Тест вероятностный по своей природе: без фикса он падает не на каждом
// прогоне. Пар проведений намеренно много, чтобы поймать окно.

import (
	"context"
	"os"
	"strings"
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

func TestSave_ConcurrentPostingsToTwoTotalsRegistersDoNotDeadlock(t *testing.T) {
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
		Name: "ДедлокИтоговДок", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		},
	}
	// Два регистра С ИТОГАМИ — именно на них берутся advisory-локи. Имена
	// подобраны так, чтобы алфавитный порядок не совпадал с порядком записи в
	// обработчике: иначе случайный порядок карты мог бы совпасть с сортировкой.
	regA := &metadata.Register{
		Name:       "ДедлокИтогиАльфа",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		Totals:     metadata.RegisterTotals{Enabled: true},
	}
	regB := &metadata.Register{
		Name:       "ДедлокИтогиОмега",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		Totals:     metadata.RegisterTotals{Enabled: true},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{regA, regB}); err != nil {
		t.Fatal(err)
	}

	onPost := mustParseProgramT(t, `Процедура OnPost()
  ДвА = Движения.ДедлокИтогиОмега.Добавить();
  ДвА.ВидДвижения  = "Приход";
  ДвА.Номенклатура = this.Номенклатура;
  ДвА.Количество   = this.Количество;

  ДвБ = Движения.ДедлокИтогиАльфа.Добавить();
  ДвБ.ВидДвижения  = "Приход";
  ДвБ.Номенклатура = this.Номенклатура;
  ДвБ.Количество   = this.Количество;
КонецПроцедуры`)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: []*metadata.Register{regA, regB},
		Programs:  map[string]*ast.Program{doc.Name: onPost},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	newService := func() *Service {
		return &Service{
			Store: db, Reg: registry, Interp: interp,
			BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) map[string]any {
				return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build()
			},
		}
	}

	const pairs = 12
	errs := make([]error, pairs*2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < pairs*2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := newService()
			<-start
			_, err := svc.Save(ctx, SaveRequest{
				Entity: doc,
				ID:     uuid.New(),
				IsNew:  true,
				Fields: map[string]any{
					"Номенклатура": "дедлок-" + uuid.New().String(),
					"Количество":   float64(1),
				},
				Action: "post",
			})
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "deadlock detected") || strings.Contains(err.Error(), "40P01") {
			t.Fatalf("проведение %d упало по взаимной блокировке — порядок захвата локов итогов "+
				"снова недетерминирован (#626): %v", i, err)
		}
		t.Errorf("проведение %d: неожиданная ошибка: %v", i, err)
	}
}
