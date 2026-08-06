package cli

// Команда `onebase support` (план 115) — сбор отчёта об ошибке без интерфейса.
//
// Нужна там, где кнопки нет: база не поднялась, лаунчер не открывается,
// пользователь сидит в консоли по RDP. Ничего никуда не отправляет — кладёт
// zip на диск и печатает, куда его отослать.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/bugreport"
	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/spf13/cobra"
)

// supportLogLines — сколько строк журнала базы кладём в пакет.
const supportLogLines = 300

var supportCmd = &cobra.Command{
	Use:   "support",
	Short: "Собрать отчёт об ошибке одним файлом",
	Long: `Собирает zip с описанием проблемы, версией платформы, окружением и
хвостом журналов. Ничего не отправляет: файл нужно послать в поддержку самому.

Примеры:
  onebase support
  onebase support --message "не проводится реализация" --project .
  onebase support --base 3f7a2c1d --out C:\tmp\report.zip
  onebase support --no-logs`,
	RunE:          runSupport,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	supportCmd.Flags().String("out", "", "куда положить пакет (по умолчанию ~/.onebase/reports/)")
	supportCmd.Flags().String("base", "", "ID базы, журнал которой приложить (по умолчанию — все известные)")
	supportCmd.Flags().String("message", "", "что случилось")
	supportCmd.Flags().String("project", "", "путь к каталогу конфигурации (для имени и контакта поддержки)")
	supportCmd.Flags().Bool("no-logs", false, "не прикладывать журналы")
	rootCmd.AddCommand(supportCmd)
}

func runSupport(cmd *cobra.Command, _ []string) error {
	out, _ := cmd.Flags().GetString("out")
	baseID, _ := cmd.Flags().GetString("base")
	message, _ := cmd.Flags().GetString("message")
	projectDir, _ := cmd.Flags().GetString("project")
	noLogs, _ := cmd.Flags().GetBool("no-logs")

	now := time.Now()
	in := bugreport.Input{Got: message, Now: now}
	appSupport := readAppSupport(projectDir, &in)
	in.Contacts = bugreport.PlatformContacts(appSupport)

	files := map[string]string{}
	if !noLogs {
		if tail := bugreport.TailFile(bugreport.StartupLogPath(), bugreport.StartupLogLines, 8<<10); tail != "" {
			files["startup.log"] = bugreport.Redact(tail)
		}
		for name, tail := range collectBaseLogs(baseID) {
			files[name] = tail
		}
	}

	path, err := supportOutPath(out, now)
	if err != nil {
		return err
	}
	if err := bugreport.WriteBundle(path, bugreport.Markdown(in), files); err != nil {
		return err
	}

	outln("Отчёт собран: " + path)
	if len(files) == 0 && !noLogs {
		outln("Журналы не найдены — приложите свои, если они есть.")
	}
	outln("")
	outln("Проверьте содержимое перед отправкой: в текст ошибок могли попасть данные.")
	printSupportContacts(in.Contacts)
	return nil
}

// readAppSupport подтягивает имя, версию и контакт поддержки из config/app.yaml.
// Каталог не указан или конфигурации там нет — не беда: отчёт соберётся и без
// них, просто без строки «Конфигурация».
func readAppSupport(dir string, in *bugreport.Input) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	cfg, err := project.LoadConfig(dir)
	if err != nil || cfg == nil {
		return ""
	}
	in.AppName = cfg.Name
	in.AppVersion = cfg.Version
	return cfg.Support
}

// maxAutoLogs — сколько журналов берём, когда база не названа. Берём самые
// свежие по времени записи: пользователь редко помнит ID базы, но сломалось у
// него то, с чем он только что работал. Все подряд класть незачем — на десятке
// зарегистрированных баз это сотни килобайт чужой истории.
const maxAutoLogs = 3

// collectBaseLogs возвращает хвосты журналов баз, готовые к вложению.
func collectBaseLogs(id string) map[string]string {
	out := map[string]string{}
	add := func(baseID, label string) {
		path, err := launcher.BaseLogPath(baseID)
		if err != nil {
			return
		}
		if tail := bugreport.TailFile(path, supportLogLines, 256<<10); tail != "" {
			out["logs/"+label+".log"] = bugreport.Redact(tail)
		}
	}
	if strings.TrimSpace(id) != "" {
		add(id, id)
		return out
	}
	for _, b := range recentlyUsedBases(maxAutoLogs) {
		add(b.ID, sanitizeLogLabel(b.Name)+"-"+b.ID)
	}
	return out
}

// recentlyUsedBases возвращает до n баз, чьи журналы писались последними.
func recentlyUsedBases(n int) []*launcher.Base {
	store, err := launcher.NewStore()
	if err != nil {
		return nil
	}
	bases, err := store.List()
	if err != nil {
		return nil
	}
	type dated struct {
		base *launcher.Base
		at   time.Time
	}
	touched := make([]dated, 0, len(bases))
	for _, b := range bases {
		path, err := launcher.BaseLogPath(b.ID)
		if err != nil {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		touched = append(touched, dated{base: b, at: st.ModTime()})
	}
	sort.Slice(touched, func(i, j int) bool { return touched[i].at.After(touched[j].at) })
	if len(touched) > n {
		touched = touched[:n]
	}
	out := make([]*launcher.Base, 0, len(touched))
	for _, d := range touched {
		out = append(out, d.base)
	}
	return out
}

// sanitizeLogLabel делает из имени базы кусок имени файла внутри архива.
func sanitizeLogLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "base"
	}
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			return '_'
		}
		return r
	}
	return strings.Map(repl, name)
}

func supportOutPath(out string, now time.Time) (string, error) {
	if strings.TrimSpace(out) != "" {
		return out, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("определить домашний каталог: %w", err)
	}
	return filepath.Join(home, ".onebase", "reports", bugreport.FileName(now, "zip")), nil
}

func printSupportContacts(c bugreport.Contacts) {
	switch {
	case c.App != "":
		outln("Отправьте файл в поддержку конфигурации: " + c.App)
	case c.Platform != "":
		outln("Отправьте файл разработчику платформы: " + c.Platform)
	case c.IssuesURL != "":
		outln("Отправьте файл в трекер платформы (нужен аккаунт GitHub): " + c.IssuesURL)
	}
}
