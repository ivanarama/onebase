package ui

// Серверные события управляемых форм, исполняемые ДО рендера HTML (issue #148).
//
// Раньше единственным «событием открытия» был ПриОткрытии — но он исполняется
// на КЛИЕНТЕ (obFire по DOMContentLoaded), уже после того как сервер отдал форму
// со всеми полями. Это не позволяло реализовать RLS на чтение: пользователь
// видел данные чужой записи, даже если обработчик потом бросал исключение.
//
// ПриЧтенииНаСервере (по аналогии с 1С «ПриЧтенииНаСервере») исполняется
// синхронно на сервере при GET формы объекта, до формирования HTML. Если
// обработчик бросает ВызватьИсключение — formEdit отдаёт 403 и не раскрывает
// данные.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// loadRuntimeObject загружает существующую запись (шапка + табличные части +
// обогащённые ссылки) в *runtime.Object — пригодный для исполнения обработчиков
// формы как «Объект». Та же логика, что в docProxy.LoadObject.
func (s *Server) loadRuntimeObject(ctx context.Context, entity *metadata.Entity, id uuid.UUID) (*runtime.Object, error) {
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("объект %s/%s не найден", entity.Name, id)
	}
	tpRows := make(map[string][]map[string]any, len(entity.TableParts))
	for _, tp := range entity.TableParts {
		rows, err := s.store.GetTablePartRows(ctx, entity.Name, tp.Name, id, tp)
		if err != nil {
			return nil, fmt.Errorf("табличная часть %s: %w", tp.Name, err)
		}
		tpRows[tp.Name] = rows
	}
	return s.runtimeObjectFromSnapshot(ctx, entity, id, row, tpRows), nil
}

// runtimeObjectFromSnapshot строит объект формы из уже загруженного снимка.
// Карты и строки ТЧ копируются перед enrich: read-hook может читать и менять
// Объект, но не должен незаметно менять канонические данные, которые вызывающий
// код затем использует для рендера или «Создать копированием».
func (s *Server) runtimeObjectFromSnapshot(
	ctx context.Context,
	entity *metadata.Entity,
	id uuid.UUID,
	row map[string]any,
	tablePartRows map[string][]map[string]any,
) *runtime.Object {
	fields := make(map[string]any, len(entity.Fields))
	for _, f := range entity.Fields {
		if v, ok := maskCIKeyValue(row, f.Name); ok && v != nil {
			fields[strings.ToLower(f.Name)] = v
		}
	}
	tpRows := cloneTablePartRowMap(tablePartRows)
	obj := &runtime.Object{
		ID:            id,
		Type:          entity.Name,
		Kind:          entity.Kind,
		Fields:        fields,
		TablePartRows: tpRows,
	}
	s.enrichHeaderRefs(ctx, entity, obj)
	for _, tp := range entity.TableParts {
		s.enrichTPRowsWithRefs(ctx, tp, tpRows[tp.Name])
	}
	return obj
}

// runFormReadHook исполняет серверный обработчик ПриЧтенииНаСервере формы объекта
// (если он объявлен) с «Объект», загруженным из БД. Возвращает ошибку обработчика
// (если он бросил исключение) — тогда вызывающий код обязан отказать в рендере.
//
// Если формы/обработчика/AST нет — возвращает nil (no-op): RLS-хук не объявлен,
// доступ разрешён обычным путём.
//
// ВАЖНО (fail-closed): когда обработчик ОБЪЯВЛЕН, но загрузить объект из БД не
// удалось, возвращаем саму ошибку, а не nil. Иначе formEdit отрисовал бы форму,
// ни разу не выполнив RLS-хук — fail-open (обход контроля доступа на чтение).
// nil допустим только пока обработчик ещё не объявлен (ранние return выше).
func (s *Server) runFormReadHook(ctx context.Context, entity *metadata.Entity, form *metadata.FormModule, id uuid.UUID) error {
	if form == nil || s.interp == nil {
		return nil
	}
	program, decl := formReadHook(form)
	if program == nil || decl == nil {
		return nil
	}

	// Обработчик объявлен — RLS-хук ОБЯЗАН отработать. Если объект не загрузился,
	// отказываем в доступе (fail-closed), а не отдаём форму без проверки.
	obj, err := s.loadRuntimeObject(ctx, entity, id)
	if err != nil {
		return fmt.Errorf("ПриЧтенииНаСервере: не удалось загрузить объект: %w", err)
	}
	return s.executeFormReadHook(ctx, entity, form, program, decl, obj)
}

