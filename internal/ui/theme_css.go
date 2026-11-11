package ui

import (
	"net/http"
	"os"
	"path/filepath"
)

// Внешние стили конфигурации (план 218, минимальный контур).
//
// Конфигурация кладёт файл «ui.css» в корень проекта: он подключается после
// встроенных стилей и переопределяет их, пересборка движка не нужна. Файл
// читается с диска при каждом запросе, поэтому правка стиля видна сразу.
// Если файла нет — страницы остаются ровно на встроенных стилях (head не
// получает ссылку, запросы к /ui/theme.css не возникают).

func (s *Server) themeCSSPath() string {
	return filepath.Join(s.cfg.ConfigLocation, "ui.css")
}

func (s *Server) hasThemeCSS() bool {
	if s.cfg.ConfigLocation == "" {
		return false
	}
	st, err := os.Stat(s.themeCSSPath())
	return err == nil && !st.IsDir()
}

// themeCSS отдаёт файл стилей конфигурации. Отсутствующий файл — пустой
// ответ 200: браузер не показывает ошибку консоли на конфигурациях без тем.
func (s *Server) themeCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	data, err := os.ReadFile(s.themeCSSPath())
	if err != nil {
		w.Write([]byte(""))
		return
	}
	w.Write(data)
}
