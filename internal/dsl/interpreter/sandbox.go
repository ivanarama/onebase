package interpreter

import (
	"errors"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
)

var (
	errSandboxTimeout = errors.New("превышено максимальное время выполнения (песочница)")
	errSandboxIters   = errors.New("превышен лимит итераций цикла (песочница)")
)

// SandboxProfile описывает ограничения одного запуска DSL. Deny-семантика:
// нулевое значение = «ничего не запрещено» = поведение по умолчанию (без
// регрессии). RunSandboxed применяет запреты профиля безусловно (см. Vars),
// поэтому, чтобы запретить возможность, явно выставь соответствующий флаг.
type SandboxProfile struct {
	DenyNet      bool          // запретить сеть: HTTP-клиент, email, ИИ-запросы
	DenyFile     bool          // запретить файловые builtins (и чтение в РаспознатьДокумент)
	DenyExec     bool          // запретить команды ОС (ВыполнитьКоманду, план 67) недоверенному коду; secure-by-default обычного режима даёт флаг базы exec.enabled
	MaxWallClock time.Duration // 0 = без лимита времени
	MaxLoopIters int           // 0 = дефолт (maxWhileIter)
}

// RestrictedProfile — строгий профиль для недоверенного кода (ИИ/marketplace):
// запрещены сеть и файлы, заданы лимиты времени и итераций.
func RestrictedProfile() SandboxProfile {
	return SandboxProfile{
		DenyNet:      true,
		DenyFile:     true,
		DenyExec:     true,
		MaxWallClock: 10 * time.Second,
		MaxLoopIters: 1_000_000,
	}
}

// Vars возвращает extraVars, навязывающие запреты возможностей профиля
// (сеть/email/файлы/ИИ). RunSandboxed мержит их ПОСЛЕ обычных переменных
// запуска, чтобы deny-guard'ы перекрыли стандартные функции. Возможности без
// выставленного запрета не трогаются — остаются обычные функции (с глобальным
// предохранителем сети, план 62). Для нулевого профиля карта пуста.
func (p SandboxProfile) Vars() map[string]any {
	m := map[string]any{}
	if p.DenyNet {
		deny := NetGuard(func() error {
			return errors.New("сеть запрещена в этом режиме (песочница)")
		})
		for k, v := range NewHTTPFunctions(deny) {
			m[k] = v
		}
		// nil sender: deny-guard срабатывает (checkNet) раньше обращения к
		// отправителю, поэтому реальный EmailSender здесь не нужен.
		for k, v := range NewEmailFunctions(nil, deny) {
			m[k] = v
		}
	}
	if p.DenyFile {
		deny := FileGuard(func() error {
			return errors.New("файловые операции запрещены в этом режиме (песочница)")
		})
		for k, v := range NewFileFunctions(deny) {
			m[k] = v
		}
	}
	if p.DenyExec {
		// Команды ОС — строго опаснее сети/файлов (RCE), поэтому явно
		// запрещаются недоверенному коду (RestrictedProfile: DenyExec=true).
		// Secure-by-default обычного режима обеспечивает флаг базы exec.enabled
		// и nil-guard→deny в dslvars, а не нулевой профиль песочницы.
		deny := ExecGuard(func() error {
			return errors.New("выполнение команд ОС запрещено в этом режиме (песочница)")
		})
		for k, v := range NewExecFunctions(deny, nil) {
			m[k] = v
		}
	}
	// ИИ-builtin'ы (llm_builtins.go) ходят в сеть (ai.Ask), а РаспознатьДокумент
	// ещё и читает файл с диска ДО сетевого вызова. Они внедряются через
	// dslvars.Build(), но не входят в HTTP/Email/File-наборы выше — поэтому
	// перекрываем их отдельно, иначе они стали бы дырой в границе песочницы.
	if p.DenyNet {
		deny := llmDenyFn("ИИ-запросы запрещены в этом режиме (песочница)")
		for k := range NewLLMFunctions(nil) {
			m[k] = deny
		}
	}
	if p.DenyFile {
		// РаспознатьДокумент/RecognizeDocument читают файл — закрываем и при
		// запрете только файлов (профиль DenyNet=false, DenyFile=true).
		deny := llmDenyFn("файловые операции запрещены в этом режиме (песочница)")
		m["РаспознатьДокумент"] = deny
		m["RecognizeDocument"] = deny
	}
	return m
}