// runFormReadHookOnObject исполняет тот же fail-closed read-hook на переданном
// объекте. Это нужно путям, которые уже загрузили и авторизовали точный снимок:
// повторный GetByID между RLS, hook и использованием данных создавал TOCTOU.
func (s *Server) runFormReadHookOnObject(
	ctx context.Context,
	entity *metadata.Entity,
	form *metadata.FormModule,
	obj *runtime.Object,
) error {
	if form == nil || s.interp == nil {
		return nil
	}
	program, decl := formReadHook(form)
	if program == nil || decl == nil {
		return nil
	}
	if obj == nil {
		return fmt.Errorf("ПриЧтенииНаСервере: объект не загружен")
	}
	return s.executeFormReadHook(ctx, entity, form, program, decl, obj)
}

func formReadHook(form *metadata.FormModule) (*ast.Program, *ast.ProcedureDecl) {
	if form == nil {
		return nil, nil
	}
	procName := resolveHandlerProc(form, "", string(metadata.FormEventOnReadAtServer))
	if procName == "" {
		return nil, nil
	}
	program, ok := form.ProgramAST.(*ast.Program)
	if !ok || program == nil {
		return nil, nil
	}
	for _, proc := range program.Procedures {
		if strings.EqualFold(proc.Name.Literal, procName) {
			return program, proc
		}
	}
	return nil, nil
}

func (s *Server) executeFormReadHook(
	ctx context.Context,
	entity *metadata.Entity,
	form *metadata.FormModule,
	program *ast.Program,
	decl *ast.ProcedureDecl,
	obj *runtime.Object,
) error {
	ctx = trustedDSLContext(ctx)

	mc := runtime.NewMovementsCollector(entity.Name, obj.ID)
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, &msgs)
	defer rollbackDSLExecution(txState)
	thisObj := s.newFormObjectThisLive(ctx, txState, obj, entity, form, false)
	vars["Объект"] = thisObj
	vars["ЭтотОбъект"] = thisObj

	formProcs := make(map[string]*ast.ProcedureDecl, len(program.Procedures))
	for _, p := range program.Procedures {
		formProcs[strings.ToLower(p.Name.Literal)] = p
	}
	vars["__form_procs__"] = formProcs

	runErr := s.interp.Run(decl, thisObj, vars)
	return finishDSLExecution(txState, runErr)
}

// runFormWriteHook исполняет серверный обработчик события записи формы
// (ПередЗаписью/ПриЗаписи/ПослеЗаписи) уровня формы, если он объявлен, с
// «Объект»=obj. Мутации Объекта остаются в obj. Возвращает ошибку обработчика
// (для ПередЗаписью/ПриЗаписи — повод отказать в записи). Сообщения копятся в msgs.
//
// Аналог runFormReadHook, но «Объект» передаётся снаружи (а не загружается из
// БД), а тип события — параметр. Используется в save-пути (submit/submitEdit).
func (s *Server) runFormWriteHook(ctx context.Context, entity *metadata.Entity, form *metadata.FormModule, obj *runtime.Object, event metadata.FormEventType, msgs *[]string) error {
	if form == nil || s.interp == nil || obj == nil {
		return nil
	}
	procName := resolveHandlerProc(form, "", string(event))
	if procName == "" {
		return nil
	}
	program, ok := form.ProgramAST.(*ast.Program)
	if !ok || program == nil {
		return nil
	}
	var decl *ast.ProcedureDecl
	for _, p := range program.Procedures {
		if strings.EqualFold(p.Name.Literal, procName) {
			decl = p
			break
		}
	}
	if decl == nil {
		return nil
	}
	ctx = trustedDSLContext(ctx)

	mc := runtime.NewMovementsCollector(entity.Name, obj.ID)
	vars, txState := s.buildDSLVarsWithMessagesTx(ctx, mc, msgs)
	defer rollbackDSLExecution(txState)
	thisObj := s.newFormObjectThisLive(ctx, txState, obj, entity, form, false)
	vars["Объект"] = thisObj
	vars["ЭтотОбъект"] = thisObj

	formProcs := make(map[string]*ast.ProcedureDecl, len(program.Procedures))
	for _, p := range program.Procedures {
		formProcs[strings.ToLower(p.Name.Literal)] = p
	}
	vars["__form_procs__"] = formProcs

	runErr := s.interp.Run(decl, thisObj, vars)
	return finishDSLExecution(txState, runErr)
}

