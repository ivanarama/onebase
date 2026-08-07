package launcher

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"time"

	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/ivantit66/onebase/internal/version"
)

// Обновление платформы из лаунчера (план 92).
//
// Почему кнопка живёт именно здесь: лаунчер запускает базы дочерними процессами
// ТОГО ЖЕ бинаря (runner.go), поэтому он единственный, кто может корректно их
// остановить, подменить файл и поднять обратно. Процесс базы своим бинарём не
// распоряжается — он может быть системной службой, — поэтому в Предприятии
// показывается только версия.
//
// Загрузка и применение разделены намеренно: скачивание идёт фоном и ничего не
// останавливает, а в опасном окне остаётся лишь подмена файлов и перезапуск.

// updateCheckInterval — как часто лаунчер спрашивает GitHub. На канале build
// сборки выходят по нескольку раз в день, но дёргать пользователя чаще, чем
// раз в несколько часов, незачем — уведомление и так тихое.
const updateCheckInterval = 4 * time.Hour

// updateFirstCheckDelay — пауза перед первой проверкой: старт лаунчера не
// должен ждать сеть (в офлайн-контуре её может не быть вовсе).
const updateFirstCheckDelay = 5 * time.Second

// updatesVM — состояние обновлений для интерфейса.
type updatesVM struct {
	// Enabled — политика разрешает показывать средства обновления.
	Enabled bool
	// NetAllowed — политика разрешает сетевые проверки.
	NetAllowed bool
	// CanWrite — у пользователя есть право заменить бинарь. Ложь на общей
	// установке (Program Files, терминальный сервер): там платформой
	// распоряжается администратор.
	CanWrite bool
	BinDir   string

	Current       string
	Channel       string
	ChannelLocked bool
	Repo          string

	CheckedAt  time.Time
	CheckError string

	LatestTag   string
	LatestNotes string
	LatestURL   string
	LatestAt    time.Time

	// Available — есть что предложить; SameScheme отличает «более новая сборка»
	// от «другой канал предлагает другую версию».
	Available  bool
	SameScheme bool

	StagedTag string
	PrevTag   string

	// RunningCount — сколько баз будет остановлено применением обновления.
	RunningCount int
}

// updatesState собирает состояние без обращения к сети.
func (h *handler) updatesState() updatesVM {
	vm := updatesVM{Current: version.String()}

	binDir, err := selfupdate.BinaryDir()
	if err != nil {
		oblog.Component("launcher").Warn("не определён каталог бинаря", "err", err)
		return vm
	}
	vm.BinDir = binDir

	policy := selfupdate.LoadPolicy(binDir)
	vm.Enabled = policy.UIAllowed()
	vm.NetAllowed = policy.CheckAllowed()
	vm.ChannelLocked = policy.ChannelLocked()
	vm.CanWrite = selfupdate.CanWriteBinaryDir(binDir)

	st, err := selfupdate.LoadState()
	if err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не прочитано", "err", err)
	}
	vm.Channel = string(policy.ChannelOr(st.ChannelOrDefault()))
	vm.Repo = policy.RepoOr("")
	vm.CheckedAt = st.CheckedAt
	vm.CheckError = st.CheckError
	if st.Latest != nil {
		vm.LatestTag = st.Latest.Tag
		vm.LatestNotes = st.Latest.Notes
		vm.LatestURL = st.Latest.URL
		vm.LatestAt = st.Latest.PublishedAt
		vm.Available = st.UpdateAvailable(vm.Current)
		vm.SameScheme = selfupdate.SameScheme(vm.Current, st.Latest.Tag)
	}
	if st.StagedReady() {
		vm.StagedTag = st.Staged.Tag
	}
	if st.Prev != nil {
		vm.PrevTag = st.Prev.Tag
	}
	if h.runner != nil {
		vm.RunningCount = len(h.runner.RunningIDs())
	}
	return vm
}

// ShowBadge сообщает, рисовать ли отметку об обновлении в шапке лаунчера.
func (v updatesVM) ShowBadge() bool { return v.Enabled && v.Available }

// CanApply сообщает, можно ли прямо сейчас применить скачанное обновление.
func (v updatesVM) CanApply() bool {
	return v.Enabled && v.CanWrite && v.StagedTag != "" && v.StagedTag != v.Current
}

func (h *handler) updatesPage(w http.ResponseWriter, r *http.Request) {
	render(w, r, "page-updates", map[string]any{
		"Title": tr(resolveLang(r), "onebase — Обновление платформы"),
		"U":     h.updatesState(),
	})
}

func (h *handler) updatesCheck(w http.ResponseWriter, r *http.Request) {
	vm := h.updatesState()
	if !vm.Enabled || !vm.NetAllowed {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	st, err := selfupdate.Check(r.Context(), selfupdate.Options{
		Repo:    vm.Repo,
		Channel: selfupdate.Channel(vm.Channel),
	})
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"latest":    latestTag(st),
		"available": st.UpdateAvailable(version.String()),
	})
}

