package bugreport

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ivantit66/onebase/internal/fsmode"
)

// maxAttachmentBytes — предел на один вложенный файл. Журнал базы растёт без
// ротации (см. launcher.baseLogPath), и целиком он в отчёте не нужен: читают
// хвост. Отсекаем на всякий случай, чтобы «Сохранить» не выдал гигабайтный zip.
const maxAttachmentBytes = 2 << 20 // 2 МиБ

// WriteBundle пишет zip с отчётом и вложениями.
//
// md — тело report.md, ровно то, что пользователь видел и правил в предпросмотре.
// files — вложения «имя в архиве → содержимое»; пустые пропускаются.
func WriteBundle(path, md string, files map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), fsmode.Dir); err != nil {
		return fmt.Errorf("создать каталог отчёта: %w", err)
	}
	f, err := os.Create(path) //nolint:gosec // G304: путь выбирает пользователь, который и запускает программу
	if err != nil {
		return fmt.Errorf("создать файл отчёта: %w", err)
	}
	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)
	if err := writeEntry(zw, "report.md", md); err != nil {
		return err
	}
	// Порядок записей фиксируем: обход map случаен, а одинаковый ввод должен
	// давать одинаковый архив (иначе тест на содержимое хрупкий).
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if body == "" {
			continue
		}
		if len(body) > maxAttachmentBytes {
			body = body[len(body)-maxAttachmentBytes:]
		}
		if err := writeEntry(zw, name, body); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("закрыть архив отчёта: %w", err)
	}
	return f.Close()
}

func writeEntry(zw *zip.Writer, name, body string) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("добавить %s в архив: %w", name, err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("записать %s в архив: %w", name, err)
	}
	return nil
}
