package launcher

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/fsmode"
)

// Сборка ZIP-архивов конфигурации: экспорт конфигурации и полный .obz.
//
// zip.Writer теряет ошибки тихо: Create отдаёт io.Writer, Write — счётчик байт,
// Close дописывает центральный каталог. Пропустив любую из трёх, получаешь
// «успешно» собранный, но неполный или вовсе нечитаемый архив — и пользователь
// сохраняет его как резервную копию. Дефект вскроется при восстановлении, то
// есть ровно тогда, когда копия нужна и проверять уже поздно.
//
// Поэтому здесь принят один контракт: любой сбой прекращает сборку и уходит
// вызывающему. Раньше обе ветки экспорта пропускали сбойный файл (`continue`
// после Scan, `return nil` после ReadFile) и молча игнорировали недоступную БД —
// архив собирался вообще без конфигурации, а пользователь получал HTTP 200.
//
// Пропуск нечитаемого файла в резервной копии хуже отказа: отказ виден сразу,
// а неполная копия выглядит нормальной ровно до попытки восстановиться из неё.

// zipAdd добавляет один файл в архив.
func zipAdd(zw *zip.Writer, name string, content []byte) error {
	f, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("создание записи %q: %w", name, err)
	}
	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("запись %q: %w", name, err)
	}
	return nil
}

// addConfigToZip кладёт в архив конфигурацию базы — из таблицы _onebase_config
// либо из каталога проекта. prefix задаёт папку внутри архива: "" для экспорта
// конфигурации, "config/" для полного .obz.
func addConfigToZip(ctx context.Context, zw *zip.Writer, b *Base, prefix string) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()

		rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config ORDER BY path`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			var content []byte
			if err := rows.Scan(&p, &content); err != nil {
				return err
			}
			if err := zipAdd(zw, prefix+strings.ReplaceAll(p, `\`, "/"), content); err != nil {
				return err
			}
		}
		return rows.Err()
	}

	srcDir := b.Path
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		rel = strings.ReplaceAll(rel, `\`, "/")
		// Каталог резервных копий в копию не кладём — иначе каждая следующая
		// вкладывает предыдущую.
		if strings.HasPrefix(rel, "backups/") {
			return nil
		}
		content, err := os.ReadFile(path) //nolint:gosec // G304,G122: путь пришёл из WalkDir по каталогу самой базы; переход на os.Root — отдельная задача этапа 109H
		if err != nil {
			return err
		}
		return zipAdd(zw, prefix+rel, content)
	})
}

// restoreConfigDir раскладывает файловую конфигурацию из распакованного архива
// в каталог базы.
//
// Каждая ошибка здесь — потерянный файл конфигурации, поэтому ни одна не
// пропускается. Раньше сбой чтения давал `return nil`, а MkdirAll и WriteFile не
// проверялись вовсе: восстановление, при котором не записался НИ ОДИН файл,
// доходило до конца, запускало миграцию и показывало «Полное восстановление
// выполнено: база данных + конфигурация». Правду пользователь узнавал, только
// открыв конфигурацию.
//
// SafeJoin держит запись внутри каталога базы: пути берутся из архива, который
// мог прийти откуда угодно.
func restoreConfigDir(configDir, basePath string) error {
	return filepath.WalkDir(configDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(configDir, path)
		if err != nil {
			return err
		}
		dst, err := configdb.SafeJoin(basePath, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		// Права — общие для всей конфигурации (internal/fsmode): восстановленная
		// конфигурация должна выглядеть точно так же, как созданная обычным путём.
		if err := os.MkdirAll(filepath.Dir(dst), fsmode.Dir); err != nil { //nolint:gosec // G122: путь — из временного каталога, который мы сами и распаковали
			return err
		}
		content, err := os.ReadFile(path) //nolint:gosec // G304,G122: путь — из временного каталога, который мы сами и распаковали
		if err != nil {
			return err
		}
		return os.WriteFile(dst, content, fsmode.File) //nolint:gosec // G703,G122: dst построен configdb.SafeJoin — это и есть guard от traversal
	})
}