func (h *handler) updatesDownload(w http.ResponseWriter, r *http.Request) {
	vm := h.updatesState()
	if !vm.Enabled || !vm.NetAllowed {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	// Скачивание длится минуты — контекст запроса для него не годится: клиент
	// мог закрыть вкладку, а загрузку прерывать незачем.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	rel, err := selfupdate.LatestRelease(ctx, vm.Repo, selfupdate.Channel(vm.Channel))
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	staged, err := selfupdate.Fetch(ctx, rel)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "staged": staged.Tag})
}

func (h *handler) updatesApply(w http.ResponseWriter, r *http.Request) {
	vm := h.updatesState()
	if !vm.Enabled {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	if !vm.CanWrite {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Нет прав на запись в каталог платформы — обратитесь к администратору")})
		return
	}
	st, err := selfupdate.LoadState()
	if err != nil || !st.StagedReady() {
		writeJSON(w, 409, map[string]any{"error": tr(resolveLang(r), "Обновление не скачано")})
		return
	}

	// Снимок запущенных баз сохраняем ДО остановки: после перезапуска их
	// поднимет уже новый процесс (ResumeAfterUpdate).
	st.RestartBases = h.runner.RunningIDs()
	if err := selfupdate.SaveState(st); err != nil {
		oblog.Component("launcher").Warn("список баз для восстановления не сохранён", "err", err)
	}
	h.stopAllForUpdate()

	if err := selfupdate.Apply(*st.Staged, vm.BinDir); err != nil {
		// Apply возвращает бинари на место сам — базы можно поднять обратно.
		h.resumeBases(st.RestartBases)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	st.Prev = &selfupdate.RelInfo{Tag: vm.Current}
	st.Staged = nil
	if err := selfupdate.SaveState(st); err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не сохранено", "err", err)
	}

	writeJSON(w, 200, map[string]any{"ok": true, "restart": true})
	h.restartAfterResponse()
}

func (h *handler) updatesRollback(w http.ResponseWriter, r *http.Request) {
	vm := h.updatesState()
	if !vm.Enabled || !vm.CanWrite {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	st, _ := selfupdate.LoadState()
	st.RestartBases = h.runner.RunningIDs()
	if err := selfupdate.SaveState(st); err != nil {
		oblog.Component("launcher").Warn("список баз для восстановления не сохранён", "err", err)
	}
	h.stopAllForUpdate()

	if err := selfupdate.RollbackPrev(vm.BinDir); err != nil {
		h.resumeBases(st.RestartBases)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	st.Prev = nil
	if err := selfupdate.SaveState(st); err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не сохранено", "err", err)
	}

	writeJSON(w, 200, map[string]any{"ok": true, "restart": true})
	h.restartAfterResponse()
}

func (h *handler) updatesChannel(w http.ResponseWriter, r *http.Request) {
	vm := h.updatesState()
	if !vm.Enabled {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Обновление платформы запрещено политикой")})
		return
	}
	if vm.ChannelLocked {
		writeJSON(w, 403, map[string]any{"error": tr(resolveLang(r), "Канал обновлений задан администратором")})
		return
	}
	ch := selfupdate.Channel(r.URL.Query().Get("value"))
	if ch != selfupdate.ChannelBuild && ch != selfupdate.ChannelStable {
		writeJSON(w, 400, map[string]any{"error": "unknown channel"})
		return
	}
	st, _ := selfupdate.LoadState()
	st.Channel = ch
	// Скачанное принадлежало прежнему каналу — предлагать его больше нельзя.
	st.Staged = nil
	st.Latest = nil
	if err := selfupdate.SaveState(st); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "channel": string(ch)})
}

// stopAllForUpdate останавливает базы этого лаунчера вместе с «усыновлёнными»
// (запущенными прежним экземпляром): бинарь нельзя подменить, пока его держит
// хоть один процесс.
func (h *handler) stopAllForUpdate() {
	var ports []int
	if bases, err := h.store.List(); err == nil {
		for _, b := range bases {
			ports = append(ports, b.Port)
		}
	}
	h.runner.StopAll(ports)
}

func (h *handler) resumeBases(ids []string) {
	for _, id := range ids {
		base, err := h.store.Get(id)
		if err != nil {
			continue
		}
		if err := h.runner.Start(base); err != nil {
			oblog.Component("launcher").Warn("база не поднялась после обновления", "base", base.Name, "err", err)
		}
	}
}

