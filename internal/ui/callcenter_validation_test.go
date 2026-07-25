package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// validationFixture грузит поставляемый examples/callcenter целиком. Тесты
// проверяют именно прикладной паттерн плана 89: YAML, DSL-хуки, проведение и
// периодический регистр, а не искусственную копию конфигурации в Go.
type validationFixture struct {
	t      *testing.T
	ctx    context.Context
	proj   *project.Project
	db     *storage.DB
	server *Server
}

func newValidationFixture(t *testing.T) *validationFixture {
	t.Helper()
	ctx := context.Background()
	proj, err := project.Load(filepath.Join("..", "..", "examples", "callcenter"))
	if err != nil {
		t.Fatalf("load callcenter: %v", err)
	}
	t.Cleanup(proj.Close)

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "validation.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("Migrate entities: %v", err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		t.Fatalf("Migrate registers: %v", err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		t.Fatalf("Migrate info registers: %v", err)
	}
	if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
		t.Fatalf("Migrate constants: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Entities:        proj.Entities,
		Programs:        proj.Programs,
		ManagerPrograms: proj.ManagerPrograms,
		Registers:       proj.Registers,
		InfoRegs:        proj.InfoRegisters,
		Enums:           proj.Enums,
		Constants:       proj.Constants,
	})
	reg.LoadModules(proj.Modules)
	reg.LoadProcessors(proj.Processors)

	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc

	s := &Server{
		store:    db,
		reg:      reg,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	s.entitySvc = s.newEntityService(nil)
	return &validationFixture{t: t, ctx: ctx, proj: proj, db: db, server: s}
}

func (f *validationFixture) entity(name string) *metadata.Entity {
	f.t.Helper()
	e := f.server.reg.GetEntity(name)
	if e == nil {
		f.t.Fatalf("entity %s not found", name)
	}
	return e
}

func (f *validationFixture) documentRef(vars map[string]any, entityName string, id uuid.UUID) *interpreter.Ref {
	f.t.Helper()
	root, ok := vars["Документы"].(*docsRoot)
	if !ok {
		f.t.Fatalf("Документы: %T, want *docsRoot", vars["Документы"])
	}
	manager, ok := root.Get(entityName).(*docProxy)
	if !ok {
		f.t.Fatalf("Документы.%s: %T, want *docProxy", entityName, root.Get(entityName))
	}
	return &interpreter.Ref{UUID: id.String(), Name: entityName, Type: entityName, Manager: manager}
}

func (f *validationFixture) callApproval(vars map[string]any, name string, args ...any) (any, error) {
	f.t.Helper()
	proc := f.server.reg.GetModuleNamespacedProc("Согласование", name)
	if proc == nil {
		f.t.Fatalf("Согласование.%s not found", name)
	}
	return f.server.interp.Call(proc, nil, args, vars)
}

func (f *validationFixture) seedRules() {
	f.t.Helper()
	_, runErr, err := RunProcessorOffline(f.ctx, f.proj, f.db, "ЗаполнитьПравилаВалидации", nil, nil)
	if err != nil {
		f.t.Fatalf("RunProcessorOffline: %v", err)
	}
	if runErr != nil {
		f.t.Fatalf("seed rules: %v", runErr)
	}
}

func (f *validationFixture) versionCount() int {
	f.t.Helper()
	var count int
	if err := f.db.QueryRow(f.ctx, "SELECT COUNT(*) FROM инфо_действующиеправила").Scan(&count); err != nil {
		f.t.Fatalf("count rule versions: %v", err)
	}
	return count
}

func (f *validationFixture) saveRequest(date time.Time, street, phone, comment string) entityservice.SaveResult {
	f.t.Helper()
	res, err := f.server.entitySvc.Save(f.ctx, entityservice.SaveRequest{
		Entity: f.entity("Заявка"),
		ID:     uuid.New(),
		IsNew:  true,
		Fields: map[string]any{
			"Номер":       "TEST-" + uuid.NewString()[:8],
			"Дата":        date,
			"Улица":       street,
			"Телефон":     phone,
			"Комментарий": comment,
		},
	})
	if err != nil {
		f.t.Fatalf("save request: %v", err)
	}
	return res
}

func (f *validationFixture) publish(ruleID string, date time.Time, number, level, text string) (uuid.UUID, entityservice.SaveResult) {
	f.t.Helper()
	id := uuid.New()
	res, err := f.server.entitySvc.Save(f.ctx, entityservice.SaveRequest{
		Entity: f.entity("ПубликацияПравила"),
		ID:     id,
		IsNew:  true,
		Action: "post",
		Fields: map[string]any{
			"Номер":     number,
			"Дата":      date,
			"Правило":   ruleID,
			"Уровень":   level,
			"Текст":     text,
			"Статус":    "Опубликовано",
			"Основание": "Автотест плана 89",
		},
	})
	if err != nil {
		f.t.Fatalf("publish rule: %v", err)
	}
	return id, res
}

