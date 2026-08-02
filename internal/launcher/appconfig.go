package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Чтение config/app.yaml базы.
//
// Этот блок был продублирован пятью копиями (список баз, страница входа, панель
// «О программе», отдача логотипа, каталог резервных копий) — и во всех пяти
// ошибка разбора игнорировалась одинаково. Последствие мягкое, но неприятное
// именно тем, что уводит в сторону: при битом app.yaml структура остаётся
// нулевой, и интерфейс показывает не «конфигурация не читается», а «имя не
// задано, логотипа нет». Пользователь ищет несуществующую проблему в настройках.
//
// Отдельный случай — каталог резервных копий: пустое значение там означает
// «класть в каталог по умолчанию», то есть битый app.yaml молча меняет место
// хранения копий.
//
// Поведение вызывающих сохранено: это пути только для показа, и ронять список
// баз из-за одной сломанной конфигурации было бы хуже. Изменилось то, что
// причина теперь попадает в журнал, а не исчезает.

// errAppConfigAbsent — конфигурации нет: не задан путь, нет записи или файла.
// Это штатная ситуация (база ещё не наполнена), в отличие от битого YAML.
var errAppConfigAbsent = errors.New("config/app.yaml отсутствует")

const appConfigPath = "config/app.yaml"

// readAppYAML разбирает config/app.yaml базы в v — из таблицы _onebase_config
// либо из каталога проекта.
func readAppYAML(ctx context.Context, b *Base, v any) error {
	var content []byte

	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := db.QueryRow(ctx,
			`SELECT content FROM _onebase_config WHERE path = $1`, appConfigPath).Scan(&content); err != nil {
			return errors.Join(errAppConfigAbsent, err)
		}
	} else {
		if b.Path == "" {
			return errAppConfigAbsent
		}
		data, err := os.ReadFile(filepath.Join(b.Path, "config", "app.yaml"))
		if err != nil {
			return errors.Join(errAppConfigAbsent, err)
		}
		content = data
	}

	if err := yaml.Unmarshal(content, v); err != nil {
		// Логируем здесь, а не у вызывающих: именно эта ошибка раньше пропадала
		// молча, и во всех пяти местах реакция на неё одинаковая — показать
		// пустые значения. Дублировать Warn по местам вызова незачем.
		respondLog().Warn("config/app.yaml не разобран — значения показаны пустыми",
			"base", b.Name, "baseID", b.ID, "err", err)
		return err
	}
	return nil
}
