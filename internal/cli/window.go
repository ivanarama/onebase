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
		// Окно живёт до закрытия пользователем и базами не распоряжается. По
		// записи базы (--base) оно запускает её сопутствующие программы.
		baseID, _ := cmd.Flags().GetString("base")
		return launcher.OpenEnterpriseWindow(url, title, baseID)
	},
}

func init() {
	windowCmd.Flags().String("url", "", "адрес страницы")
	windowCmd.Flags().String("title", "", "заголовок окна")
	windowCmd.Flags().String("base", "", "id записи базы в лаунчере (сопутствующие программы)")
	rootCmd.AddCommand(windowCmd)
}
