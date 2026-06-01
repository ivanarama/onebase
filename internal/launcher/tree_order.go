package launcher

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/configdb"
	"gopkg.in/yaml.v3"
)

// Пользовательский порядок объектов метаданных в дереве конфигуратора —
// аналог ручного перемещения объектов в 1С:Конфигураторе. Порядок хранится
// рядом с конфигурацией в файле tree_order.yaml (только для file-режима;
// в database-режиме объекты остаются отсортированными по алфавиту).

const treeOrderFile = "tree_order.yaml"

// treeOrderGroups — группы дерева, поддерживающие ручной порядок. Ключ
// совпадает с ключом в tree_order.yaml и со значением data-group в шаблоне.
var treeOrderGroups = map[string]bool{
	"catalogs":         true,
	"documents":        true,
	"registers":        true,
	"inforegisters":    true,
	"accountregisters": true,
	"enums":            true,
	"constants":        true,
	"reports":          true,
	"modules":          true,
	"processors":       true,
	"subsystems":       true,
	"widgets":          true,
}

func treeOrderPath(projDir string) string {
	return filepath.Join(projDir, treeOrderFile)
}

func parseTreeOrder(raw []byte) map[string][]string {
	out := map[string][]string{}
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &out); err != nil || out == nil {
			return map[string][]string{}
		}
	}
	return out
}

// loadTreeOrderFor читает сохранённый порядок объектов/групп дерева независимо
// от режима хранения конфигурации: в file-режиме из tree_order.yaml в каталоге
// проекта, в database-режиме — из записи _onebase_config. Отсутствие данных —
// не ошибка (объекты останутся по алфавиту, группы — в порядке по умолчанию).
func (h *handler) loadTreeOrderFor(ctx context.Context, b *Base) map[string][]string {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return map[string][]string{}
		}
		defer db.Close()
		raw, ok, _ := configdb.New(db).ReadFile(ctx, treeOrderFile)
		if !ok {
			return map[string][]string{}
		}
		return parseTreeOrder(raw)
	}
	raw, err := os.ReadFile(treeOrderPath(b.Path))
	if err != nil {
		return map[string][]string{}
	}
	return parseTreeOrder(raw)
}

// saveTreeOrderGroupFor сохраняет порядок одной группы (или спец-ключа "groups"
// — порядок самих групп), не затрагивая остальные, в нужный бэкенд хранения.
func (h *handler) saveTreeOrderGroupFor(ctx context.Context, b *Base, group string, names []string) error {
	all := h.loadTreeOrderFor(ctx, b)
	all[group] = names
	raw, err := yaml.Marshal(all)
	if err != nil {
		return err
	}
	if b.ConfigSource == "database" {
		db, derr := OpenDB(ctx, b)
		if derr != nil {
			return derr
		}
		defer db.Close()
		return configdb.New(db).SaveFile(ctx, treeOrderFile, raw)
	}
	return os.WriteFile(treeOrderPath(b.Path), raw, 0o644)
}

// readConfigFileRaw читает один файл конфигурации в обоих режимах хранения.
// Второе значение false — файла нет (не ошибка).
func (h *handler) readConfigFileRaw(ctx context.Context, b *Base, relPath string) ([]byte, bool) {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return nil, false
		}
		defer db.Close()
		raw, ok, _ := configdb.New(db).ReadFile(ctx, relPath)
		return raw, ok
	}
	raw, err := os.ReadFile(filepath.Join(b.Path, filepath.FromSlash(relPath)))
	if err != nil {
		return nil, false
	}
	return raw, true
}

// orderLineRe находит верхнеуровневую строку «order: N» в YAML подсистемы.
var orderLineRe = regexp.MustCompile(`(?m)^order:[ \t]*[0-9]+`)

