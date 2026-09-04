package entityservice

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Применение значений по умолчанию (план 153).
//
// ЕДИНСТВЕННАЯ реализация на все пути создания объекта: форма (ui.Server.form
// при открытии и ui.Server.submit при записи), DSL (Документы.X.Создать()),
// REST (createObject, createObjectV2) и действие ИИ-чата (ui.Server.aiActionRun).
// Отдельная реализация «для формы» дала бы четвёртое расхождение к трём путям
// записи документа — тот же класс дефекта, что #366 (DSL не звал OnUnpost) и
// разбор недели 16.08 (#962).
//
// Формой путей два, а не один, и это не дублирование: GET считает значения для
// показа, но управляемая форма рисует только размещённые элементы, поэтому
// неразмещённый реквизит до POST не доезжает и на записи считается заново
// (#1189). Полный разбор — ui.Server.applyDefaultsToUnsubmittedFields.
//
// Что здесь НЕ делается: чтение персональных настроек пользователя. Набор
// таких настроек, права на них и форма у каждой конфигурации свои, поэтому
// они остаются прикладными — конфигурация читает свой регистр сведений в
// хуке ПриСозданииНового.

// DefaultsOptions — режим применения дефолтов.
type DefaultsOptions struct {
	// FormEntry — объект создаётся для ручного ввода в форме. Включает
	// умолчания формы ввода (см. applyFormEntryDefaults), которые
	// существовали в UI до плана 153 и остаются только там.
	FormEntry bool
}

// ApplyDefaults заполняет незаполненные поля объекта значениями по умолчанию
// из метаданных. Возвращает имена полей, которые заполнил.
//
// Уже заданное значение не перетирается никогда: дефолт — то, с чего человек
// начинает, а не то, чем платформа исправляет введённое.
func (s *Service) ApplyDefaults(ctx context.Context, entity *metadata.Entity, fields map[string]any, opts DefaultsOptions) ([]string, error) {
	if entity == nil || fields == nil {
		return nil, nil
	}
	var filled []string
	for _, f := range entity.Fields {
		spec, ok, err := metadata.ParseDefault(f.Default)
		if err != nil {
			// Разбор уже прошёл валидацию на onebase check; сюда попадаем
			// только если конфигурация загружена в обход загрузчика.
			return filled, fmt.Errorf("%s.%s: %w", entity.Name, f.Name, err)
		}
		if !ok || fieldFilled(fields, f.Name) {
			continue
		}
		value, ok, err := s.defaultValue(ctx, entity, f, spec)
		if err != nil {
			return filled, err
		}
		if !ok {
			continue
		}
		setFieldEqualFold(fields, f.Name, value)
		filled = append(filled, f.Name)
	}
	if opts.FormEntry {
		filled = append(filled, applyFormEntryDefaults(entity, fields)...)
	}
	return filled, nil
}

// applyFormEntryDefaults — умолчания формы ручного ввода: дата документа и
// признак активности. Существовали в UI до плана 153 (жили прямо в
// HTTP-обработчике) и намеренно НЕ распространены на DSL и REST.
//
// Расхождение сохранено сознательно. Программное создание документа без даты
// работает во всех существующих конфигурациях; начни платформа подставлять
// туда текущую дату, изменилось бы поведение уже написанного кода — молча и
// во всех базах сразу. Конфигурации, которой нужна дата и в программном пути,
// достаточно объявить `default: сейчас` — тогда значение появится на всех
// путях создания.
func applyFormEntryDefaults(entity *metadata.Entity, fields map[string]any) []string {
	var filled []string
	if entity.Kind == metadata.KindDocument {
		for _, f := range entity.Fields {
			if f.Type != metadata.FieldTypeDate || fieldFilled(fields, f.Name) {
				continue
			}
			setFieldEqualFold(fields, f.Name, time.Now())
			filled = append(filled, f.Name)
		}
	}
	if entity.Activity != nil && !fieldFilled(fields, entity.Activity.Field) {
		setFieldEqualFold(fields, entity.Activity.Field, true)
		filled = append(filled, entity.Activity.Field)
	}
	return filled
}

