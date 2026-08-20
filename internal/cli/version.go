package cli

// `onebase version` — какой именно бинарь сейчас исполняется (#1052).
//
// Заявку завёл сценарий: пользователь скачал релиз v0.9.9, набрал `onebase -v`
// и получил v0.9.3. Версия была верной — просто отвечал не скачанный файл, а
// старый бинарь, лежавший в PATH (архив распаковывается в подкаталог, а шага
// `sudo mv .../onebase /usr/local/bin/` в тот раз не было). Понять это по
// строке «onebase version v0.9.3» невозможно: она не говорит, ЧЕЙ это ответ.
//
// Поэтому команда печатает путь исполняемого файла — вопрос «а тот ли бинарь я
// запускаю» отвечается на месте, без переписки. Заодно тут коммит, дата сборки
// и платформа: по ним отличают две сборки одной версии, а `-v` этого не даёт.
//
// Сама команда добавлена ещё и потому, что `onebase version` — первое, что
// набирает человек. Раньше в ответ прилетало «unknown command "version"»:
// команда, которой нет, выглядит как сломанный бинарь.

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/ivantit66/onebase/internal/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Версия платформы, коммит сборки и путь исполняемого файла",
	Long: `Печатает версию платформы и сведения о сборке.

Путь исполняемого файла в выводе — не украшение: если версия не та, которую вы
ждёте, почти всегда отвечает другой бинарь (старый в PATH). Строка сразу
показывает, какой именно.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Текст собирается целиком и пишется одной операцией: у команды есть код
		// возврата, и отказ записи обязан до него доехать, а не теряться в
		// нескольких непроверенных вызовах печати.
		var b strings.Builder
		b.WriteString(fmt.Sprintf("onebase version %s\n", version.String()))
		if c := version.Commit(); c != "" {
			line := "коммит:      " + c
			if d := version.CommitDate(); d != "" {
				line += " от " + d
			}
			if version.Modified() {
				line += " (собран с незакоммиченными правками)"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString(fmt.Sprintf("платформа:   %s/%s, Go %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version()))
		b.WriteString(fmt.Sprintf("исполняется: %s\n", executablePath()))

		_, err := io.WriteString(cmd.OutOrStdout(), b.String())
		return err
	},
}

// executablePath — путь запущенного файла. При отказе возвращаем то, чем
// программу позвали: даже argv[0] полезнее пустой строки, когда выясняют,
// какой из двух бинарей ответил.
func executablePath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	if len(os.Args) > 0 {
		return os.Args[0] + " (полный путь определить не удалось)"
	}
	return "неизвестно"
}
