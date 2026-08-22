package launcher

// Дозаполнение пустых кодов кнопкой (#1067).
//
// Класс ошибки «уникальность Код включена, но у N записей значение пусто»
// останавливает запуск базы и имеет ровно одно механическое лекарство —
// `onebase renumber --write`. Оно уже названо в тексте ошибки, но пользователь
// лаунчера в консоли не работает: он открывает базу кнопкой, и лечить её
// должен тоже кнопкой, иначе инструкция «выполните команду» отправляет его в
// другой мир и заканчивается звонком внедренцу.
//
// Работу делает дочерний `onebase renumber`, а не код внутри лаунчера:
// нумерация — прикладная операция над схемой базы, и повторять её вторым
// путём значило бы завести вторую реализацию с собственными расхождениями.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
)

// renumberProbeTimeout — сколько ждём подсчёт объёма (без записи). Считается
// он постранично по всем объектам с нумератором, поэтому на большой базе это
// не мгновенно; но и держать неудавшийся запуск дольше минуты незачем —
// кнопка просто не появится.
const renumberProbeTimeout = time.Minute

// renumberWriteTimeout — запись идёт по одной записи с генерацией номера,
// поэтому на большом справочнике это минуты. Прерывать её по таймауту
// запроса нельзя: половина дозаполненных кодов хуже, чем ни одного.
const renumberWriteTimeout = 30 * time.Minute

// RenumberObject — итог по одному объекту в отчёте `onebase renumber --json`.
//
// Тип экспортирован не ради чужих вызовов, а ради проверяемости контракта:
// отчёт пересекает границу процесса, и разъехаться две стороны могут молча.
// Со стороны команды его держит TestRenumberJSONMatchesLauncherContract.
type RenumberObject struct {
	Object string `json:"object"`
	Field  string `json:"field"`
	Empty  int    `json:"empty"`
	Filled int    `json:"filled"`
	// Error — объект пропущен командой: его таблицы ещё нет или в ней нет
	// колонок под объявленные реквизиты. Только этот класс и пропускается —
	// сорвавшаяся работа роняет команду целиком, и тогда сюда не доходит
	// вообще ничего. Кнопке пропуск не мешает: лечится то, что прочиталось,
	// остальное догонит миграция при следующем запуске базы.
	Error string `json:"error,omitempty"`
}

// RenumberReport — отчёт дочерней команды целиком.
type RenumberReport struct {
	Write   bool             `json:"write"`
	Objects []RenumberObject `json:"objects"`
}

// ParseRenumberReport разбирает вывод `onebase renumber --json`.
func ParseRenumberReport(data []byte) (RenumberReport, error) {
	var rep RenumberReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return rep, fmt.Errorf("onebase renumber: неразобранный отчёт: %w", err)
	}
	return rep, nil
}

// Pending — объекты, у которых на момент прогона были пустые значения.
//
// В отчёте разведки (без записи) это и есть «что предстоит дозаполнить»; в
// отчёте о записи empty означает «сколько было пусто» и после лечения не
// обнуляется — состояние базы спрашивают повторной разведкой, а не этим полем.
func (rep RenumberReport) Pending() []RenumberObject {
	var out []RenumberObject
	for _, obj := range rep.Objects {
		if obj.Empty > 0 {
			out = append(out, obj)
		}
	}
	return out
}

// Skipped — объекты, которые команда не смогла прочитать.
func (rep RenumberReport) Skipped() []RenumberObject {
	var out []RenumberObject
	for _, obj := range rep.Objects {
		if obj.Error != "" {
			out = append(out, obj)
		}
	}
	return out
}

// EmptyCount — сколько записей были без значения по всем объектам.
func (rep RenumberReport) EmptyCount() int {
	n := 0
	for _, obj := range rep.Objects {
		n += obj.Empty
	}
	return n
}

// FilledCount — сколько записей команда дозаполнила.
func (rep RenumberReport) FilledCount() int {
	n := 0
	for _, obj := range rep.Objects {
		n += obj.Filled
	}
	return n
}

// startFix — предложение платформы вылечить причину отказа. Уходит в JSON
// рядом с текстом ошибки: окно с причиной само по себе оставляет пользователя
// с кнопкой OK, а здесь ему есть что нажать.
type startFix struct {
	Kind    string           `json:"kind"`
	Empty   int              `json:"empty"`
	Objects []RenumberObject `json:"objects"`
}

const fixKindRenumber = "renumber"

// renumberBase — узкий шов для тестов лаунчера: production запускает дочерний
// процесс, тест подменяет отчёт и не платит за сборку бинаря. Сам дочерний
// путь проверяется со стороны команды (см. ParseRenumberReport).
var renumberBase = (*Runner).RenumberBase