// runPreSaveFormHooks исполняет ПередЗаписью и ПриЗаписи (если объявлены) ДО
// записи. Если ни одно событие не объявлено — no-op (obj не трогается, поведение
// save как раньше). Иначе: обогащаем ссылки шапки (чтобы в хуках работали
// ЗначениеРеквизитаОбъекта и работа со ссылками, как в событиях полей), запускаем
// обработчики, затем сводим регистр ключей полей и возвращаем ссылочные значения
// к UUID-строкам — чтобы Save получил ровно то же, что и без хуков.
//
// Возвращает ошибку обработчика — вызывающий код обязан отказать в записи
// (перерисовать форму с ошибкой), как при DSLError из entityService.Save.
func (s *Server) runPreSaveFormHooks(ctx context.Context, entity *metadata.Entity, obj *runtime.Object, msgs *[]string) error {
	before := pickObjectFormWithHook(entity, string(metadata.FormEventBeforeWrite))
	onWrite := pickObjectFormWithHook(entity, string(metadata.FormEventOnWrite))
	if before == nil && onWrite == nil {
		return nil
	}

	s.enrichHeaderRefs(ctx, entity, obj)

	if before != nil {
		if err := s.runFormWriteHook(ctx, entity, before, obj, metadata.FormEventBeforeWrite, msgs); err != nil {
			return err
		}
	}
	if onWrite != nil {
		if err := s.runFormWriteHook(ctx, entity, onWrite, obj, metadata.FormEventOnWrite, msgs); err != nil {
			return err
		}
	}

	canonicalizeFields(obj, entity)
	derefFields(obj)
	return nil
}

// runAfterWriteFormHook исполняет ПослеЗаписи (если объявлено) ПОСЛЕ успешной
// записи — с перезагруженным из БД Объектом (есть присвоенный номер/ссылки).
// Отменить запись уже нельзя, поэтому ошибка обработчика логируется в msgs, но
// не прерывает поток (запись состоялась).
func (s *Server) runAfterWriteFormHook(ctx context.Context, entity *metadata.Entity, id uuid.UUID, msgs *[]string) {
	form := pickObjectFormWithHook(entity, string(metadata.FormEventAfterWrite))
	if form == nil {
		return
	}
	obj, err := s.loadRuntimeObject(ctx, entity, id)
	if err != nil {
		return
	}
	if err := s.runFormWriteHook(ctx, entity, form, obj, metadata.FormEventAfterWrite, msgs); err != nil && msgs != nil {
		*msgs = append(*msgs, "ПослеЗаписи: "+err.Error())
	}
}

// canonicalizeFields сводит obj.Fields к одному ключу на поле сущности в
// оригинальном регистре. Нужно потому, что formToFields пишет ключи в оригинале
// (PascalCase), а Object.Set (через который хук делает Объект.Поле = …) — в
// нижнем регистре. Без сведения у поля было бы два ключа, и Save мог прочитать
// устаревшее значение, проигнорировав мутацию хука. Не-полевые ключи
// (parent_id, is_folder) не трогаем.
func canonicalizeFields(obj *runtime.Object, entity *metadata.Entity) {
	if obj == nil || entity == nil {
		return
	}
	for _, fd := range entity.Fields {
		low := strings.ToLower(fd.Name)
		if low == fd.Name {
			continue // ключ и так каноничный/нижний — дубля быть не может
		}
		lv, hasLow := obj.Fields[low]
		if _, hasOrig := obj.Fields[fd.Name]; hasLow && hasOrig {
			obj.Fields[fd.Name] = lv // мутация хука (нижний регистр) приоритетна
			delete(obj.Fields, low)
		}
	}
}

// derefFields возвращает ссылочные значения шапки к UUID-строкам перед записью.
// runPreSaveFormHooks обогащает ссылки до *interpreter.Ref ради хуков; здесь
// возвращаем их обратно, чтобы Save получил те же UUID-строки, что и без хуков.
// Assertion сужен именно к *interpreter.Ref (а не к интерфейсу GetRefUUID),
// иначе случайно попавший в шапку *runtime.Object (тоже реализующий GetRefUUID
// → o.ID.String()) молча превратился бы в UUID-строку и исказил поле.
func derefFields(obj *runtime.Object) {
	if obj == nil {
		return
	}
	for k, v := range obj.Fields {
		if rl, ok := v.(*interpreter.Ref); ok {
			obj.Fields[k] = rl.GetRefUUID()
		}
	}
}
