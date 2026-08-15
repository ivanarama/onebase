package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
// updateYAMLMapping точечно правит YAML-документ, сохраняя всё, чего правка не
// касается: неизвестные ключи, комментарии, порядок и форматирование.
//
// docName участвует только в тексте ошибки — функция общая для нескольких
// файлов конфигурации (config/app.yaml, subsystems/*.yaml), и сообщение
// «ожидалось YAML-отображение в config/app.yaml» при сохранении подсистемы
// отправляло бы искать проблему не в том файле.
func updateYAMLMapping(raw []byte, docName string, edit func(doc *yaml.Node) error) ([]byte, error) {
	root := yaml.Node{Kind: yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	if len(bytes.TrimSpace(raw)) > 0 {
		var parsed yaml.Node
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&parsed); err != nil {
			return nil, err
		}
		var extra yaml.Node
		if err := dec.Decode(&extra); err != io.EOF {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%s: ожидался один YAML-документ", docName)
		}
		if parsed.Kind != yaml.DocumentNode || len(parsed.Content) == 0 ||
			parsed.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: ожидалось YAML-отображение в корне", docName)
		}
		if err := validateYAMLTree(parsed.Content[0], docName); err != nil {
			return nil, err
		}
		root = parsed
	}
	if err := edit(root.Content[0]); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	// Конфигурацию пишут руками и держат в git — отступ по умолчанию (4)
	// переколбасил бы все вложенные блоки при первом же сохранении.
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	// yaml.Encoder trusts Alias.Value and can emit an alias whose anchor was
	// removed by edit (for example when deleting a mapping value that contained
	// a nested anchor). Reparse the result before it can replace the user's
	// configuration. A failed edit must be fail-closed and leave the source
	// untouched.
	out := buf.Bytes()
	var verified yaml.Node
	if err := yaml.Unmarshal(out, &verified); err != nil {
		return nil, fmt.Errorf("%s: результат YAML-правки некорректен: %w", docName, err)
	}
	return out, nil
}

