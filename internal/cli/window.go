package cli

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/spf13/cobra"
)

// windowCmd — служебная команда (план 78, п. 4.2): открывает URL во втором
// нативном окне и блокируется до его закрытия. Лаунчер GUI-сборки запускает
// ею самого себя для изолированных окон Предприятия: переменная окружения
// ONEBASE_WEBVIEW_PROFILE (читается патчем webview.h из third_party/webview_go)
// направляет профиль WebView2 в каталог окна — у каждого окна свой cookie-jar.
var windowCmd = &cobra.Command{
	Use:    "window",
	Short:  "Открыть URL в нативном окне (служебная)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !launcher.NativeIsolatedSupported() {
			return fmt.Errorf("window: нативные окна доступны только в GUI-сборке под Windows (-tags webview)")
		}
		url, _ := cmd.Flags().GetString("url")
		title, _ := cmd.Flags().GetString("title")
		if url == "" {
			return fmt.Errorf("window: требуется --url")
		}
		if title == "" {
			title = "onebase"
		}
		// nil-канал: окно живёт до закрытия пользователем. Координатор закрытия
		// тоже nil — это окно Предприятия, базами оно не распоряжается.
		return launcher.OpenWindow(url, title, nil, nil)
	},
}

func init() {
	windowCmd.Flags().String("url", "", "адрес страницы")
	windowCmd.Flags().String("title", "", "заголовок окна")
	rootCmd.AddCommand(windowCmd)
}
