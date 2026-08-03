package cli

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/deviceagent"
	"github.com/ivantit66/onebase/internal/equipment"
)

var deviceAgentCmd = &cobra.Command{
	Use:   "device-agent",
	Short: "Запустить локальный агент подключаемого оборудования (для рабочего места кассира)",
	Long: "Поднимает HTTP-агент на localhost машины кассира. Сервер или браузер РМК\n" +
		"шлёт ему JSON-команды (/print, /drawer), а агент печатает на подключённое\n" +
		"оборудование через драйверы onebase. Команды защищаются токеном X-Agent-Token.",
	RunE: runDeviceAgent,
}

func init() {
	deviceAgentCmd.Flags().String("listen", "127.0.0.1:8765", "адрес прослушивания агента")
	deviceAgentCmd.Flags().String("token", "", "общий токен (заголовок X-Agent-Token); пусто — без проверки")
}

func runDeviceAgent(cmd *cobra.Command, _ []string) error {
	listen, _ := cmd.Flags().GetString("listen")
	token, _ := cmd.Flags().GetString("token")

	fmt.Printf("onebase device-agent слушает %s (драйверы: %v)\n", listen, equipment.Drivers())
	if token == "" {
		fmt.Println("ВНИМАНИЕ: токен не задан — команды принимаются без аутентификации")
	}

	srv := &http.Server{Addr: listen, Handler: deviceagent.New(token).Handler()} //nolint:gosec // G112: таймауты HTTP-сервера выставляются вызывающим, см. настройку сервера
	return srv.ListenAndServe()
}