func TestCallcenterValidation_EffectiveDatingAndSeedRecovery(t *testing.T) {
	f := newValidationFixture(t)

	// Имитируем частичный прошлый запуск: правило создано, публикации нет.
	if _, err := f.db.WriteCatalogRecord(f.ctx, f.entity("ПравилаВалидации"), "", map[string]any{
		"Наименование":     "Заявка: улица обязательна",
		"Код":              "REQ-STREET",
		"ОбъектМетаданных": "Заявка",
		"Поле":             "Улица",
		"ВидПроверки":      "НеПусто",
		"Владелец":         "Руководитель КЦ",
	}); err != nil {
		t.Fatalf("precreate rule: %v", err)
	}

	f.seedRules()
	if got := f.versionCount(); got != 2 {
		t.Fatalf("seed must recover missing publication and create 2 versions, got %d", got)
	}
	f.seedRules()
	if got := f.versionCount(); got != 2 {
		t.Fatalf("repeated seed must be idempotent, got %d versions", got)
	}

	before := f.saveRequest(time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "", "79990000000", "")
	if before.DSLError != "" {
		t.Fatalf("rule must not apply before effective date: %s", before.DSLError)
	}
	after := f.saveRequest(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "", "79990000000", "")
	if !strings.Contains(after.DSLError, "[REQ-STREET]") {
		t.Fatalf("active BLOCK must reject request with stable rule code, got %q", after.DSLError)
	}
}

func TestCallcenterValidation_PhoneRejectsNonDigits(t *testing.T) {
	f := newValidationFixture(t)
	f.seedRules()

	ruleID, err := f.db.WriteCatalogRecord(f.ctx, f.entity("ПравилаВалидации"), "", map[string]any{
		"Наименование":     "Заявка: формат телефона",
		"Код":              "REQ-PHONE",
		"ОбъектМетаданных": "Заявка",
		"Поле":             "Телефон",
		"ВидПроверки":      "ФорматТелефона",
		"Владелец":         "Руководитель КЦ",
	})
	if err != nil {
		t.Fatalf("create phone rule: %v", err)
	}
	_, published := f.publish(ruleID, time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), "PHONE-1", "Блокирующее", "Некорректный телефон")
	if published.DSLError != "" {
		t.Fatalf("publish phone rule: %s", published.DSLError)
	}

	invalid := f.saveRequest(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "Тверская", "abcdefghijk", "test")
	if !strings.Contains(invalid.DSLError, "[REQ-PHONE]") {
		t.Fatalf("11 letters must not pass phone validation, got %q", invalid.DSLError)
	}
	valid := f.saveRequest(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "Тверская", "+7 (999) 000-00-00", "test")
	if valid.DSLError != "" {
		t.Fatalf("formatted 11-digit phone must pass: %s", valid.DSLError)
	}
}

func TestCallcenterValidation_PublishedVersionsAreImmutable(t *testing.T) {
	f := newValidationFixture(t)
	f.seedRules()

	var publicationID, ruleID, effectiveRaw string
	if err := f.db.QueryRow(f.ctx, `
		SELECT id, правило_id, дата
		FROM публикацияправила
		WHERE статус = 'Опубликовано'
		ORDER BY номер
		LIMIT 1`).Scan(&publicationID, &ruleID, &effectiveRaw); err != nil {
		t.Fatalf("load seeded publication: %v", err)
	}
	effective, ok := storage.ParseRegPeriod(effectiveRaw)
	if !ok {
		t.Fatalf("parse effective date %q", effectiveRaw)
	}
	id, err := uuid.Parse(publicationID)
	if err != nil {
		t.Fatalf("parse publication id: %v", err)
	}

	unposted, err := f.server.entitySvc.Unpost(f.ctx, f.entity("ПубликацияПравила"), id)
	if err != nil {
		t.Fatalf("unpost technical error: %v", err)
	}
	if !strings.Contains(unposted.DSLError, "нельзя отменить") {
		t.Fatalf("unpost must be rejected, got %q", unposted.DSLError)
	}
	if got := f.versionCount(); got != 2 {
		t.Fatalf("failed unpost must preserve history, got %d versions", got)
	}

	stored, err := f.db.GetByID(f.ctx, "ПубликацияПравила", id, f.entity("ПубликацияПравила"))
	if err != nil {
		t.Fatalf("load publication: %v", err)
	}
	stored["Текст"] = "Попытка изменить историю"
	for _, action := range []string{"", "post"} {
		res, saveErr := f.server.entitySvc.Save(f.ctx, entityservice.SaveRequest{
			Entity: f.entity("ПубликацияПравила"),
			ID:     id,
			IsNew:  false,
			Action: action,
			Fields: stored,
		})
		if saveErr != nil {
			t.Fatalf("immutable save action %q: %v", action, saveErr)
		}
		if !strings.Contains(res.DSLError, "нельзя изменять или перепроводить") {
			t.Fatalf("action %q must reject mutation of published version, got %q", action, res.DSLError)
		}
	}

	_, duplicate := f.publish(ruleID, effective, "DUPLICATE", "Блокирующее", "Новая версия с той же датой")
	if !strings.Contains(duplicate.DSLError, "уже существует версия с такой датой") {
		t.Fatalf("same effective date must not overwrite old row, got %q", duplicate.DSLError)
	}
	if got := f.versionCount(); got != 2 {
		t.Fatalf("duplicate effective date must preserve history, got %d versions", got)
	}

	var posted bool
	if err := f.db.QueryRow(f.ctx, "SELECT posted FROM публикацияправила WHERE id = "+f.db.Dialect().Placeholder(1), publicationID).Scan(&posted); err != nil {
		t.Fatalf("read posted flag: %v", err)
	}
	if !posted {
		t.Fatal("rejected mutations must leave original publication posted")
	}

}

