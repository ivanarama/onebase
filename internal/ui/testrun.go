package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Режимы изоляции данных между тестами.
const (
	// IsolationTransaction — каждый тест в своей транзакции, откат после
	// (значение по умолчанию). Пишущие тесты не видят записей друг друга и не
	// оставляют следов. Работает на SQLite и PostgreSQL: пути записи DSL
	// (Записать/Провести) присоединяются к открытой транзакции через savepoint.
	IsolationTransaction = "transaction"
	// IsolationNone — без изоляции: записи персистятся и видны следующим тестам.
	// Для чистых юнит-тестов без записи или когда тест сам чистит за собой.
	IsolationNone = "none"
	// IsolationSchema — эфемерная схема на прогон (PostgreSQL). Обрабатывается
	// на уровне подключения до RunTests (см. cli.test), сюда приходит как none.
	IsolationSchema = "schema"
)

// Раннер тестов уровня конфигурации (план 136). Находит тест-обработки
// (kind: test), гоняет каждую процедуру Выполнить() офлайн (как procrun),
// инжектируя объект «Утверждать», и собирает результаты проверок и ошибок.

// TestCaseResult — итог одного тест-процессора.
type TestCaseResult struct {
	Name     string
	Title    string
	Asserts  []interpreter.AssertOutcome
	Passed   int      // сколько проверок прошло
	Failed   int      // сколько проверок провалено
	Err      error    // ошибка выполнения (ВызватьИсключение, unknown function, …)
	Messages []string // вывод Сообщить
	Duration time.Duration
}

// OK — тест успешен: не упал с ошибкой и все проверки прошли. Тест без единой
// проверки считается неуспешным (пустой тест — почти всегда ошибка автора).
func (c TestCaseResult) OK() bool {
	return c.Err == nil && c.Failed == 0 && c.Passed > 0
}

// TestRunResult — итог всего прогона.
type TestRunResult struct {
	Cases []TestCaseResult
}

// OK — весь прогон успешен.
func (r TestRunResult) OK() bool {
	for _, c := range r.Cases {
		if !c.OK() {
			return false
		}
	}
	return true
}

// Totals агрегирует счётчики прогона.
func (r TestRunResult) Totals() (tests, passedTests, asserts, failedAsserts int) {
	for _, c := range r.Cases {
		tests++
		if c.OK() {
			passedTests++
		}
		asserts += c.Passed + c.Failed
		failedAsserts += c.Failed
	}
	return
}

// testRecorder реализует interpreter.AssertRecorder для одного теста.
type testRecorder struct{ outcomes []interpreter.AssertOutcome }

func (t *testRecorder) RecordAssert(o interpreter.AssertOutcome) {
	t.outcomes = append(t.outcomes, o)
}

// accessChecker реализует interpreter.AccessChecker поверх roles/*.yaml проекта и
// метаданных реестра, так что ассерты доступа гоняют те же auth.PermissionHas /
// access.FieldDecisions / access.Decide, что и рантайм, — без запущенного сервера
// и без заведённых пользователей (планы 112, 113).
type accessChecker struct {
	byName map[string]*auth.Role
	reg    *runtime.Registry
}

func newAccessChecker(proj *project.Project, reg *runtime.Registry) (*accessChecker, error) {
	defs, err := auth.LoadRolesYAML(filepath.Join(proj.Dir, "roles"))
	if err != nil {
		return nil, fmt.Errorf("загрузка ролей для тестов: %w", err)
	}
	byName := make(map[string]*auth.Role, len(defs))
	for _, r := range defs {
		byName[strings.ToLower(strings.TrimSpace(r.Name))] = r
	}
	return &accessChecker{byName: byName, reg: reg}, nil
}

func (rc *accessChecker) role(name string) (*auth.Role, error) {
	r, ok := rc.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, fmt.Errorf("роль «%s» не найдена в roles/*.yaml", name)
	}
	return r, nil
}

func (rc *accessChecker) RoleAllows(name, kind, entity, op string) (bool, error) {
	r, err := rc.role(name)
	if err != nil {
		return false, err
	}
	return r.Allows(kind, entity, op)
}

// FieldMask применяет полевое маскирование роли к value (план 88, поверх
// access.FieldDecisions). hasPolicy=true, если поле маскируется/скрывается.
// Требует read на объекте — иначе маскирование неприменимо (loud error).
func (rc *accessChecker) FieldMask(name, kind, entity, field string, value any) (any, bool, error) {
	r, err := rc.role(name)
	if err != nil {
		return nil, false, err
	}
	k := auth.KindFromWord(kind)
	if k == "" {
		return nil, false, fmt.Errorf("неизвестный вид объекта %q", kind)
	}
	meta := rc.reg.GetEntity(entity)
	if meta == nil {
		return nil, false, fmt.Errorf("объект «%s» не найден", entity)
	}
	if !auth.PermissionHas(r.Permissions, k, entity, "read") {
		return nil, false, fmt.Errorf("роль «%s» не имеет права чтения «%s» — маскирование неприменимо", name, entity)
	}
	user := &auth.User{Login: "_test_user", Roles: []*auth.Role{r}}
	for f, d := range access.FieldDecisions(user, k, entity, meta) {
		if strings.EqualFold(f, field) {
			return access.MaskValue(d.Strategy, d.Keep, value), true, nil
		}
	}
	return value, false, nil
}