// llmDenyFn — заглушка ИИ-builtin'а, мгновенно запрещающая вызов (userError,
// ловится Попыткой). Песочница перекрывает ею ИИ-функции, которые ходят в сеть
// и читают файлы.
func llmDenyFn(msg string) BuiltinFunc {
	return func(args []any, file string, line int) (any, error) {
		panic(userError{Msg: msg})
	}
}

// applySandboxLimits задаёт один абсолютный wall-clock дедлайн запуска. Таймер
// здесь намеренно не создаётся: большинство CallSandboxed не вызывает паузу,
// а обычные операторы проверяют дешёвый time.Time через checkDeadline.
func applySandboxLimits(e *env, p SandboxProfile) {
	e.ec.maxLoopIters = p.MaxLoopIters
	if p.MaxWallClock > 0 {
		e.ec.deadline = time.Now().Add(p.MaxWallClock)
	}
}

// applySandboxVars навязывает запреты после переменных вызывающего. Штатная
// пауза не хранится в env: при провале к глобальному builtin evalCall применяет
// к ней общий deadline запуска.
func applySandboxVars(e *env, p SandboxProfile) {
	for k, v := range p.Vars() {
		e.setLocal(k, v)
	}
}

// RunSandboxed исполняет процедуру с ресурсными лимитами профиля (wall-clock и
// итерации) и запретами возможностей (сеть/файлы/ИИ). Запреты навязываются
// автоматически — p.Vars() мержится ПОСЛЕ extraVars вызывающего, поэтому
// переоткрыть запрещённую возможность через extraVars нельзя. Возвращаемое
// значение — в result.
func (i *Interpreter) RunSandboxed(proc *ast.ProcedureDecl, this This, p SandboxProfile, result *any, extraVars ...map[string]any) (err error) {
	e := i.startEnv(this)
	if proc != nil {
		e.sourceFile = proc.Name.File
	}
	applySandboxLimits(e, p)
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslStop:
				err = s.err
			case userError:
				err = &DSLError{File: e.ec.curFile, Line: e.ec.curLine, Msg: s.Msg, Err: s.Err}
			case dslReturn:
				if result != nil {
					*result = s.val
				}
			default:
				panic(r)
			}
		}
	}()
	for _, m := range extraVars {
		for k, v := range m {
			e.setLocal(k, v)
		}
	}
	// Запреты профиля навязываем ПОСЛЕДНИМИ: они перекрывают любые extraVars,
	// поэтому вызывающий не может (случайно или намеренно) переоткрыть
	// запрещённую возможность. Раньше Vars() передавался вызывающим вручную —
	// забытый или неверно упорядоченный вызов молча открывал песочницу.
	applySandboxVars(e, p)
	if i.StrictLexicalScope {
		if result != nil {
			*result = i.callEntryProc(proc, e, nil)
		} else {
			i.callEntryProc(proc, e, nil)
		}
		return nil
	}
	i.execBlock(proc.Body, e)
	return nil
}

// CallSandboxed is the argument-passing counterpart of RunSandboxed. It is used
// for HTTP services and manager calls that need wall-clock limits but still
// return a value.
func (i *Interpreter) CallSandboxed(proc *ast.ProcedureDecl, this This, args []any, p SandboxProfile, extraVars ...map[string]any) (result any, err error) {
	e := i.startEnv(this)
	if proc != nil {
		e.sourceFile = proc.Name.File
	}
	applySandboxLimits(e, p)
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslStop:
				err = s.err
			case userError:
				err = &DSLError{File: e.ec.curFile, Line: e.ec.curLine, Msg: s.Msg, Err: s.Err}
			default:
				panic(r)
			}
		}
	}()
	for _, m := range extraVars {
		for k, v := range m {
			e.setLocal(k, v)
		}
	}
	applySandboxVars(e, p)
	result = i.callUserProc(proc, e, args)
	return
}
