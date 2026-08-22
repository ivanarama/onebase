package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/project"
)

// Конфигуратор пишет файл раньше, чем узнаёт, что получилось. Проверка перед
// записью есть, но смотрит она на одну редактируемую форму (validateEntityFieldEdit
// собирает объект из полей формы и проверяет идентификаторы) — согласованность
// объекта с остальной конфигурацией ей не видна. Поэтому правка, безупречная
// сама по себе, могла оставить конфигурацию незагружаемой: тип реквизита
// разошёлся с блоком tile_view, и база перестала запускаться, а Конфигуратор
// при следующем открытии показал пустое дерево (#1090).
//
// Полагаться на полноту проверок формы нельзя: связей между объектами много,
// каждая новая — ещё один способ собрать нерабочую конфигурацию через UI.
// Поэтому гейт стоит не на списке правил, а на факте: после записи
// конфигурация обязана загружаться. Не загрузилась — правка откатывается.
//
// Условие «а до правки загружалась» обязательно. Без него пользователь с уже
// сломанной конфигурацией оказался бы заперт: любое сохранение отклонялось бы
// из-за чужой ошибки, и починить её через Конфигуратор стало бы невозможно.

// configSnapshot — содержимое YAML-файлов конфигурации до правки. Хранится
// целиком, а не как список путей: откат обязан вернуть прежние байты, а не
// пересобрать их заново из формы.
type configSnapshot struct {
	files map[string][]byte
}

// snapshotConfig снимает содержимое конфигурации базы. Источник (файлы или БД)
// прячет listConfiguratorFiles; в файловом режиме он читает содержимое только
// у *.yaml/*.yml — ровно те файлы, которые правят редакторы объектов.
func (h *handler) snapshotConfig(ctx context.Context, b *Base) (*configSnapshot, error) {
	files, err := h.listConfiguratorFiles(ctx, b)
	if err != nil {
		return nil, err
	}
	snap := &configSnapshot{files: make(map[string][]byte, len(files))}
	for _, f := range files {
		if f.Content == nil {
			continue
		}
		snap.files[f.Path] = append([]byte(nil), f.Content...)
	}
	return snap, nil
}

// restore возвращает конфигурацию к снятому состоянию: изменённым файлам
// возвращает прежнее содержимое, появившиеся удаляет. Файлы, которых снимок не
// видел (не-YAML в файловом режиме), не трогает — их редакторы объектов и не
// пишут.
func (h *handler) restore(ctx context.Context, b *Base, snap *configSnapshot) error {
	current, err := h.listConfiguratorFiles(ctx, b)
	if err != nil {
		return err
	}
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		for _, f := range current {
			if f.Content == nil {
				continue
			}
			was, existed := snap.files[f.Path]
			switch {
			case !existed:
				if err := repo.DeleteFile(ctx, f.Path); err != nil {
					return err
				}
			case string(was) != string(f.Content):
				if err := cfgUpsert(ctx, db, f.Path, was); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, f := range current {
		if f.Content == nil {
			continue
		}
		full := filepath.Join(b.Path, filepath.FromSlash(f.Path))
		was, existed := snap.files[f.Path]
		switch {
		case !existed:
			if err := os.Remove(full); err != nil {
				return err
			}
		case string(was) != string(f.Content):
			if err := os.WriteFile(full, was, fsmode.File); err != nil { //nolint:gosec // G703: путь получен обходом каталога проекта в listConfiguratorFiles, из запроса он не приходит
				return err
			}
		}
	}
	return nil
}

// configLoadError возвращает ошибку загрузки конфигурации базы или nil.
// Это тот же путь, которым конфигурацию читает запуск базы, — гейт обязан
// спрашивать ровно у него, иначе он проверяет не то, на что жалуется
// пользователь.
func (h *handler) configLoadError(ctx context.Context, b *Base) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			// Недоступная БД — не приговор правке: гейт молчит и пропускает.
			return nil
		}
		defer db.Close()
		repo := configdb.New(db)
		if empty, err := repo.IsEmpty(ctx); err != nil || empty {
			return nil
		}
		p, err := project.LoadFromDB(ctx, repo)
		if err != nil {
			return err
		}
		p.Close()
		return nil
	}
	p, err := project.Load(b.Path)
	if err != nil {
		return err
	}
	p.Close()
	return nil
}

// configRejectedError — правка записана, признана негодной и откачена.
type configRejectedError struct {
	cause      error // почему конфигурация перестала загружаться
	restoreErr error // непусто, если откатить не удалось
}

func (e *configRejectedError) Error() string {
	if e.restoreErr != nil {
		return fmt.Sprintf("%v (откат не удался: %v)", e.cause, e.restoreErr)
	}
	return e.cause.Error()
}

func (e *configRejectedError) Unwrap() error { return e.cause }

// cfgSaveErrorText — текст ошибки сохранения для формы конфигуратора.
// Отклонённая правка получает собственную формулировку: «сохранить не вышло» и
// «сохранили, проверили и вернули как было» — разные события, и путать их
// нельзя. Во втором случае пользователю нужно понять, что его файл цел.
func cfgSaveErrorText(lang string, err error) string {
	var rejected *configRejectedError
	if errors.As(err, &rejected) {
		if rejected.restoreErr != nil {
			return tr(lang, "Правка не применена: с ней конфигурация не загружается, а откат не удался") + ": " + err.Error()
		}
		return tr(lang, "Правка отклонена и откачена: с ней конфигурация перестала бы загружаться") + ": " + rejected.cause.Error()
	}
	return tr(lang, "Ошибка сохранения") + ": " + err.Error()
}

// guardConfigLoadable выполняет правку конфигурации mutate и откатывает её,
// если после неё конфигурация перестала загружаться, а до неё загружалась.
//
// Возвращает ошибку самой правки как есть, а отклонение — обёрнутым в
// *configRejectedError: вызывающему надо развести «сохранить не вышло» и
// «сохранили и передумали», это разные сообщения пользователю.
//
// Если снимок снять не удалось, гейт отходит в сторону и просто выполняет
// правку: откатывать нечем, а запрещать работу из-за нечитаемого каталога
// хуже, чем пропустить.
//
// Цена — два лишних чтения конфигурации на сохранение: около 25 мс каждое на
// examples/trade (168 файлов), то есть порядка 60 мс на нажатие кнопки.
// Сохранение и без того перечитывает конфигурацию, чтобы перерисовать дерево,
// так что порядок величины прежний. Проверку «загружалась ли до правки» можно
// было бы отложить до провала (снять второй снимок, откатиться, сравнить и при
// нужде вернуть правку обратно) — но это выигрывает те же 25 мс ценой ветки,
// которая пишет файлы дважды. Пока сохранение остаётся действием по кнопке,
// такой размен невыгоден.
func (h *handler) guardConfigLoadable(ctx context.Context, b *Base, mutate func() error) error {
	snap, snapErr := h.snapshotConfig(ctx, b)
	loadableBefore := snapErr == nil && h.configLoadError(ctx, b) == nil

	if err := mutate(); err != nil {
		return err
	}
	if !loadableBefore {
		return nil
	}
	loadErr := h.configLoadError(ctx, b)
	if loadErr == nil {
		return nil
	}
	return &configRejectedError{cause: loadErr, restoreErr: h.restore(ctx, b, snap)}
}