// RenumberBase запускает дочерний `onebase renumber --json`. Без write команда
// ничего не пишет — только считает объём, ровно как в консоли.
func (r *Runner) RenumberBase(ctx context.Context, base *Base, write bool) (RenumberReport, error) {
	var rep RenumberReport
	exe, err := exePath()
	if err != nil {
		return rep, err
	}
	args := append([]string{"renumber", "--json"}, baseTargetArgs(base)...)
	if write {
		args = append(args, "--write")
	}

	cmd := exec.CommandContext(ctx, exe, args...) //nolint:gosec // G204: имя программы фиксировано, аргументы собраны из записи реестра баз на машине пользователя; shell не запускается
	var stderr strings.Builder
	cmd.Stderr = &stderr
	noWindow(cmd)
	// Отчёт читается из stdout отдельно от stderr: журнал платформы идёт во
	// второй поток, и CombinedOutput подмешал бы его в JSON.
	out, err := cmd.Output()
	if err != nil {
		// Причина отказа лежит в stderr дочернего процесса — без неё остаётся
		// «exit status 1», по которому нельзя понять ничего.
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return rep, fmt.Errorf("onebase renumber: %s", msg)
		}
		return rep, fmt.Errorf("onebase renumber: %w", err)
	}
	return ParseRenumberReport(out)
}

// renumberFix проверяет, лечится ли отказ запуска дозаполнением кодов.
//
// Признак берётся из двух источников сразу, и это намеренно. Текст ошибки
// пересёк границу процесса (лаунчер читает хвост лога дочернего `run`), так что
// узнать класс ошибки можно только по нему; но текст говорит лишь о том, ЧТО
// произошло, а объём работ обязан подтвердить сам инструмент. Без первой
// проверки кнопка «дозаполнить коды» появлялась бы на любом отказе запуска, где
// в базе просто есть документы без номера, — то есть предлагала бы лечить не то.
func (h *handler) renumberFix(ctx context.Context, base *Base, startErr error) *startFix {
	if startErr == nil || !strings.Contains(startErr.Error(), storage.RenumberHint) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, renumberProbeTimeout)
	defer cancel()
	rep, err := renumberBase(h.runner, ctx, base, false)
	if err != nil {
		// Не отказ пользователю: причину запуска он уже получил, а сорвавшаяся
		// разведка означает лишь отсутствие кнопки.
		oblog.Component("launcher").Warn("не удалось оценить объём дозаполнения кодов",
			"baseID", base.ID, "err", err)
		return nil
	}
	// Пропуски не отменяют кнопку, но и молчать о них нельзя: если
	// дозаполнение не помогло, в журнале должно быть видно, чего команда не
	// увидела.
	for _, obj := range rep.Skipped() {
		oblog.Component("launcher").Warn("объект пропущен при дозаполнении кодов",
			"baseID", base.ID, "object", obj.Object, "err", obj.Error)
	}
	pending := rep.Pending()
	if len(pending) == 0 {
		return nil
	}
	return &startFix{Kind: fixKindRenumber, Empty: rep.EmptyCount(), Objects: pending}
}

// startFailure отвечает на неудавшийся запуск: причина и, если платформа умеет
// её вылечить, готовое действие.
func (h *handler) startFailure(w http.ResponseWriter, r *http.Request, base *Base, err error) {
	body := map[string]any{"error": errText(r, err)}
	if fix := h.renumberFix(r.Context(), base, err); fix != nil {
		body["fix"] = fix
	}
	writeJSON(w, 500, body)
}

// renumber — обработчик кнопки «Дозаполнить коды». Без ?write=1 только считает
// объём: тот же контракт, что у команды, где запись включается флагом.
func (h *handler) renumber(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	write := r.URL.Query().Get("write") == "1"
	timeout := renumberProbeTimeout
	if write {
		timeout = renumberWriteTimeout
	}

	// Lifecycle gate, а не просто проверка «база не запущена»: между проверкой и
	// запуском дочернего процесса базу мог бы поднять параллельный запрос, и
	// запись пошла бы в файл SQLite под работающим сервером.
	if err := h.runner.holdStarts(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": errText(r, err)})
		return
	}
	defer h.runner.AllowStarts()
	if h.baseRunning(b) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": tr(resolveLang(r), "База запущена — остановите её перед дозаполнением кодов"),
		})
		return
	}

	// Контекст запроса здесь не годится: пользователь мог закрыть окно, а
	// прерывать запись на середине нельзя.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	rep, err := renumberBase(h.runner, ctx, b, write)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": errText(r, err)})
		return
	}
	// skipped едет рядом с filled намеренно: «дозаполнено 0» и «дозаполнено 0,
	// но три объекта пропущены» — разные ответы, и второй объясняет, почему
	// запуск может не поправиться с первого раза.
	writeJSON(w, 200, map[string]any{
		"ok":      true,
		"write":   write,
		"empty":   rep.EmptyCount(),
		"filled":  rep.FilledCount(),
		"objects": rep.Pending(),
		"skipped": rep.Skipped(),
	})
}