// defaultValue вычисляет значение одного дефолта. Второй результат false —
// «значения нет» (константа пуста, единственного элемента не нашлось,
// пользователь неизвестен): поле остаётся пустым, и это не ошибка.
func (s *Service) defaultValue(ctx context.Context, entity *metadata.Entity, f metadata.Field, spec metadata.DefaultSpec) (any, bool, error) {
	switch spec.Kind {
	case metadata.DefaultNow:
		return time.Now(), true, nil
	case metadata.DefaultToday:
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), true, nil
	case metadata.DefaultCurrentUser:
		u := auth.UserFromContext(ctx)
		if u == nil || u.Login == "" {
			// Фоновое задание, обмен, procrun — пользователя нет. Пустое поле
			// честнее выдуманного логина.
			return nil, false, nil
		}
		return u.Login, true, nil
	case metadata.DefaultConstant:
		return s.constantDefault(ctx, f, spec)
	case metadata.DefaultSingle:
		return s.singleRefDefault(ctx, f)
	case metadata.DefaultLiteral:
		return literalDefault(f, spec.Raw)
	}
	return nil, false, nil
}

// constantDefault читает значение константы. Ошибку чтения не глотаем: пустая
// константа и недоступная таблица констант — разные вещи, и вторая означает,
// что база не мигрирована.
func (s *Service) constantDefault(ctx context.Context, f metadata.Field, spec metadata.DefaultSpec) (any, bool, error) {
	if s.Store == nil {
		// Сервер поднят без базы (ui.New(reg, nil, …)) — читать неоткуда.
		return nil, false, nil
	}
	name := spec.Constant
	if s.Reg != nil {
		// Имя в YAML может отличаться регистром от объявления константы;
		// в _constants ключ — имя ровно из объявления, поэтому приводим.
		if c := s.Reg.GetConstantMeta(name); c != nil {
			name = c.Name
		} else {
			for _, c := range s.Reg.Constants() {
				if strings.EqualFold(c.Name, name) {
					name = c.Name
					break
				}
			}
		}
	}
	raw, err := s.Store.GetConstant(ctx, name)
	if err != nil {
		if isNoRowsErr(err) {
			// Константа объявлена, но значения ещё нет (миграция не
			// проставила default, пользователь не заполнил) — поле пустое.
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("default константа.%s: %w", spec.Constant, err)
	}
	value, ok := normalizeConstantValue(f, raw)
	return value, ok, nil
}

// singleRefDefault подставляет единственный доступный элемент справочника.
//
// «Доступный» означает ровно то, что перечислено ниже, и это часть контракта,
// а не деталь реализации: не помечен на удаление, не группа, активен (если у
// сущности объявлен блок activity) и виден текущему пользователю по строковым
// политикам. Последнее означает, что у разных пользователей результат может
// быть разным — поэтому источник и сделан опт-ином на реквизите.
func (s *Service) singleRefDefault(ctx context.Context, f metadata.Field) (any, bool, error) {
	if s.Reg == nil || s.Store == nil || f.RefEntity == "" {
		return nil, false, nil
	}
	ref := s.Reg.GetEntity(f.RefEntity)
	if ref == nil {
		return nil, false, nil
	}
	params := storage.ListParams{
		// Два, а не COUNT: нужен ответ «ровно один или нет», и он получается
		// на второй строке. Открытие нового объекта — самый горячий путь в
		// интерфейсе, полный подсчёт на нём не нужен никому.
		Limit:          2,
		ExcludeMarked:  true,
		ExcludeFolders: ref.Hierarchical,
	}
	if ref.Activity != nil {
		params.ActivityScope = metadata.ActivityScopeActive
	}
	params, err := s.listParamsWithRowAccess(ctx, ref, params)
	if err != nil {
		if errors.Is(err, errNoAccessibleRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	rows, err := s.Store.List(ctx, ref.Name, ref, params)
	if err != nil {
		return nil, false, fmt.Errorf("default единственный (%s): %w", ref.Name, err)
	}
	if len(rows) != 1 {
		return nil, false, nil
	}
	id, ok := rows[0]["id"]
	if !ok || id == nil {
		return nil, false, nil
	}
	return fmt.Sprint(id), true, nil
}

// listParamsWithRowAccess досыпает в параметры списка предикат строкового
// доступа текущего пользователя. Решение считается тем же кодом, что в UI и
// REST (access.DecideWithLookup), поэтому «единственный» не может показать
// элемент, которого пользователь не видит в списке.
//
// Отказ в доступе — не ошибка: элементов, видимых пользователю, ноль, значит
// подставлять нечего.
func (s *Service) listParamsWithRowAccess(ctx context.Context, ref *metadata.Entity, params storage.ListParams) (storage.ListParams, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		// Фоновый контекст без пользователя: политики не применяются, как и
		// в остальных серверных путях.
		params.RowFilterEvaluated = true
		return params, nil
	}
	dec, err := access.DecideWithLookup(u, string(ref.Kind), ref.Name, "read", ref, s.Reg)
	if err != nil {
		return params, fmt.Errorf("default единственный (%s): %w", ref.Name, err)
	}
	if !dec.Allowed {
		return params, errNoAccessibleRows
	}
	if !dec.Unrestricted {
		params.RowFilter = dec.Predicate
	}
	params.RowFilterEvaluated = true // план 79F: строковый доступ вычислен
	return params, nil
}

// errNoAccessibleRows — внутренний признак «читать нечего», а не сбой:
// пользователю не разрешено чтение справочника, значит видимых элементов ноль
// и подставлять нечего. Открытие формы при этом не ломается.
var errNoAccessibleRows = errors.New("default единственный: нет доступных строк")

// literalDefault приводит литерал из YAML к типу реквизита. Проверка уже
// прошла на onebase check (metadata.ValidateDefaults), поэтому нераспознанное
// значение здесь просто пропускается: ронять запись документа из-за дефолта
// нельзя.
func literalDefault(f metadata.Field, raw string) (any, bool, error) {
	if f.EnumName != "" {
		return raw, true, nil
	}
	switch f.Type {
	case metadata.FieldTypeNumber:
		n, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64)
		if err != nil {
			return nil, false, nil
		}
		return n, true, nil
	case metadata.FieldTypeBool:
		b, ok := metadata.ParseBoolLiteral(raw)
		if !ok {
			return nil, false, nil
		}
		return b, true, nil
	case metadata.FieldTypeString:
		return raw, true, nil
	}
	return nil, false, nil
}

// normalizeConstantValue приводит значение константы (оно хранится в JSON) к
// виду, ожидаемому реквизитом. Константы записываются строкой и из формы
// «Константы», и миграцией default, поэтому число и булево приходится
// разбирать из строки.
func normalizeConstantValue(f metadata.Field, raw any) (any, bool) {
	if raw == nil {
		return nil, false
	}
	if s, ok := raw.(string); ok && strings.TrimSpace(s) == "" {
		return nil, false
	}
	if f.RefEntity != "" || f.EnumName != "" || f.Type == metadata.FieldTypeString {
		return fmt.Sprint(raw), true
	}
	switch f.Type {
	case metadata.FieldTypeNumber:
		switch v := raw.(type) {
		case float64:
			return v, true
		case int64:
			return float64(v), true
		case string:
			n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
			if err != nil {
				return nil, false
			}
			return n, true
		}
	case metadata.FieldTypeBool:
		switch v := raw.(type) {
		case bool:
			return v, true
		case string:
			b, ok := metadata.ParseBoolLiteral(v)
			return b, ok
		}
	case metadata.FieldTypeDate:
		if s, ok := raw.(string); ok {
			if t, ok := storage.ParseRegPeriod(s); ok {
				return t, true
			}
		}
	}
	return nil, false
}

// fieldFilled сообщает, задано ли поле. Ключи приходят и в PascalCase (формы),
// и в нижнем регистре (Object.Set), поэтому сравнение регистронезависимое —
// иначе дефолт перетирал бы значение, введённое пользователем.
func fieldFilled(fields map[string]any, name string) bool {
	for k, v := range fields {
		if !strings.EqualFold(k, name) {
			continue
		}
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			if strings.TrimSpace(s) != "" {
				return true
			}
			continue
		}
		return true
	}
	return false
}