func TestCallcenterApproval_SubmitAndApprove(t *testing.T) {
	f := newValidationFixture(t)
	request := f.saveRequest(time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), "Тверская", "79990000000", "")
	if request.DSLError != "" {
		t.Fatalf("create request: %s", request.DSLError)
	}

	vars := f.server.buildDSLVars(f.ctx, nil)
	requestRef := f.documentRef(vars, "Заявка", request.ID)
	taskValue, err := f.callApproval(vars, "ОтправитьНаСогласование", requestRef, "operator", "supervisor")
	if err != nil {
		t.Fatalf("submit for approval: %v", err)
	}
	taskRef, ok := taskValue.(*interpreter.Ref)
	if !ok {
		t.Fatalf("approval task: %T, want *interpreter.Ref", taskValue)
	}

	requestRow, err := f.db.GetByID(f.ctx, "Заявка", request.ID, f.entity("Заявка"))
	if err != nil {
		t.Fatal(err)
	}
	if got := requestRow["Состояние"]; got != "НаСогласовании" {
		t.Fatalf("Заявка.Состояние = %v, want НаСогласовании", got)
	}
	taskID, err := uuid.Parse(taskRef.UUID)
	if err != nil {
		t.Fatal(err)
	}
	taskRow, err := f.db.GetByID(f.ctx, "Задача", taskID, f.entity("Задача"))
	if err != nil {
		t.Fatal(err)
	}
	if taskRow["Состояние"] != "Открыта" || taskRow["Адресат"] != "supervisor" {
		t.Fatalf("approval task fields: %#v", taskRow)
	}

	if _, err := f.callApproval(vars, "ВыполнитьЗадачу", taskRef, "supervisor", "Утверждено", "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	requestRow, err = f.db.GetByID(f.ctx, "Заявка", request.ID, f.entity("Заявка"))
	if err != nil {
		t.Fatal(err)
	}
	taskRow, err = f.db.GetByID(f.ctx, "Задача", taskID, f.entity("Задача"))
	if err != nil {
		t.Fatal(err)
	}
	if requestRow["Состояние"] != "Утверждена" || taskRow["Состояние"] != "Выполнена" {
		t.Fatalf("approved route: request=%#v task=%#v", requestRow, taskRow)
	}
}

func TestCallcenterApproval_EnforcesSeparationOfDuties(t *testing.T) {
	f := newValidationFixture(t)
	request := f.saveRequest(time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), "Тверская", "79990000000", "")
	if request.DSLError != "" {
		t.Fatalf("create request: %s", request.DSLError)
	}

	vars := f.server.buildDSLVars(f.ctx, nil)
	requestRef := f.documentRef(vars, "Заявка", request.ID)
	if _, err := f.callApproval(vars, "ОтправитьНаСогласование", requestRef, "operator", "operator"); err == nil ||
		!strings.Contains(err.Error(), "собственную заявку") {
		t.Fatalf("self-approval must be rejected, got %v", err)
	}

	taskValue, err := f.callApproval(vars, "ОтправитьНаСогласование", requestRef, "operator", "supervisor")
	if err != nil {
		t.Fatalf("submit for approval: %v", err)
	}
	taskRef := taskValue.(*interpreter.Ref)
	if _, err := f.callApproval(vars, "ВыполнитьЗадачу", taskRef, "operator", "Утверждено", "self"); err == nil ||
		!strings.Contains(err.Error(), "только её адресат") {
		t.Fatalf("non-addressee completion must be rejected, got %v", err)
	}

	requestRow, err := f.db.GetByID(f.ctx, "Заявка", request.ID, f.entity("Заявка"))
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.MustParse(taskRef.UUID)
	taskRow, err := f.db.GetByID(f.ctx, "Задача", taskID, f.entity("Задача"))
	if err != nil {
		t.Fatal(err)
	}
	if requestRow["Состояние"] != "НаСогласовании" || taskRow["Состояние"] != "Открыта" {
		t.Fatalf("rejected completion changed state: request=%#v task=%#v", requestRow, taskRow)
	}
}

