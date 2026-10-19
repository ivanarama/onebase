package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegRowIndexAccessMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name: "AccessIndex",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{
				{Name: "Owner", Type: metadata.FieldTypeString},
				{Name: "Secret", Type: metadata.FieldTypeString},
				{Name: "Value", Type: metadata.FieldTypeString},
			},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"mine", "other"} {
			if err := db.InfoRegSet(ctx, ir, map[string]any{"Slice": "S", "Key": key}, map[string]any{"Owner": key, "Secret": "secret-" + key, "Value": "keep"}, nil); err != nil {
				t.Fatal(err)
			}
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), messages: NewMessageStore(), lockMgr: runtime.NewLockManager()}
		s.entitySvc = s.newEntityService(nil)
		role := &auth.Role{Permissions: auth.Permission{
			InfoRegs: map[string][]string{ir.Name: {"read", "write", "delete"}},
			FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{
				ir.Name: {"Secret": {Read: "mask_all"}},
			}},
			RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{
				ir.Name: {
					"read":   {Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}},
					"write":  {Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}},
					"delete": {Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}},
				},
			}},
		}}
		run := func(user *auth.User, body string) ([]string, error) {
			t.Helper()
			prog := mustParse(t, "Процедура Тест()\n"+body+"\nКонецПроцедуры")
			var messages []string
			vars, tx := s.buildDSLVarsWithMessagesTx(auth.ContextWithUser(ctx, user), nil, &messages)
			defer interpreter.RollbackTxExecution(tx)
			err := s.interp.Run(prog.Procedures[0], nil, vars)
			return messages, err
		}
		user := &auth.User{ID: "index-reader", Roles: []*auth.Role{role}}
		const read = `Н = РегистрыСведений.AccessIndex.СоздатьНаборЗаписей();
Н.Отбор.Slice = "S";
Н.Прочитать();
Сообщить(Н.Количество());
Стр = Н.Получить(0);
Сообщить(Стр["Secret"]);
Сообщить(Стр.Secret);
Для Каждого Запись Из Н Цикл
  Сообщить(Запись["Secret"]);
КонецЦикла;`
		messages, err := run(user, read)
		if err != nil || strings.Join(messages, "|") != "1|••••••|••••••|••••••" {
			t.Fatalf("indexed read bypassed mask/RLS: %q, %v", messages, err)
		}
		// Mutating a row through an index does not bypass object permissions
		// or the proposed-row policy when the normal record set is written.
		for _, tc := range []struct {
			name string
			user *auth.User
			body string
		}{
			{"RBAC", &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{InfoRegs: map[string][]string{ir.Name: {"read"}}}}}}, `Стр["Value"] = "changed";`},
			{"RLS", user, `Стр["Owner"] = "other";`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := run(tc.user, read+tc.body+`Н.Записать();`)
				if err == nil {
					t.Fatal("indexed edit bypassed write access")
				}
				rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{})
				if err != nil || len(rows) != 2 {
					t.Fatalf("rows after denied write: %+v, %v", rows, err)
				}
				for _, row := range rows {
					if row["Value"] != "keep" || row["Owner"] != row["Key"] {
						t.Fatalf("denied write changed rows: %+v", rows)
					}
				}
			})
		}
	})
}

type infoRegIndexCondition struct {
	expr  string
	asked bool
	value bool
	err   error
}

func (h *infoRegIndexCondition) HookCheckBreakpoint(_ string, _ int, cond func(string) (bool, error)) bool {
	if !h.asked {
		h.asked = true
		h.value, h.err = cond(h.expr)
	}
	return false
}
func (*infoRegIndexCondition) HookShouldStep(string, int) bool { return false }
func (*infoRegIndexCondition) HookOnPause(string, int, map[string]any, func(string) (any, error), string) {
}
func (*infoRegIndexCondition) HookPushFrame(string, int) {}
func (*infoRegIndexCondition) HookPopFrame()             {}

func TestInfoRegRowIndexReadOnly(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ir := stateInfoReg(false, false)
		if err := db.MigrateInfoRegisters(context.Background(), []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), messages: NewMessageStore()}
		vars, tx := s.buildDSLVarsWithMessagesTx(context.Background(), nil, nil)
		defer interpreter.RollbackTxExecution(tx)
		create := mustParse(t, `Функция СоздатьСтроку()
Н = РегистрыСведений.СостояниеУзлов.СоздатьНаборЗаписей();
Стр = Н.Добавить();
Стр.Состояние = "keep";
Возврат Стр;
КонецФункции`)
		value, err := s.interp.Call(create.Procedures[0], nil, nil, vars)
		if err != nil {
			t.Fatal(err)
		}
		row := value.(interpreter.This)
		prog := mustParse(t, `Процедура Тест()
Х = 1;
КонецПроцедуры
Функция Изменить(Стр)
Попытка
  Стр["Состояние"] = "changed";
Исключение
  Возврат Истина;
КонецПопытки;
Возврат Истина;
КонецФункции`)
		for _, expr := range []string{`ЭтотОбъект["Состояние"] = "keep"`, `Изменить(ЭтотОбъект)`} {
			hook := &infoRegIndexCondition{expr: expr}
			i := interpreter.New()
			i.DebugSource = func() interpreter.DebugHook { return hook }
			i.LookupProc = func(name string) *ast.ProcedureDecl { return prog.Procedures[1] }
			if err := i.Run(prog.Procedures[0], row); err != nil {
				t.Fatal(err)
			}
			if !hook.asked {
				t.Fatal("read-only condition was not evaluated")
			}
			if strings.HasPrefix(expr, "Изменить") {
				if hook.err == nil || !strings.Contains(hook.err.Error(), "изменение элемента коллекции недоступен") {
					t.Fatalf("read-only write not rejected: %v", hook.err)
				}
			} else if hook.err != nil || !hook.value {
				t.Fatalf("read-only index read: %v, %v", hook.value, hook.err)
			}
			if got := row.Get("Состояние"); got != "keep" {
				t.Fatalf("read-only condition mutated row: %v", got)
			}
		}
	})
}

func TestInfoRegRowIndexDoesNotExtendAccumulationRows(t *testing.T) {
	f := newRegisterMaskFixture(t)
	messages, err := runDSLBody(t, f.s, `
Строки = РегистрыНакопления.Продажи.Движения();
Стр = Строки.Получить(0);
Сообщить(ТипЗнч(Стр.Количество));
Попытка
  Х = Стр["Количество"];
  Сообщить("unexpected read");
Исключение
  Сообщить("read denied");
КонецПопытки;
Попытка
  Стр["Количество"] = 100;
  Сообщить("unexpected write");
Исключение
  Сообщить("write denied");
КонецПопытки;`)
	if err != nil || strings.Join(messages, "|") != "Число|read denied|write denied" {
		t.Fatalf("accumulation row contract changed: %q, %v", messages, err)
	}
}