// setYAMLOrder заменяет значение поля order, сохраняя остальное содержимое и
// концы строк нетронутыми. Если поля нет — вставляет его после первой строки.
func setYAMLOrder(raw []byte, order int) []byte {
	repl := "order: " + strconv.Itoa(order)
	if orderLineRe.Match(raw) {
		return orderLineRe.ReplaceAll(raw, []byte(repl))
	}
	nl := strings.IndexByte(string(raw), '\n')
	if nl < 0 {
		return append(append([]byte{}, raw...), []byte("\n"+repl)...)
	}
	out := make([]byte, 0, len(raw)+len(repl)+1)
	out = append(out, raw[:nl+1]...)
	out = append(out, []byte(repl+"\n")...)
	out = append(out, raw[nl+1:]...)
	return out
}

// applySubsystemOrder переписывает поле order в YAML каждой подсистемы так, чтобы
// порядок в пользовательском режиме (метаданные сортируются по order, см.
// metadata.LoadSubsystems) совпал с ручным порядком в дереве конфигуратора. Шаг
// 10 оставляет зазоры для будущих вставок — как принято в 1С.
func (h *handler) applySubsystemOrder(ctx context.Context, b *Base, names []string) {
	for i, name := range names {
		relPath := "subsystems/" + nameToFilename(name) + ".yaml"
		raw, ok := h.readConfigFileRaw(ctx, b, relPath)
		if !ok {
			continue
		}
		updated := setYAMLOrder(raw, (i+1)*10)
		if updated == nil {
			continue
		}
		_ = h.saveConfigFile(ctx, b, relPath, updated)
	}
}

// reorderByName стабильно переставляет items: перечисленные в order идут первыми
// в заданном порядке, остальные — следом по алфавиту (регистронезависимо).
func reorderByName[T any](items []T, order []string, nameOf func(T) string) {
	if len(items) < 2 || len(order) == 0 {
		return
	}
	idx := make(map[string]int, len(order))
	for i, n := range order {
		idx[strings.ToLower(strings.TrimSpace(n))] = i
	}
	sort.SliceStable(items, func(a, b int) bool {
		na := strings.ToLower(nameOf(items[a]))
		nb := strings.ToLower(nameOf(items[b]))
		ia, oka := idx[na]
		ib, okb := idx[nb]
		switch {
		case oka && okb:
			return ia < ib
		case oka != okb:
			return oka // элемент из сохранённого порядка идёт раньше
		default:
			return na < nb
		}
	})
}

// applyTreeOrder применяет сохранённый порядок ко всем спискам дерева, а также
// заполняет data.GroupOrder (порядок самих групп для клиентской перестановки).
func applyTreeOrder(data *configuratorData, order map[string][]string) {
	if len(order) == 0 {
		return
	}
	data.GroupOrder = order["groups"]
	reorderByName(data.Catalogs, order["catalogs"], func(e cfgEntity) string { return e.Name })
	reorderByName(data.Docs, order["documents"], func(e cfgEntity) string { return e.Name })
	reorderByName(data.Registers, order["registers"], func(e cfgRegister) string { return e.Name })
	reorderByName(data.InfoRegisters, order["inforegisters"], func(e cfgInfoRegister) string { return e.Name })
	reorderByName(data.AccountRegisters, order["accountregisters"], func(e cfgAccountRegister) string { return e.Name })
	reorderByName(data.Enums, order["enums"], func(e cfgEnum) string { return e.Name })
	reorderByName(data.Constants, order["constants"], func(e cfgConstant) string { return e.Name })
	reorderByName(data.Reports, order["reports"], func(e cfgReport) string { return e.Name })
	reorderByName(data.Modules, order["modules"], func(e cfgModule) string { return e.Name })
	reorderByName(data.Processors, order["processors"], func(e cfgProcessor) string { return e.Name })
	reorderByName(data.Subsystems, order["subsystems"], func(e cfgSubsystem) string { return e.Name })
	reorderByName(data.Widgets, order["widgets"], func(e cfgWidget) string { return e.Name })
}
