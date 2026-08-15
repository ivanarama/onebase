package cli

import "os"

// noGUI отключает графический интерфейс. Для start это также запрещает
// открывать браузер/native WebView; ошибки всех команд остаются в stderr.
var noGUI bool

func guiDisabled() bool {
	return noGUI || os.Getenv("ONEBASE_NO_GUI") != ""
}

// guiErrorsDisabled сообщает, что модальные окна показывать нельзя: либо явно
// задан флаг --no-gui, либо переменная окружения ONEBASE_NO_GUI. Нужно для
// скриптов и CI, где некому закрыть всплывающее окно (иначе процесс зависает).
func guiErrorsDisabled() bool {
	return guiDisabled()
}