func TestCallcenterApproval_RejectCreatesReworkTask(t *testing.T) {
	f := newValidationFixture(t)
	request := f.saveRequest(time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), "Тверская", "79990000000", "")
	if request.DSLError != "" {
		t.Fatalf("create request: %s", request.DSLError)
	}

	vars := f.server.buildDSLVars(f.ctx, nil)
	requestRef := f.documentRef(vars, "Заявка", request.ID)
	taskValue, err := f.callApproval(vars, "ОтправитьНаСогласование", requestRef, "operator", "supervisor")
	if err != nil {
		t.Fatalf("submit for approval: %v", err)
	}
	taskRef := taskValue.(*interpreter.Ref)

	if _, err := f.callApproval(vars, "ВыполнитьЗадачу", taskRef, "supervisor", "unknown", ""); err == nil {
		t.Fatal("invalid result must be rejected")
	}
	reworkValue, err := f.callApproval(vars, "ВыполнитьЗадачу", taskRef, "supervisor", "Отклонено", "needs changes")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	reworkRef, ok := reworkValue.(*interpreter.Ref)
	if !ok {
		t.Fatalf("rework task: %T, want *interpreter.Ref", reworkValue)
	}

	requestRow, err := f.db.GetByID(f.ctx, "Заявка", request.ID, f.entity("Заявка"))
	if err != nil {
		t.Fatal(err)
	}
	reworkID, err := uuid.Parse(reworkRef.UUID)
	if err != nil {
		t.Fatal(err)
	}
	reworkRow, err := f.db.GetByID(f.ctx, "Задача", reworkID, f.entity("Задача"))
	if err != nil {
		t.Fatal(err)
	}
	if requestRow["Состояние"] != "Отклонена" || reworkRow["Тип"] != "Доработка" || reworkRow["Адресат"] != "operator" || reworkRow["Состояние"] != "Открыта" {
		t.Fatalf("rejected route: request=%#v rework=%#v", requestRow, reworkRow)
	}
}

func TestCallcenterApproval_TaskAndRequestRollbackTogether(t *testing.T) {
	f := newValidationFixture(t)
	f.seedRules()

	requestID := uuid.New()
	if err := f.db.Upsert(f.ctx, "Заявка", requestID, map[string]any{
		"Номер":     "ROLLBACK",
		"Дата":      time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
		"Состояние": "НаСогласовании",
		"Улица":     "", // active REQ-STREET rule makes the second route write fail
		"Телефон":   "79990000000",
	}, f.entity("Заявка")); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.New()
	if err := f.db.Upsert(f.ctx, "Задача", taskID, map[string]any{
		"Номер":     "TASK-ROLLBACK",
		"Дата":      time.Now(),
		"Тип":       "Согласование",
		"Инициатор": "operator",
		"Основание": requestID.String(),
		"Адресат":   "supervisor",
		"Состояние": "Открыта",
	}, f.entity("Задача")); err != nil {
		t.Fatal(err)
	}

	vars := f.server.buildDSLVars(f.ctx, nil)
	taskRef := f.documentRef(vars, "Задача", taskID)
	if _, err := f.callApproval(vars, "ВыполнитьЗадачу", taskRef, "supervisor", "Утверждено", "ok"); err == nil || !strings.Contains(err.Error(), "REQ-STREET") {
		t.Fatalf("expected request validation error, got %v", err)
	}

	requestRow, err := f.db.GetByID(f.ctx, "Заявка", requestID, f.entity("Заявка"))
	if err != nil {
		t.Fatal(err)
	}
	taskRow, err := f.db.GetByID(f.ctx, "Задача", taskID, f.entity("Задача"))
	if err != nil {
		t.Fatal(err)
	}
	if requestRow["Состояние"] != "НаСогласовании" || taskRow["Состояние"] != "Открыта" {
		t.Fatalf("route rollback failed: request=%#v task=%#v", requestRow, taskRow)
	}
}
