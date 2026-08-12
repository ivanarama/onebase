package cli

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/api"
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

	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("неверный адрес --listen %q: %w", listen, err)
	}
	// Пустой host в адресе прослушивания — это все интерфейсы, поэтому он
	// НЕ считается loopback (в отличие от api.IsLoopbackHost, где "" = дефолт
	// 127.0.0.1). Наружу агент печатает на кассовое оборудование по открытому
	// HTTP — bind не на loopback без токена означал бы удалённое управление
	// кассой кем угодно, поэтому запрещаем.
	safeLoopback := host != "" && api.IsLoopbackHost(host)
	if !safeLoopback && token == "" {
		return fmt.Errorf("bind на не-loopback адрес %q требует --token", listen)
	}

	fmt.Printf("onebase device-agent слушает %s (драйверы: %v)\n", listen, equipment.Drivers())
	if token == "" {
		fmt.Println("ВНИМАНИЕ: токен не задан — команды принимаются без аутентификации")
	}

	// Таймауты выставляем здесь (раньше их не было вовсе, вопреки комментарию):
	// без ReadHeaderTimeout медленный клиент держит соединение бесконечно
	// (slowloris), а агент — одноузловой процесс на машине кассира.
	srv := &http.Server{
		Addr:              listen,
		Handler:           deviceagent.New(token).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}