// RowRestriction сообщает, применяется ли строковый RLS-фильтр (план 79, поверх
// access.DecideWithLookup): "denied" | "unrestricted" | "restricted".
func (rc *accessChecker) RowRestriction(name, kind, entity, op string) (string, error) {
	r, err := rc.role(name)
	if err != nil {
		return "", err
	}
	k := auth.KindFromWord(kind)
	if k == "" {
		return "", fmt.Errorf("неизвестный вид объекта %q", kind)
	}
	meta := rc.reg.GetEntity(entity)
	if meta == nil {
		return "", fmt.Errorf("объект «%s» не найден", entity)
	}
	user := &auth.User{ID: "_test_user_id", Login: "_test_user", Roles: []*auth.Role{r}}
	dec, err := access.DecideWithLookup(user, k, entity, auth.NormalizeOp(op), meta, rc.reg)
	if err != nil {
		return "", err
	}
	switch {
	case !dec.Allowed:
		return "denied", nil
	case dec.Unrestricted:
		return "unrestricted", nil
	default:
		return "restricted", nil
	}
}

// TestRunOptions управляет прогоном тестов.
type TestRunOptions struct {
	Filter    string // маска по имени (регистронезависимая подстрока); пусто — все
	Isolation string // IsolationTransaction (по умолчанию) | IsolationNone
}

// RunTests находит тест-обработки, гоняет каждую и собирает итоги. Ошибка
// возвращается только при сбое окружения (сборка сервера, старт транзакции,
// отсутствие обработки); провалы самих тестов — в TestRunResult.
func RunTests(ctx context.Context, proj *project.Project, db *storage.DB, opts TestRunOptions) (TestRunResult, error) {
	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		return TestRunResult{}, err
	}
	isolation := opts.Isolation
	if isolation == "" {
		isolation = IsolationTransaction
	}

	// Один тест-профиль (часы + моки) на прогон; сбрасывается перед каждым
	// тестом, чтобы Мок.* и замороженное время не протекали между тестами.
	profile := interpreter.NewTestProfile()

	// Резолвер прав для ассертов доступа — грузим roles/*.yaml один раз на прогон
	// (роли между тестами не меняются); метаданные берём из реестра.
	checker, err := newAccessChecker(proj, reg)
	if err != nil {
		return TestRunResult{}, err
	}

	var res TestRunResult
	for _, proc := range selectTests(proj, opts.Filter) {
		c, envErr := runOneTest(ctx, s, reg, db, proc, isolation, profile, checker)
		if envErr != nil {
			return res, envErr
		}
		res.Cases = append(res.Cases, c)
	}
	return res, nil
}

// runOneTest гоняет один тест-процессор, при необходимости оборачивая его в
// транзакцию с откатом (изоляция данных). Возвращает ошибку только при сбое
// окружения; провал/ошибку самого теста несёт TestCaseResult.
func runOneTest(ctx context.Context, s *Server, reg *runtime.Registry, db *storage.DB, proc *processor.Processor, isolation string, profile *interpreter.TestProfile, checker interpreter.AccessChecker) (TestCaseResult, error) {
	rec := &testRecorder{}
	assert := interpreter.NewAssertRoot(rec)
	assert.SetAccessChecker(checker)
	extra := map[string]any{"Утверждать": assert, "Assert": assert}
	// Тест-профиль: часы + моки. Reset — чтобы записи/время предыдущего теста
	// не протекали. Инжектируем последними, поверх стандартных переменных.
	profile.Reset()
	for k, v := range profile.Vars() {
		extra[k] = v
	}

	runCtx := ctx
	var tx storage.Tx
	if isolation == IsolationTransaction {
		var (
			txCtx context.Context
			err   error
		)
		tx, txCtx, err = db.BeginTx(ctx)
		if err != nil {
			return TestCaseResult{}, fmt.Errorf("тест %s: старт транзакции: %w", proc.Name, err)
		}
		runCtx = txCtx
	}

	start := time.Now()
	msgs, runErr, envErr := s.RunProcessor(runCtx, reg, proc.Name, nil, nil, extra)
	dur := time.Since(start)

	// Всегда откатываем: тесты не должны оставлять данных, даже успешные.
	// Откат по txCtx, а не ctx — иначе SQLite не найдёт транзакцию.
	if tx != nil {
		_ = tx.Rollback(runCtx)
	}
	if envErr != nil {
		return TestCaseResult{}, envErr
	}

	c := TestCaseResult{
		Name:     proc.Name,
		Title:    proc.DisplayName(""),
		Asserts:  rec.outcomes,
		Err:      runErr,
		Messages: msgs,
		Duration: dur,
	}
	for _, o := range rec.outcomes {
		if o.Passed {
			c.Passed++
		} else {
			c.Failed++
		}
	}
	return c, nil
}

// selectTests возвращает тест-обработки, отсортированные по имени (для
// детерминированного вывода), с учётом фильтра-подстроки.
func selectTests(proj *project.Project, filter string) []*processor.Processor {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var out []*processor.Processor
	for _, p := range proj.Processors {
		if !p.IsTest() {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(p.Name), filter) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
