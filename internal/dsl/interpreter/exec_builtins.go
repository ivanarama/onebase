package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Выполнение команд ОС из DSL (план 67). Это самая мощная возможность —
// запуск произвольного процесса = исполнение кода на сервере. Поэтому ВЫКЛЮЧЕНА
// по умолчанию: guard (ExecGuard) пропускает вызов только при включённом флаге
// базы exec.enabled (как предохранитель сети, план 62), а недоверенный код
// дополнительно отсекается профилем песочницы (DenyExec). Команда и аргументы
// передаются РАЗДЕЛЬНО (exec.CommandContext, без
// shell) — это убирает инъекцию команд. Таймаут обязателен.

// ExecGuard вызывается перед запуском команды. nil-ошибка — запуск разрешён,
// иначе вызов прерывается userError (ловится Попыткой), как checkNet/checkFile.
type ExecGuard func() error

// ExecAudit, если задан, вызывается после запуска для записи в журнал.
type ExecAudit func(command string, args []string, code int)

func checkExec(guard ExecGuard) {
	if guard == nil {
		return
	}
	if err := guard(); err != nil {
		panic(userError{Msg: err.Error()})
	}
}

const (
	execDefaultTimeout = 30 * time.Second
	execMaxTimeout     = 10 * time.Minute
	execOutputCap      = 1 << 20 // 1 МиБ на поток
)

// NewExecFunctions возвращает builtin ВыполнитьКоманду с привязанными guard'ом и
// (необязательным) аудитом. guard=deny используется песочницей для запрета
// (см. SandboxProfile.Vars).
func NewExecFunctions(guard ExecGuard, audit ExecAudit, ctxSources ...CtxSource) map[string]any {
	ctxSource := firstCtxSource(ctxSources)
	run := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		checkExec(guard)
		if len(args) == 0 || args[0] == nil {
			panic(userError{Msg: "ВыполнитьКоманду: не задана команда"})
		}
		name := strings.TrimSpace(fmt.Sprintf("%v", args[0]))
		if name == "" {
			panic(userError{Msg: "ВыполнитьКоманду: пустая команда"})
		}

		// Аргументы — Массив строк (раздельно, без shell). Одиночное значение
		// тоже принимается как единственный аргумент.
		var cmdArgs []string
		if len(args) >= 2 && args[1] != nil {
			if a, ok := args[1].(*Array); ok {
				for _, it := range a.items {
					cmdArgs = append(cmdArgs, fmt.Sprintf("%v", it))
				}
			} else {
				cmdArgs = append(cmdArgs, fmt.Sprintf("%v", args[1]))
			}
		}

		// Таймаут (сек), по умолчанию 30, потолок 10 минут.
		timeout := execDefaultTimeout
		if len(args) >= 3 {
			if f, ok := toFloat(args[2]); ok && f > 0 {
				timeout = time.Duration(f * float64(time.Second))
				if timeout > execMaxTimeout {
					timeout = execMaxTimeout
				}
			}
		}

		workdir := ""
		if len(args) >= 4 && args[3] != nil {
			workdir = strings.TrimSpace(fmt.Sprintf("%v", args[3]))
		}

		baseCtx := contextFromSource(ctxSource)
		checkExecutionContext(baseCtx)
		ctx, cancel := context.WithTimeout(baseCtx, timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, name, cmdArgs...) //nolint:gosec // G204: builtin ВыполнитьКоманду закрыт ExecGuard (настройка GetExecEnabled, по умолчанию выключена), в песочнице guard=deny; аргументы идут слайсом, без шелла
		if workdir != "" {
			cmd.Dir = workdir
		}
		stdout := &capWriter{max: execOutputCap}
		stderr := &capWriter{max: execOutputCap}
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		runErr := cmd.Run()
		executionErr := executionContextError(baseCtx)
		timedOut := ctx.Err() == context.DeadlineExceeded && executionErr == nil

		code := 0
		switch {
		case executionErr != nil || timedOut:
			code = -1
		case runErr != nil:
			if ee, ok := runErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				// Команда не запустилась (нет исполняемого файла и т.п.) —
				// понятная ошибка, ловится Попыткой.
				panic(userError{Msg: "ВыполнитьКоманду: " + runErr.Error()})
			}
		}

		if audit != nil {
			audit(name, cmdArgs, code)
		}
		if executionErr != nil {
			return nil, executionErr
		}

		res := &MapThis{M: map[string]any{
			"КодВозврата":      float64(code),
			"СтандартныйВывод": stdout.String(),
			"ОшибочныйВывод":   stderr.String(),
			"Завершилась":      !timedOut,
			"ReturnCode":       float64(code),
			"StandardOutput":   stdout.String(),
			"ErrorOutput":      stderr.String(),
			"Finished":         !timedOut,
		}}
		return res, nil
	})
	return map[string]any{
		"ВыполнитьКоманду": run,
		"ExecuteCommand":   run,
	}
}

// capWriter накапливает не более max байт вывода, остальное молча отбрасывает —
// чтобы «болтливая» команда не съела память и не заблокировалась на полном пайпе.
type capWriter struct {
	buf bytes.Buffer
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		w.buf.Write(p[:room])
	}
	return len(p), nil
}

func (w *capWriter) String() string { return w.buf.String() }
