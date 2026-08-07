package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// Запись config/app.yaml из форм конфигуратора.
//
// Формы конфигуратора правят по несколько ключей: «Свойства конфигурации» —
// восемь верхнеуровневых, «Настройки бэкапа» — блок backup. Обе собирали файл
// заново из усечённой структуры, отражающей только свою форму, и всё, чего в
// этой структуре нет, при сохранении молча исчезало: email, attachments,
// webhooks, llm, ai, demo, backup (вместе с ключами доступа S3), file_storage,
// db, limits, dsl, russian_post — плюс комментарии и порядок ключей. Ничего при
// этом не падало: следующий запуск просто работал по умолчаниям, и связать это
// с нажатием «Сохранить» было уже трудно (issue #656).
//
// Поэтому правка точечная: разбираем файл в дерево yaml.Node и меняем только
// свои ключи. Приём в пакете уже принят — так же устроены applyReportComposition
// и applyAccountRegFields.

// updateAppYAML разбирает config/app.yaml в дерево узлов, отдаёт корневое
// отображение в edit и кодирует обратно. Всё, чего edit не касается, остаётся
// как было. Пустой или отсутствующий файл — начинаем с чистого отображения;
// битый YAML и не-отображение в корне возвращают ошибку: молча затереть чужой
// файл хуже, чем отказать в сохранении (починить можно на вкладке «Файлы»).
func updateAppYAML(raw []byte, edit func(doc *yaml.Node) error) ([]byte, error) {
	root := yaml.Node{Kind: yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	if len(bytes.TrimSpace(raw)) > 0 {
		var parsed yaml.Node
		if err := yaml.Unmarshal(raw, &parsed); err != nil {
			return nil, err
		}
		if parsed.Kind != yaml.DocumentNode || len(parsed.Content) == 0 ||
			parsed.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("updateAppYAML: ожидалось YAML-отображение в корне config/app.yaml")
		}
		root = parsed
	}
	if err := edit(root.Content[0]); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	// app.yaml пишут руками и держат в git — отступ по умолчанию (4) переколбасил
	// бы все вложенные блоки при первом же сохранении.
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// yamlSubMap возвращает вложенное отображение по ключу, создавая пустое, если
// ключа ещё нет. Ошибка — если ключ занят не отображением.
func yamlSubMap(m *yaml.Node, key string) (*yaml.Node, error) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if m.Content[i+1].Kind != yaml.MappingNode {
				return nil, fmt.Errorf("yamlSubMap: ключ %q в config/app.yaml — не отображение", key)
			}
			return m.Content[i+1], nil
		}
	}
	sub := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, sub)
	return sub, nil
}

// setAppYAMLFields применяет к отображению набор «ключ → значение» по порядку.
func setAppYAMLFields(m *yaml.Node, fields []appYAMLField) error {
	for _, f := range fields {
		if err := setYAMLMapField(m, f.key, f.val); err != nil {
			return err
		}
	}
	return nil
}

type appYAMLField struct {
	key string
	val any
}

// strOrNil превращает пустую строку в нетипизированный nil, чтобы
// setYAMLMapField удалил ключ: пустое поле формы означает «ключа быть не
// должно» — ровно то, что раньше давал omitempty у структуры.
func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