// setFieldEqualFold пишет значение в существующий ключ, если он есть в другом
// регистре, — чтобы рядом не оказалось двух ключей одного поля.
func setFieldEqualFold(fields map[string]any, name string, value any) {
	for k := range fields {
		if strings.EqualFold(k, name) {
			fields[k] = value
			return
		}
	}
	fields[name] = value
}

func isNoRowsErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no rows") || strings.Contains(msg, "sql: no rows in result set")
}

// NewObjectRequest — запрос на создание нового (ещё не записанного) объекта.
type NewObjectRequest struct {
	Entity *metadata.Entity
	// FormEntry — объект создаётся для ручного ввода (форма). См.
	// DefaultsOptions.FormEntry.
	FormEntry bool
	// Fields — значения, уже известные вызывающему (тело REST-запроса).
	// Кладутся ДО дефолтов, поэтому дефолт их не перетирает, и видны хуку
	// ПриСозданииНового: для REST «ввод пользователя» приходит целиком
	// сразу, и хук обязан видеть тот же объект, что увидел бы в форме
	// после заполнения.
	Fields map[string]any
	// TablePartRows — строки табличных частей из того же источника.
	TablePartRows map[string][]map[string]any
}

// NewObjectResult — новый объект с применёнными дефолтами и результатом хука.
type NewObjectResult struct {
	Object *runtime.Object
	// Filled — имена реквизитов, заполненных декларативными дефолтами
	// (до хука). Нужен вызывающему, чтобы отличить «платформа подставила» от
	// «пользователь ввёл» в диагностике.
	Filled []string
	// DSLError != "" — хук ПриСозданииНового вернул ошибку. Как и в Fill,
	// это не отказ создания: форма открывается, пользователь видит причину.
	DSLError    string
	DSLMessages []string
}