// restartAfterResponse запускает новый процесс из подменённого бинаря и просит
// текущий закрыться. Пауза даёт HTTP-ответу дойти до страницы, которая покажет
// «перезапуск» до того, как окно закроется.
func (h *handler) restartAfterResponse() {
	go func() {
		time.Sleep(700 * time.Millisecond)
		if err := RestartSelf(); err != nil {
			oblog.Component("launcher").Error("не удалось перезапустить лаунчер после обновления", "err", err)
			// Без перезапуска пользователь останется в старом окне; базы уже
			// остановлены, поэтому поднимаем их обратно, чтобы не оставить
			// систему в полурабочем состоянии.
			if st, err := selfupdate.LoadState(); err == nil {
				h.resumeBases(st.RestartBases)
			}
			return
		}
		if h.quitFn != nil {
			h.quitFn()
		}
	}()
}

// RestartSelf запускает новый экземпляр onebase с теми же аргументами. После
// подмены файла по этому пути лежит уже новая версия, поэтому «перезапустить
// себя» и «запустить обновлённую платформу» — одно и то же действие.
func RestartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...) //nolint:gosec // G204: путь — собственный исполняемый файл, аргументы — свои же
	cmd.Env = os.Environ()
	noWindow(cmd)
	return cmd.Start()
}

// ResumeAfterUpdate поднимает базы, работавшие до перезапуска ради обновления.
// Вызывается при старте лаунчера, до открытия окна.
func ResumeAfterUpdate(store *Store, runner *Runner) {
	st, err := selfupdate.LoadState()
	if err != nil || len(st.RestartBases) == 0 {
		return
	}
	ids := st.RestartBases
	// Список очищаем ДО попытки запуска: база, которая не поднимается, иначе
	// заставляла бы каждый следующий старт лаунчера снова её дёргать.
	st.RestartBases = nil
	if err := selfupdate.SaveState(st); err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не сохранено", "err", err)
	}
	for _, id := range ids {
		base, err := store.Get(id)
		if err != nil {
			continue
		}
		if err := runner.Start(base); err != nil {
			oblog.Component("launcher").Warn("база не поднялась после обновления", "base", base.Name, "err", err)
		}
	}
}

// ApplyStagedOnStart применяет скачанное обновление в самый безопасный момент —
// на старте, когда баз ещё нет и останавливать нечего. Работает только при
// включённом auto_apply: на канале build молча менять платформу нельзя.
// Возвращает true, если бинарь заменён и процесс нужно перезапустить.
func ApplyStagedOnStart() bool {
	st, err := selfupdate.LoadState()
	if err != nil || !st.AutoApply || !st.StagedReady() {
		return false
	}
	if st.Staged.Tag == version.String() {
		// Уже работаем на этой версии — просто прибираем.
		st.Staged = nil
		if err := selfupdate.SaveState(st); err != nil {
			oblog.Component("launcher").Warn("состояние обновлений не сохранено", "err", err)
		}
		return false
	}
	binDir, err := selfupdate.BinaryDir()
	if err != nil {
		return false
	}
	if !selfupdate.LoadPolicy(binDir).UIAllowed() || !selfupdate.CanWriteBinaryDir(binDir) {
		return false
	}
	if err := selfupdate.Apply(*st.Staged, binDir); err != nil {
		oblog.Component("launcher").Warn("обновление не применено при старте", "err", err)
		return false
	}
	st.Prev = &selfupdate.RelInfo{Tag: version.String()}
	st.Staged = nil
	if err := selfupdate.SaveState(st); err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не сохранено", "err", err)
	}
	return true
}

// startUpdateWatcher включает тихую фоновую проверку обновлений.
func (h *handler) startUpdateWatcher() {
	go func() {
		time.Sleep(updateFirstCheckDelay)
		for {
			h.checkUpdatesQuiet()
			time.Sleep(updateCheckInterval)
		}
	}()
}

// checkUpdatesQuiet спрашивает GitHub и молча складывает ответ в состояние.
// Ошибка сети здесь — нормальный исход (офлайн-машина, прокси), в интерфейс она
// попадёт строкой «проверить не удалось», а не всплывающей ошибкой.
func (h *handler) checkUpdatesQuiet() {
	binDir, err := selfupdate.BinaryDir()
	if err != nil {
		return
	}
	policy := selfupdate.LoadPolicy(binDir)
	if !policy.UIAllowed() || !policy.CheckAllowed() {
		return
	}
	// Сборка без -ldflags — локальная, разработчика: обновлять её всё равно
	// нельзя (Newer отказывает на dev-*), а лишний исходящий запрос из
	// dev-окружения не нужен.
	if version.Build == "" {
		return
	}
	st, err := selfupdate.LoadState()
	if err != nil {
		oblog.Component("launcher").Warn("состояние обновлений не прочитано", "err", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := selfupdate.Check(ctx, selfupdate.Options{
		Repo:    policy.RepoOr(""),
		Channel: policy.ChannelOr(st.ChannelOrDefault()),
	}); err != nil {
		oblog.Component("launcher").Debug("проверка обновлений не удалась", "err", err)
	}
}

func latestTag(st selfupdate.State) string {
	if st.Latest == nil {
		return ""
	}
	return st.Latest.Tag
}