// validateYAMLTree отклоняет неоднозначные mapping-узлы до правки. yaml.Node
// намеренно умеет представить дубли ключей, но обычная загрузка конфигурации в
// struct затем отвергнет такой файл. Сохранить форму с кодом 200 и оставить
// подсистему нечитаемой хуже, чем показать ошибку и не тронуть исходник.
func validateYAMLTree(n *yaml.Node, docName string) error {
	if n == nil {
		return fmt.Errorf("%s: пустой YAML-узел", docName)
	}
	switch n.Kind {
	case yaml.MappingNode:
		if len(n.Content)%2 != 0 {
			return fmt.Errorf("%s: нечётное число узлов в YAML-отображении", docName)
		}
		seen := make(map[string]struct{}, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Kind == yaml.ScalarNode {
				identity := key.Tag + "\x00" + key.Value
				if _, ok := seen[identity]; ok {
					return fmt.Errorf("%s: повторяющийся YAML-ключ %q", docName, key.Value)
				}
				seen[identity] = struct{}{}
			}
			if err := validateYAMLTree(key, docName); err != nil {
				return err
			}
			if err := validateYAMLTree(val, docName); err != nil {
				return err
			}
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, child := range n.Content {
			if err := validateYAMLTree(child, docName); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		if n.Alias == nil {
			return fmt.Errorf("%s: YAML-псевдоним %q без цели", docName, n.Value)
		}
	}
	return nil
}

// updateAppYAML — прежнее имя для config/app.yaml.
func updateAppYAML(raw []byte, edit func(doc *yaml.Node) error) ([]byte, error) {
	return updateYAMLMapping(raw, "config/app.yaml", edit)
}

// yamlSubMap возвращает вложенное отображение по ключу, создавая пустое, если
// ключа ещё нет. Ошибка — если ключ занят не отображением.
func yamlSubMap(m *yaml.Node, key string) (*yaml.Node, error) {
	mapping, err := resolveYAMLAlias(m)
	if err != nil {
		return nil, err
	}
	value, _, err := yamlMapField(m, key)
	if err != nil {
		return nil, err
	}
	if value != nil {
		resolved, err := resolveYAMLAlias(value)
		if err != nil {
			return nil, fmt.Errorf("yamlSubMap: ключ %q: %w", key, err)
		}
		// null в старом struct-round-trip означал пустое отображение. Сохраняем
		// эту семантику, не заставляя пользователя чинить безвредный null вручную.
		if resolved.Kind == yaml.ScalarNode && resolved.Tag == "!!null" {
			replaceYAMLNode(resolved, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
		}
		if resolved.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("yamlSubMap: ключ %q — не отображение", key)
		}
		return resolved, nil
	}
	inherited, err := yamlMapHasMergedField(mapping, key)
	if err != nil {
		return nil, err
	}
	if inherited {
		// Creating an empty direct mapping would shadow the inherited mapping and
		// silently drop every field the form does not edit (pages, titles, future
		// extension keys). Materialising an arbitrary anchored graph safely is not
		// possible here, so reject the edit and preserve the source verbatim.
		return nil, fmt.Errorf("yamlSubMap: ключ %q унаследован через YAML merge; точечная правка небезопасна", key)
	}
	sub := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, sub)
	return sub, nil
}

// yamlMapField возвращает значение и позицию ключа, не разворачивая alias.
// Неоднозначный дубль отклоняется даже если функция вызвана без общего
// validateYAMLTree.
func yamlMapField(m *yaml.Node, key string) (*yaml.Node, int, error) {
	resolved, err := resolveYAMLAlias(m)
	if err != nil {
		return nil, -1, err
	}
	if resolved.Kind != yaml.MappingNode || len(resolved.Content)%2 != 0 {
		return nil, -1, fmt.Errorf("ожидалось YAML-отображение")
	}
	index := -1
	for i := 0; i < len(resolved.Content); i += 2 {
		if resolved.Content[i].Kind == yaml.ScalarNode && resolved.Content[i].Value == key {
			if index >= 0 {
				return nil, -1, fmt.Errorf("повторяющийся YAML-ключ %q", key)
			}
			index = i
		}
	}
	if index < 0 {
		return nil, -1, nil
	}
	return resolved.Content[index+1], index, nil
}

// yamlMapHasMergedField reports whether key would be inherited through a YAML
// merge key (<<). Removing a direct key without accounting for this would
// merely expose the old inherited value again, so a form would report success
// while the value selected by the user remained unchanged.
func yamlMapHasMergedField(m *yaml.Node, key string) (bool, error) {
	resolved, err := resolveYAMLAlias(m)
	if err != nil {
		return false, err
	}
	if resolved.Kind != yaml.MappingNode || len(resolved.Content)%2 != 0 {
		return false, fmt.Errorf("ожидалось YAML-отображение")
	}
	active := make(map[*yaml.Node]struct{})
	for i := 0; i < len(resolved.Content); i += 2 {
		if isYAMLMergeKey(resolved.Content[i]) {
			return yamlMergeSourceHasField(resolved.Content[i+1], key, active)
		}
	}
	return false, nil
}

func yamlMergeSourceHasField(source *yaml.Node, key string, active map[*yaml.Node]struct{}) (bool, error) {
	resolved, err := resolveYAMLAlias(source)
	if err != nil {
		return false, err
	}
	switch resolved.Kind {
	case yaml.MappingNode:
		return yamlMappingDefinesField(resolved, key, active)
	case yaml.SequenceNode:
		for _, item := range resolved.Content {
			itemResolved, err := resolveYAMLAlias(item)
			if err != nil {
				return false, err
			}
			if itemResolved.Kind != yaml.MappingNode {
				return false, fmt.Errorf("YAML merge ожидает отображение или последовательность отображений")
			}
			found, err := yamlMappingDefinesField(itemResolved, key, active)
			if err != nil || found {
				return found, err
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("YAML merge ожидает отображение или последовательность отображений")
	}
}

func yamlMappingDefinesField(m *yaml.Node, key string, active map[*yaml.Node]struct{}) (bool, error) {
	if _, ok := active[m]; ok {
		return false, fmt.Errorf("циклический YAML merge")
	}
	active[m] = struct{}{}
	defer delete(active, m)

	if m.Kind != yaml.MappingNode || len(m.Content)%2 != 0 {
		return false, fmt.Errorf("YAML merge ожидает отображение")
	}
	for i := 0; i < len(m.Content); i += 2 {
		k := m.Content[i]
		if !isYAMLMergeKey(k) && k.Kind == yaml.ScalarNode && k.Value == key {
			return true, nil
		}
	}
	for i := 0; i < len(m.Content); i += 2 {
		if isYAMLMergeKey(m.Content[i]) {
			found, err := yamlMergeSourceHasField(m.Content[i+1], key, active)
			if err != nil || found {
				return found, err
			}
		}
	}
	return false, nil
}

func isYAMLMergeKey(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Value == "<<" && n.ShortTag() == "!!merge"
}

// yamlNodeDefinesAnchor checks anchor definitions in the represented tree.
// Alias.Alias is deliberately not traversed: an alias only references a
// definition elsewhere and removing that alias does not remove the definition.
func yamlNodeDefinesAnchor(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind != yaml.AliasNode && n.Anchor != "" {
		return true
	}
	for _, child := range n.Content {
		if yamlNodeDefinesAnchor(child) {
			return true
		}
	}
	return false
}

func yamlNodeDescendantsDefineAnchor(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	for _, child := range n.Content {
		if yamlNodeDefinesAnchor(child) {
			return true
		}
	}
	return false
}

func resolveYAMLAlias(n *yaml.Node) (*yaml.Node, error) {
	seen := map[*yaml.Node]struct{}{}
	for n != nil && n.Kind == yaml.AliasNode {
		if _, ok := seen[n]; ok {
			return nil, fmt.Errorf("циклический YAML-псевдоним %q", n.Value)
		}
		seen[n] = struct{}{}
		if n.Alias == nil {
			return nil, fmt.Errorf("YAML-псевдоним %q без цели", n.Value)
		}
		n = n.Alias
	}
	if n == nil {
		return nil, fmt.Errorf("пустая цель YAML-псевдонима")
	}
	return n, nil
}

// replaceYAMLNode меняет значение узла на месте: ссылки Alias продолжают
// указывать на тот же объект, а комментарии/anchor и выбранный человеком стиль
// скаляра не исчезают при сохранении формы.
func replaceYAMLNode(dst, src *yaml.Node) {
	anchor := dst.Anchor
	head, line, foot := dst.HeadComment, dst.LineComment, dst.FootComment
	style := dst.Style
	oldKind := dst.Kind
	*dst = *src
	dst.Anchor = anchor
	dst.HeadComment, dst.LineComment, dst.FootComment = head, line, foot
	// Flow/block style у коллекций можно сохранить без смены значения. Стиль
	// скаляра намеренно не переносим: прежний form-save код кодировал новое
	// значение заново (и существующие тесты фиксируют эту семантику, например
	// снятие лишних кавычек у cron schedule).
	if oldKind == dst.Kind && oldKind != yaml.ScalarNode {
		dst.Style = style
	}
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