// NewObject создаёт объект для ввода: применяет декларативные дефолты, затем
// запускает хук ПриСозданииНового(Объект).
//
// Порядок именно такой: хук главнее метаданных, потому что он видит контекст
// (пользователя, настройки, другие реквизиты), а YAML — нет. Ввод на
// основании и копирование отрабатывают ПОСЛЕ и перетирают оба слоя: явный
// источник данных главнее любого умолчания.
func (s *Service) NewObject(ctx context.Context, req NewObjectRequest) (NewObjectResult, error) {
	entity := req.Entity
	if entity == nil {
		return NewObjectResult{}, errBadRequest("entity is nil")
	}
	obj := runtime.NewObject(entity.Name, entity.Kind)
	obj.Presentation = entity.Presentation
	for _, tp := range entity.TableParts {
		obj.TablePartRows[tp.Name] = []map[string]any{}
	}
	for k, v := range req.Fields {
		obj.Fields[k] = v
	}
	for tpName, rows := range req.TablePartRows {
		if rows != nil {
			obj.TablePartRows[tpName] = rows
		}
	}
	filled, err := s.ApplyDefaults(ctx, entity, obj.Fields, DefaultsOptions{FormEntry: req.FormEntry})
	if err != nil {
		return NewObjectResult{Object: obj, Filled: filled}, err
	}

	proc := s.Reg.GetProcedure(entity.Name, "OnCreate")
	if proc == nil {
		return NewObjectResult{Object: obj, Filled: filled}, nil
	}
	hookCtx, cancelHook := s.hookExecutionContext(ctx)
	defer cancelHook()

	var msgs []string
	var vars map[string]any
	var txState *interpreter.TxState
	if s.BuildVars != nil {
		vars, txState = s.BuildVars(hookCtx, runtime.NewMovementsCollector(entity.Name, obj.ID), &msgs)
	} else {
		vars = make(map[string]any)
	}
	defer interpreter.RollbackTxExecution(txState)
	// Имя параметра выбирает пользователь (Объект, ЭтотОбъект, Док…).
	if len(proc.Params) > 0 {
		vars[proc.Params[0].Literal] = obj
	}
	var thisVal interpreter.This = obj
	if s.MakeThis != nil {
		thisVal = s.MakeThis(hookCtx, txState, obj, entity)
	}
	runErr := s.runHook(hookCtx, proc, thisVal, vars)
	if runErr = interpreter.FinishTxExecution(txState, runErr); runErr != nil {
		normalizeTPRowKeys(obj.TablePartRows, entity)
		msg := runErr.Error()
		if dslErr, ok := runErr.(*interpreter.DSLError); ok {
			msg = dslErr.UserMessage()
		}
		return NewObjectResult{Object: obj, Filled: filled, DSLError: msg, DSLMessages: msgs}, nil
	}
	normalizeTPRowKeys(obj.TablePartRows, entity)
	return NewObjectResult{Object: obj, Filled: filled, DSLMessages: msgs}, nil
}
