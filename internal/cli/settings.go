package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/storage"
)

// Предохранители базы из командной строки (issue #709).
//
// Сеть и запуск команд ОС закрыты флагами в _settings, и до сих пор включить их
// можно было только в UI или прямой правкой таблицы. Для headless-окружения —
// CI, автоматический прогон на свежей базе, восстановление из копии — это
// значило лезть в базу мимо движка: `onebase migrate` флаги не создаёт, и любой
// живой сетевой прогон падал, пока кто-нибудь не догадается, какие ключи писать.
// Имена ключей при этом нигде не были описаны.
//
// ПОЧЕМУ БЕЛЫЙ СПИСОК, а не произвольный `settings set <любой ключ>`. В
// _settings лежит не только это: политика аутентификации, ссылки на секреты,
// режим хранения файлов. Команда, пишущая туда что угодно, — это редактор базы
// в обход всех проверок движка, причём с опечатками без предупреждения. Здесь
// разрешены ровно предохранители; остальное правится тем кодом, который знает
// про их инварианты.

// guard — предохранитель базы: как он называется в _settings и как читается.
type guard struct {
	key   string
	title string
	doc   string
	get   func(*storage.DB, context.Context) bool
	set   func(*storage.DB, context.Context, bool) error
}

var guards = []guard{
	{
		key:   "net.enabled",
		title: "Разрешить сетевые операции",
		doc: "исходящие веб-хуки, HTTP-клиент DSL, входящие HTTP-сервисы и отправка почты. " +
			"По умолчанию выключено: восстановленная на другой машине копия не должна " +
			"молча стрелять в боевые системы.",
		get: (*storage.DB).GetNetworkEnabled,
		set: (*storage.DB).SaveNetworkEnabled,
	},
	{
		key:   "exec.enabled",
		title: "Разрешить запуск команд ОС",
		doc: "builtin ВыполнитьКоманду. По умолчанию выключено и отдельно от сети: " +
			"запуск процесса — исполнение произвольного кода на сервере.",
		get: (*storage.DB).GetExecEnabled,
		set: (*storage.DB).SaveExecEnabled,
	},
}

func guardByKey(key string) (guard, bool) {
	for _, g := range guards {
		if strings.EqualFold(g.key, key) {
			return g, true
		}
	}
	return guard{}, false
}

func guardKeyList() string {
	keys := make([]string, 0, len(guards))
	for _, g := range guards {
		keys = append(keys, g.key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Предохранители базы: показать и переключить без UI",
	Long: "Предохранители базы (сеть, запуск команд ОС) хранятся в таблице _settings " +
		"и по умолчанию выключены. Команда даёт их читать и переключать в headless-окружении, " +
		"где UI недоступен.\n\nДоступные ключи: " + guardKeyList(),
}

var settingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Показать предохранители базы и их состояние",
	RunE:  runSettingsList,
}

var settingsSetCmd = &cobra.Command{
	Use:   "set <ключ> <вкл|выкл>",
	Short: "Переключить предохранитель базы",
	Args:  cobra.ExactArgs(2),
	RunE:  runSettingsSet,
}

func init() {
	for _, c := range []*cobra.Command{settingsListCmd, settingsSetCmd} {
		c.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
		c.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	}
	settingsCmd.AddCommand(settingsListCmd, settingsSetCmd)
}

func openSettingsDB(cmd *cobra.Command) (*storage.DB, error) {
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	if sqlitePath != "" {
		return storage.ConnectSQLite(context.Background(), sqlitePath)
	}
	return storage.Connect(context.Background(), dsnFromFlags(cmd))
}

func runSettingsList(cmd *cobra.Command, _ []string) error {
	db, err := openSettingsDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	for _, g := range guards {
		state := "выключено"
		if g.get(db, ctx) {
			state = "включено"
		}
		outf("%-14s %-10s %s\n", g.key, state, g.title)
	}
	return nil
}

func runSettingsSet(cmd *cobra.Command, args []string) error {
	g, ok := guardByKey(args[0])
	if !ok {
		return fmt.Errorf("неизвестный ключ %q; командой переключаются только предохранители: %s",
			args[0], guardKeyList())
	}
	on, err := parseOnOff(args[1])
	if err != nil {
		return err
	}
	db, err := openSettingsDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := g.set(db, context.Background(), on); err != nil {
		return err
	}
	state := "выключено"
	if on {
		state = "включено"
	}
	outf("%s: %s\n", g.key, state)
	return nil
}

// parseOnOff принимает и русские, и английские написания: команду пишут и
// руками, и в CI-скриптах.
func parseOnOff(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "вкл", "включено", "on", "true", "1", "да", "yes":
		return true, nil
	case "выкл", "выключено", "off", "false", "0", "нет", "no":
		return false, nil
	}
	return false, fmt.Errorf("значение %q непонятно: ожидается вкл/выкл (on/off, true/false, 1/0)", s)
}
