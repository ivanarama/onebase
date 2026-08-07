package launcher

// Обработчики страницы «Сообщить об ошибке» в лаунчере (план 116).
//
// Лаунчер — единственное место, где отчёт можно собрать полностью: журналы баз
// пишет он (~/.onebase/logs), и он работает, когда база вообще не поднялась.
// Наружу по-прежнему ничего не уходит: пакет ложится на диск, папка
// открывается в проводнике, отправляет файл пользователь.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/bugreport"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

const (
	// reportLogLines — сколько строк журнала базы кладём в пакет.
	reportLogLines = 300
	// reportMaxTextBytes — предел на текст отчёта, присланный из формы.
	reportMaxTextBytes = 1 << 20
)

// openReportDir открывает папку с готовым пакетом. Вынесено в переменную,
// чтобы тесты не запускали проводник: иначе каждый прогон `go test` открывал бы
// на машине разработчика окно.
var openReportDir = OpenPath

func (h *handler) reportProblem(w http.ResponseWriter, r *http.Request) {
	h.renderReportProblem(w, r, reportVM{
		BaseID:    r.URL.Query().Get("base"),
		AttachLog: true, // журналы для того и собираем — по умолчанию включены
	})
}

// reportVM — состояние страницы: и форма, и предпросмотр, и результат.
type reportVM struct {
	Did       string
	Expected  string
	Got       string
	BaseID    string
	AttachLog bool
	Preview   string
	SavedPath string
	Error     string
}

func (h *handler) renderReportProblem(w http.ResponseWriter, r *http.Request, vm reportVM) {
	lang := resolveLang(r)
	bases, err := h.store.List()
	if err != nil && vm.Error == "" {
		vm.Error = tr(lang, "Не удалось прочитать список баз") + ": " + err.Error()
	}
	render(w, r, "page-report-problem", map[string]any{
		"Title":     tr(lang, "onebase — Сообщить об ошибке"),
		"Bases":     bases,
		"Did":       vm.Did,
		"Expected":  vm.Expected,
		"Got":       vm.Got,
		"BaseID":    vm.BaseID,
		"AttachLog": vm.AttachLog,
		"Preview":   vm.Preview,
		"SavedPath": vm.SavedPath,
		"Error":     vm.Error,
		"Contacts":  bugreport.PlatformContacts(h.appSupportContact(r, vm.BaseID)),
	})
}

func (h *handler) reportProblemPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, reportMaxTextBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vm := reportVM{
		Did:       r.FormValue("did"),
		Expected:  r.FormValue("expected"),
		Got:       r.FormValue("got"),
		BaseID:    strings.TrimSpace(r.FormValue("base")),
		AttachLog: r.FormValue("attach_log") == "1",
	}
	vm.Preview = bugreport.Markdown(h.reportInput(r, vm))
	h.renderReportProblem(w, r, vm)
}

// reportInput собирает данные отчёта по состоянию формы.
func (h *handler) reportInput(r *http.Request, vm reportVM) bugreport.Input {
	in := bugreport.Input{
		Did: vm.Did, Expected: vm.Expected, Got: vm.Got,
		Contacts: bugreport.PlatformContacts(h.appSupportContact(r, vm.BaseID)),
		Now:      time.Now(),
	}
	if b, err := h.store.Get(vm.BaseID); err == nil && b != nil {
		in.ConfigSource = b.ConfigSource
		in.DBKind = b.DBType
		var cfg struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		}
		if err := readAppYAML(r.Context(), b, &cfg); err == nil {
			in.AppName, in.AppVersion = cfg.Name, cfg.Version
		}
		if in.AppName == "" {
			in.AppName = b.Name
		}
	}
	// В предпросмотр журнал целиком не тянем: он занял бы весь экран и мешал
	// вычитывать описание. В пакет он попадает отдельными файлами.
	if vm.AttachLog {
		in.LogTail = h.baseLogTail(vm.BaseID)
	}
	return in
}

func (h *handler) reportProblemSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, reportMaxTextBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vm := reportVM{
		BaseID:    strings.TrimSpace(r.FormValue("base")),
		AttachLog: r.FormValue("attach_log") == "1",
	}
	// Сохраняем ровно то, что пользователь видел и правил.
	vm.Preview = r.FormValue("report")
	lang := resolveLang(r)
	if strings.TrimSpace(vm.Preview) == "" {
		vm.Error = tr(lang, "Пустой отчёт")
		h.renderReportProblem(w, r, vm)
		return
	}

	files := map[string]string{}
	if vm.AttachLog {
		if tail := h.baseLogTail(vm.BaseID); tail != "" {
			files["logs/base.log"] = tail
		}
		if tail := bugreport.TailFile(bugreport.StartupLogPath(), bugreport.StartupLogLines, 8<<10); tail != "" {
			files["startup.log"] = bugreport.Redact(tail)
		}
	}

	path, err := reportBundlePath(time.Now())
	if err == nil {
		err = bugreport.WriteBundle(path, vm.Preview, files)
	}
	if err != nil {
		vm.Error = tr(lang, "Не удалось сохранить пакет") + ": " + err.Error()
		h.renderReportProblem(w, r, vm)
		return
	}
	vm.SavedPath = path
	// Показываем результат в проводнике: путь в профиле пользователю ни о чём
	// не говорит, а приложить файл к письму он должен уметь сразу.
	if oerr := openReportDir(filepath.Dir(path)); oerr != nil {
		oblog.Component("launcher.report").Warn("не удалось открыть папку отчёта", "path", path, "err", oerr)
	}
	h.renderReportProblem(w, r, vm)
}

// baseLogTail возвращает отредактированный хвост журнала выбранной базы.
func (h *handler) baseLogTail(baseID string) string {
	if strings.TrimSpace(baseID) == "" {
		return ""
	}
	path, err := baseLogPath(baseID)
	if err != nil {
		return ""
	}
	return bugreport.Redact(bugreport.TailFile(path, reportLogLines, 256<<10))
}

// appSupportContact читает контакт поддержки из app.yaml выбранной базы.
func (h *handler) appSupportContact(r *http.Request, baseID string) string {
	if strings.TrimSpace(baseID) == "" {
		return ""
	}
	b, err := h.store.Get(baseID)
	if err != nil || b == nil {
		return ""
	}
	var cfg struct {
		Support string `yaml:"support"`
	}
	if err := readAppYAML(r.Context(), b, &cfg); err != nil {
		return ""
	}
	return cfg.Support
}

// reportBundlePath возвращает путь для нового пакета в профиле пользователя.
func reportBundlePath(now time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".onebase", "reports", bugreport.FileName(now, "zip")), nil
}
