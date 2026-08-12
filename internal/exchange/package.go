package exchange

// Сборка и разбор пакетов обмена (.obx, план 86). Пакет — версионированный JSON:
// набор изменённых объектов (шапка + табличные части) с их версией-источником.
// Загрузка идемпотентна по версии: повторная доставка того же пакета не создаёт
// дублей и не наращивает ревизии.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// FormatV1 — значение поля Format в пакете первой версии.
const FormatV1 = "onebase-exchange/1"

const (
	// MaxPackageBytes is shared by file and HTTP transports.
	MaxPackageBytes   = 64 << 20
	MaxPackageObjects = 10_000
)

// EntityResolver отдаёт метаданные состава обмена по имени (сущность или регистр
// сведений). Реализуется runtime.Registry, но объявлен здесь, чтобы пакет обмена
// не зависел от runtime и был тестируем с фейковым резолвером.
type EntityResolver interface {
	GetEntity(name string) *metadata.Entity
	GetConstantMeta(name string) *metadata.Constant
	GetInfoRegister(name string) *metadata.InfoRegister
}

// Package — конверт обмена.
type Package struct {
	Format    string `json:"format"`
	Plan      string `json:"plan"`
	FromNode  string `json:"from_node"`
	ToNode    string `json:"to_node"`
	MessageNo int64  `json:"message_no"`
	// AckNo — «я получил от тебя сообщения вплоть до этого номера». Приёмник по
	// нему очищает свою очередь к отправителю (подтверждение приёма пакетов,
	// доставленных ранее в обратную сторону). 0 = нечего подтверждать.
	AckNo   int64           `json:"ack_no,omitempty"`
	Objects []PackageObject `json:"objects"`
}

// PackageObject — один объект в пакете. Fields/TableParts несут канонизированные
// значения (числа — точной строкой, даты — RFC3339, ссылки — UUID-строкой),
// пригодные для обратной записи через тот же путь, что и обычное сохранение.
type PackageObject struct {
	// Kind — категория объекта: "" (сущность), storage.ExchangeKindConstant,
	// storage.ExchangeKindInfoReg. Определяет форму полей и путь применения.
	Kind      string `json:"kind,omitempty"`
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	Version   int64  `json:"version"`
	Deletion  bool   `json:"deletion,omitempty"`
	Tombstone bool   `json:"tombstone,omitempty"`
	// Posted — документ был проведён на источнике. Приёмник по нему перепроводит
	// документ, если у плана включён repost (иначе документ приходит непроведённым).
	Posted     bool                        `json:"posted,omitempty"`
	ChangedAt  int64                       `json:"changed_at"`
	Fields     map[string]any              `json:"fields,omitempty"`
	TableParts map[string][]map[string]any `json:"table_parts,omitempty"`
}

// LoadResult — итог загрузки пакета.
type LoadResult struct {
	Applied   int `json:"applied"`            // применено (создано/обновлено)
	Skipped   int `json:"skipped"`            // пропущено (идемпотентно: версия не новее, либо сущность неизвестна)
	Deleted   int `json:"deleted"`            // применено с пометкой на удаление
	Conflicts int `json:"conflicts"`          // обнаружено встречных правок (разрешено правилом)
	Reposted  int `json:"reposted,omitempty"` // перепроведено документов на приёмнике (repost)
}

// BuildPackage собирает пакет незапподтверждённых изменений для узла toNode,
// присваивает ему следующий номер сообщения и помечает вошедшие строки очереди
// как выгруженные. Всё — в одной транзакции.
func BuildPackage(ctx context.Context, store *storage.DB, resolver EntityResolver, plan *metadata.ExchangePlan, toNode string) ([]byte, error) {
	if err := validateExchangePlan(plan); err != nil {
		return nil, err
	}
	toNodeDef := plan.Node(toNode)
	if toNodeDef == nil {
		return nil, fmt.Errorf("exchange: узел-получатель %q не описан в плане %q", toNode, plan.Name)
	}
	toNode = toNodeDef.Code
	var out []byte
	err := store.WithTx(ctx, func(ctx context.Context) error {
		thisNode, err := store.GetExchangeThisNode(ctx, plan.Name)
		if err != nil {
			return err
		}
		thisNodeDef := plan.Node(thisNode)
		if thisNode == "" || thisNodeDef == nil {
			return fmt.Errorf("exchange: текущий узел базы не задан или не описан в плане %q", plan.Name)
		}
		thisNode = thisNodeDef.Code
		if strings.EqualFold(thisNode, toNode) {
			return fmt.Errorf("exchange: нельзя выгрузить пакет текущему узлу %q", toNode)
		}
		changes, err := store.PendingExchangeChanges(ctx, plan.Name, toNode)
		if err != nil {
			return err
		}
		if len(changes) > MaxPackageObjects {
			changes = changes[:MaxPackageObjects]
		}
		msgNo, err := store.NextExchangeMessageNo(ctx, plan.Name, toNode)
		if err != nil {
			return err
		}
		// Пиггибэк подтверждения: сообщаем узлу, сколько его сообщений мы приняли,
		// чтобы он очистил свою очередь к нам (recv_no по этому узлу).
		peer, err := store.GetExchangePeer(ctx, plan.Name, toNode)
		if err != nil {
			return err
		}
		pkg := Package{Format: FormatV1, Plan: plan.Name, FromNode: thisNode, ToNode: toNode, MessageNo: msgNo, AckNo: peer.RecvNo}
		var included []storage.ExchangeChange
		for _, ch := range changes {
			switch ch.Kind {
			case storage.ExchangeKindConstant:
				constant := resolver.GetConstantMeta(ch.ObjectType)
				if constant == nil {
					return fmt.Errorf("exchange: константа очереди %q отсутствует в конфигурации", ch.ObjectType)
				}
				if !plan.IncludesConstant(constant.Name) {
					return fmt.Errorf("exchange: константа очереди %q не входит в состав плана", ch.ObjectType)
				}
				val, err := store.GetConstant(ctx, constant.Name)
				if err != nil {
					return fmt.Errorf("exchange: константа очереди %q недоступна: %w", ch.ObjectType, err)
				}
				pkg.Objects = append(pkg.Objects, PackageObject{
					Kind:      storage.ExchangeKindConstant,
					Type:      constant.Name,
					Version:   ch.Version,
					ChangedAt: ch.ChangedAt,
					Fields:    map[string]any{"value": val},
				})
				included = append(included, ch)
			case storage.ExchangeKindInfoReg:
				ir := resolver.GetInfoRegister(ch.ObjectType)
				if ir == nil {
					return fmt.Errorf("exchange: регистр сведений очереди %q отсутствует в конфигурации", ch.ObjectType)
				}
				if ir.Periodic || !plan.IncludesInfoRegister(ir.Name) {
					return fmt.Errorf("exchange: регистр сведений очереди %q не поддержан или не входит в состав плана", ch.ObjectType)
				}
				dims, derr := decodeInfoRegKey(ch.ObjectID)
				if derr != nil {
					return fmt.Errorf("exchange: неверный ключ регистра сведений %q: %w", ch.ObjectType, derr)
				}
				obj := PackageObject{
					Kind:      storage.ExchangeKindInfoReg,
					Type:      ch.ObjectType,
					ID:        ch.ObjectID,
					Version:   ch.Version,
					Deletion:  ch.Deletion,
					ChangedAt: ch.ChangedAt,
					Fields:    map[string]any{},
				}
				for k, v := range dims { // измерения из ключа уже каноничны
					obj.Fields[k] = v
				}
				if !ch.Deletion {
					rec, rerr := store.InfoRegGetExact(ctx, ir, dims, nil)
					if rerr != nil {
						return rerr
					}
					if rec == nil {
						return fmt.Errorf("exchange: запись регистра сведений %s/%s исчезла без регистрации удаления", ir.Name, ch.ObjectID)
					}
					for _, rf := range ir.Resources {
						obj.Fields[rf.Name] = canonicalScalar(rf, rec[rf.Name])
					}
				}
				pkg.Objects = append(pkg.Objects, obj)
				included = append(included, ch)
			default: // сущность (справочник/документ)
				ent := resolver.GetEntity(ch.ObjectType)
				if ent == nil {
					return fmt.Errorf("exchange: сущность очереди %q отсутствует в конфигурации; пакет не сформирован", ch.ObjectType)
				}
				id, err := uuid.Parse(ch.ObjectID)
				if err != nil {
					return fmt.Errorf("exchange: неверный id %q в очереди: %w", ch.ObjectID, err)
				}
				version, exists, err := store.EntityVersionExists(ctx, ent.Name, id)
				if err != nil {
					return err
				}
				if !exists && !ch.Deletion {
					return fmt.Errorf("exchange: объект очереди %s/%s физически удалён без tombstone", ent.Name, id)
				}
				// Не смешиваем поля новой записи со старой версией строки очереди.
				if exists && version != ch.Version {
					continue
				}
				var row map[string]any
				if exists {
					row, err = store.GetByID(ctx, ent.Name, id, ent)
					if err != nil {
						return fmt.Errorf("exchange: объект очереди %s/%s недоступен: %w", ent.Name, id, err)
					}
				}
				obj := PackageObject{
					Type:      ch.ObjectType,
					ID:        ch.ObjectID,
					Version:   ch.Version,
					Deletion:  ch.Deletion,
					Posted:    exists && ent.Kind == metadata.KindDocument && toBool(row["posted"]),
					ChangedAt: ch.ChangedAt,
					Fields:    canonicalHeader(ent, row),
				}
				if !exists {
					obj.Tombstone = true
					obj.Fields = emptyCanonicalHeader(ent)
				}
				if len(ent.TableParts) > 0 {
					obj.TableParts = make(map[string][]map[string]any, len(ent.TableParts))
				}
				for _, tp := range ent.TableParts {
					if obj.Tombstone {
						obj.TableParts[tp.Name] = []map[string]any{}
						continue
					}
					rows, err := store.GetTablePartRows(ctx, ent.Name, tp.Name, id, tp)
					if err != nil {
						return err
					}
					canon := make([]map[string]any, 0, len(rows))
					for _, r := range rows {
						canon = append(canon, canonicalRow(tp.Fields, r))
					}
					obj.TableParts[tp.Name] = canon
				}
				pkg.Objects = append(pkg.Objects, obj)
				included = append(included, ch)
			}
		}
		if err := store.MarkExchangeChangesSent(ctx, included, msgNo); err != nil {
			return err
		}
		out, err = json.MarshalIndent(pkg, "", "  ")
		if err != nil {
			return err
		}
		if len(out) > MaxPackageBytes {
			return fmt.Errorf("exchange: сформированный пакет превышает лимит %d байт; уменьшите размер объекта", MaxPackageBytes)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ParsePackage разбирает и валидирует конверт пакета.
func ParsePackage(data []byte) (*Package, error) {
	if len(data) > MaxPackageBytes {
		return nil, fmt.Errorf("exchange: пакет превышает лимит %d байт", MaxPackageBytes)
	}
	var pkg Package
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pkg); err != nil {
		return nil, fmt.Errorf("exchange: разбор пакета: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("exchange: после конверта обнаружены лишние JSON-данные")
		}
		return nil, fmt.Errorf("exchange: разбор хвоста пакета: %w", err)
	}
	if pkg.Format != FormatV1 {
		return nil, fmt.Errorf("exchange: неизвестный формат пакета %q (ожидался %q)", pkg.Format, FormatV1)
	}
	if strings.TrimSpace(pkg.Plan) == "" || strings.TrimSpace(pkg.FromNode) == "" || strings.TrimSpace(pkg.ToNode) == "" {
		return nil, fmt.Errorf("exchange: plan, from_node и to_node обязательны")
	}
	if pkg.MessageNo <= 0 || pkg.AckNo < 0 {
		return nil, fmt.Errorf("exchange: неверные счётчики message_no=%d ack_no=%d", pkg.MessageNo, pkg.AckNo)
	}
	if len(pkg.Objects) > MaxPackageObjects {
		return nil, fmt.Errorf("exchange: объектов в пакете %d, лимит %d", len(pkg.Objects), MaxPackageObjects)
	}
	seen := make(map[string]struct{}, len(pkg.Objects))
	for i := range pkg.Objects {
		obj := &pkg.Objects[i]
		obj.Kind = strings.ToLower(strings.TrimSpace(obj.Kind))
		obj.Type = strings.TrimSpace(obj.Type)
		if obj.Type == "" || obj.Version <= 0 || obj.ChangedAt <= 0 {
			return nil, fmt.Errorf("exchange: объект %d имеет неверные type/version/changed_at", i)
		}
		if obj.Tombstone && !obj.Deletion {
			return nil, fmt.Errorf("exchange: tombstone объекта %d должен иметь deletion=true", i)
		}
		switch obj.Kind {
		case storage.ExchangeKindConstant:
			if obj.ID != "" || obj.Deletion || obj.Tombstone || obj.Posted {
				return nil, fmt.Errorf("exchange: константа %d имеет несовместимые системные поля", i)
			}
		case storage.ExchangeKindInfoReg:
			if obj.ID == "" || obj.Tombstone || obj.Posted {
				return nil, fmt.Errorf("exchange: запись регистра сведений %d имеет несовместимые системные поля", i)
			}
			dims, err := decodeInfoRegKey(obj.ID)
			if err != nil {
				return nil, fmt.Errorf("exchange: запись регистра сведений %d: неверный ключ: %w", i, err)
			}
			canonical, err := json.Marshal(dims)
			if err != nil {
				return nil, fmt.Errorf("exchange: запись регистра сведений %d: неверный ключ: %w", i, err)
			}
			obj.ID = string(canonical)
		case "":
			id, err := uuid.Parse(obj.ID)
			if err != nil {
				return nil, fmt.Errorf("exchange: объект %d: неверный id: %w", i, err)
			}
			obj.ID = id.String()
		default:
			return nil, fmt.Errorf("exchange: объект %d имеет неизвестный kind %q", i, obj.Kind)
		}
		key := obj.Kind + "/" + strings.ToLower(obj.Type) + "/" + obj.ID
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("exchange: объект %s продублирован в пакете", key)
		}
		seen[key] = struct{}{}
	}
	return &pkg, nil
}

func validateObjectShape(ent *metadata.Entity, obj PackageObject) error {
	wantFields := make(map[string]struct{}, len(ent.Fields)+2)
	for _, f := range ent.Fields {
		wantFields[f.Name] = struct{}{}
	}
	if ent.Hierarchical {
		wantFields["parent_id"] = struct{}{}
		wantFields["is_folder"] = struct{}{}
	}
	if len(obj.Fields) != len(wantFields) {
		return fmt.Errorf("exchange: объект %s/%s содержит неполную или несовместимую шапку", ent.Name, obj.ID)
	}
	for name := range obj.Fields {
		if _, ok := wantFields[name]; !ok {
			return fmt.Errorf("exchange: объект %s/%s содержит неизвестное поле %q", ent.Name, obj.ID, name)
		}
	}
	if len(obj.TableParts) != len(ent.TableParts) {
		return fmt.Errorf("exchange: объект %s/%s содержит неполный набор табличных частей", ent.Name, obj.ID)
	}
	parts := make(map[string]metadata.TablePart, len(ent.TableParts))
	for _, tp := range ent.TableParts {
		parts[tp.Name] = tp
	}
	for name, rows := range obj.TableParts {
		tp, ok := parts[name]
		if !ok {
			return fmt.Errorf("exchange: объект %s/%s содержит неизвестную табличную часть %q", ent.Name, obj.ID, name)
		}
		want := make(map[string]struct{}, len(tp.Fields))
		for _, f := range tp.Fields {
			want[f.Name] = struct{}{}
		}
		for rowNo, row := range rows {
			if len(row) != len(want) {
				return fmt.Errorf("exchange: %s/%s.%s[%d] имеет несовместимый набор полей", ent.Name, obj.ID, tp.Name, rowNo)
			}
			for field := range row {
				if _, ok := want[field]; !ok {
					return fmt.Errorf("exchange: %s/%s.%s[%d] содержит неизвестное поле %q", ent.Name, obj.ID, tp.Name, rowNo, field)
				}
			}
		}
	}
	return nil
}

func validatePackageObjects(pkg *Package, resolver EntityResolver, plan *metadata.ExchangePlan) error {
	for i := range pkg.Objects {
		obj := &pkg.Objects[i]
		switch obj.Kind {
		case storage.ExchangeKindConstant:
			constant := resolver.GetConstantMeta(obj.Type)
			if constant == nil {
				return fmt.Errorf("exchange: константа %q неизвестна приёмнику", obj.Type)
			}
			if !plan.IncludesConstant(constant.Name) {
				return fmt.Errorf("exchange: константа %q не входит в состав плана %q", obj.Type, plan.Name)
			}
			obj.Type = constant.Name
			if len(obj.Fields) != 1 {
				return fmt.Errorf("exchange: константа %q содержит несовместимый набор полей", obj.Type)
			}
			if _, ok := obj.Fields["value"]; !ok {
				return fmt.Errorf("exchange: константа %q не содержит поле value", obj.Type)
			}
		case storage.ExchangeKindInfoReg:
			ir := resolver.GetInfoRegister(obj.Type)
			if ir == nil {
				return fmt.Errorf("exchange: регистр сведений %q неизвестен приёмнику", obj.Type)
			}
			if ir.Periodic || !plan.IncludesInfoRegister(ir.Name) {
				return fmt.Errorf("exchange: регистр сведений %q не поддержан или не входит в состав плана %q", obj.Type, plan.Name)
			}
			obj.Type = ir.Name
			if err := validateInfoRegObjectShape(ir, *obj); err != nil {
				return err
			}
		default:
			ent := resolver.GetEntity(obj.Type)
			if ent == nil {
				return fmt.Errorf("exchange: сущность %q неизвестна приёмнику; пакет отклонён без подтверждения", obj.Type)
			}
			if !plan.Includes(ent) {
				return fmt.Errorf("exchange: сущность %q не входит в состав плана %q", obj.Type, plan.Name)
			}
			if obj.Posted && (ent.Kind != metadata.KindDocument || !ent.Posting || obj.Tombstone) {
				return fmt.Errorf("exchange: объект %s/%s имеет несовместимый признак posted", ent.Name, obj.ID)
			}
			obj.Type = ent.Name
			if err := validateObjectShape(ent, *obj); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateInfoRegObjectShape(ir *metadata.InfoRegister, obj PackageObject) error {
	want := make(map[string]struct{}, len(ir.Dimensions)+len(ir.Resources))
	dims := make(map[string]any, len(ir.Dimensions))
	for _, field := range ir.Dimensions {
		want[field.Name] = struct{}{}
		value, ok := obj.Fields[field.Name]
		if !ok {
			return fmt.Errorf("exchange: запись регистра сведений %s/%s не содержит измерение %q", ir.Name, obj.ID, field.Name)
		}
		dims[field.Name] = value
	}
	if !obj.Deletion {
		for _, field := range ir.Resources {
			want[field.Name] = struct{}{}
		}
	}
	if len(obj.Fields) != len(want) {
		return fmt.Errorf("exchange: запись регистра сведений %s/%s содержит несовместимый набор полей", ir.Name, obj.ID)
	}
	for name := range obj.Fields {
		if _, ok := want[name]; !ok {
			return fmt.Errorf("exchange: запись регистра сведений %s/%s содержит неизвестное поле %q", ir.Name, obj.ID, name)
		}
	}
	key, err := json.Marshal(dims)
	if err != nil || string(key) != obj.ID {
		return fmt.Errorf("exchange: ключ записи регистра сведений %s не совпадает с измерениями", ir.Name)
	}
	return nil
}

// ApplyPackage идемпотентно загружает пакет в план plan. Для каждого объекта:
//   - если приёмник тоже менял его со времени последней синхронизации с
//     источником (есть неотправленная строка очереди этому узлу) — это конфликт,
//     разрешаемый правилом плана (resolveConflict);
//   - иначе применяется, если объекта нет или входящая версия строго новее
//     локальной (_version); иначе пропускается (идемпотентно).
//
// Запись идёт напрямую через storage (минуя entityservice.Save) — без хуков,
// движений и обратной регистрации в обмене (нет эха).
func ApplyPackage(ctx context.Context, store *storage.DB, resolver EntityResolver, plan *metadata.ExchangePlan, data []byte, opts ApplyOptions) (LoadResult, error) {
	pkg, err := ParsePackage(data)
	if err != nil {
		return LoadResult{}, err
	}
	if plan == nil {
		return LoadResult{}, fmt.Errorf("exchange: план %q не найден на приёмнике", pkg.Plan)
	}
	if err := validateExchangePlan(plan); err != nil {
		return LoadResult{}, err
	}
	if !strings.EqualFold(pkg.Plan, plan.Name) {
		return LoadResult{}, fmt.Errorf("exchange: пакет плана %q загружается в план %q", pkg.Plan, plan.Name)
	}
	if err := validatePackageObjects(pkg, resolver, plan); err != nil {
		return LoadResult{}, err
	}
	var res LoadResult
	var toRepost []PackageObject // проведённые документы к перепроведению (после коммита)
	err = store.WithTx(ctx, func(ctx context.Context) error {
		thisNode, err := store.GetExchangeThisNode(ctx, plan.Name)
		if err != nil {
			return err
		}
		thisNodeDef := plan.Node(thisNode)
		if thisNode == "" || thisNodeDef == nil {
			return fmt.Errorf("exchange: текущий узел базы не задан или не описан в плане %q", plan.Name)
		}
		thisNode = thisNodeDef.Code
		fromNodeDef := plan.Node(pkg.FromNode)
		if pkg.FromNode == "" || fromNodeDef == nil {
			return fmt.Errorf("exchange: узел-источник %q не описан в плане %q", pkg.FromNode, plan.Name)
		}
		fromNode := fromNodeDef.Code
		if pkg.ToNode == "" || !strings.EqualFold(pkg.ToNode, thisNode) {
			return fmt.Errorf("exchange: пакет адресован узлу %q, текущий узел %q", pkg.ToNode, thisNode)
		}
		if strings.EqualFold(fromNode, thisNode) {
			return fmt.Errorf("exchange: пакет не может быть отправлен текущим узлом %q самому себе", thisNode)
		}
		peer, err := store.GetExchangePeer(ctx, plan.Name, fromNode)
		if err != nil {
			return err
		}
		if pkg.AckNo > peer.SentNo {
			return fmt.Errorf("exchange: ack_no=%d больше последнего отправленного сообщения %d узлу %q", pkg.AckNo, peer.SentNo, fromNode)
		}
		// Подтверждённая отправителем строка очереди уже не является встречной
		// правкой и не должна участвовать в разрешении конфликта.
		if pkg.AckNo > 0 {
			if _, err := store.AckExchangeChanges(ctx, plan.Name, fromNode, pkg.AckNo); err != nil {
				return err
			}
		}
		toRepost = toRepost[:0]
		if pkg.MessageNo <= peer.RecvNo {
			// Данные повторного сообщения уже применены. Единственная операция,
			// которую надо безопасно повторить, — перепроведение после прошлой ошибки.
			res.Skipped = len(pkg.Objects)
			for _, obj := range pkg.Objects {
				if obj.Kind != "" || !plan.Repost || opts.Repost == nil || !obj.Posted || obj.Deletion {
					continue
				}
				need, err := needsRepost(ctx, store, resolver, obj)
				if err != nil {
					return err
				}
				if need {
					toRepost = append(toRepost, obj)
				}
			}
			return nil
		}

		// Транзит хаб→спицы (план 86, фаза 2): если этот узел — хаб, применённые
		// изменения ретранслируются остальным спицам (кроме источника). Пусто, если
		// узел не хаб или топология плоская — тогда обмен работает как раньше.
		transit := plan.TransitTargets(thisNode, fromNode)
		var applied []PackageObject
		for _, obj := range pkg.Objects {
			var objApplied bool
			var entityCurrent bool
			switch obj.Kind {
			case storage.ExchangeKindConstant:
				objApplied, err = applyConstant(ctx, store, plan, thisNode, fromNode, obj, &res)
			case storage.ExchangeKindInfoReg:
				objApplied, err = applyInfoReg(ctx, store, resolver, plan, thisNode, fromNode, obj, &res)
			default:
				objApplied, entityCurrent, err = applyEntity(ctx, store, resolver, plan, thisNode, fromNode, pkg.MessageNo, obj, &res, opts)
			}
			if err != nil {
				return err
			}
			if objApplied && len(transit) > 0 {
				applied = append(applied, obj)
			}
			// Перепроведение (план 86, фаза 2): применённый документ, проведённый на
			// источнике, при включённом repost и доступном обработчике — в очередь
			// на перепроведение ПОСЛЕ коммита (entityservice открывает свою транзакцию).
			// Равная локальная версия тоже считается кандидатом: это повтор пакета
			// после ошибки перепроведения, когда сами данные уже были закоммичены.
			if entityCurrent && plan.Repost && opts.Repost != nil && obj.Posted && !obj.Deletion {
				need, nerr := needsRepost(ctx, store, resolver, obj)
				if nerr != nil {
					return nerr
				}
				if need {
					toRepost = append(toRepost, obj)
				}
			}
		}
		// Ретрансляция применённых изменений спицам (в той же транзакции).
		for _, obj := range applied {
			for _, target := range transit {
				if err := store.RegisterExchangeChange(ctx, storage.ExchangeChange{
					Plan:       plan.Name,
					ObjectType: obj.Type,
					ObjectID:   obj.ID,
					NodeCode:   target,
					Kind:       obj.Kind,
					Version:    obj.Version,
					Deletion:   obj.Deletion,
					ChangedAt:  obj.ChangedAt,
				}); err != nil {
					return err
				}
			}
		}
		// Запоминаем номер принятого сообщения от узла-источника.
		if err := store.SetExchangeRecvNo(ctx, plan.Name, fromNode, pkg.MessageNo); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return LoadResult{}, err
	}
	// Перепроведение документов — ВНЕ транзакции загрузки: entityservice.Repost
	// открывает собственную транзакцию (storage.WithTx не вложенная), запускает
	// ОбработкаПроведения и пишет движения в регистры приёмника. Данные уже
	// закоммичены, поэтому ошибка перепроведения не откатывает загрузку — сообщаем
	// её вызывающему, документ останется непроведённым до следующей синхронизации.
	for _, obj := range toRepost {
		id, perr := uuid.Parse(obj.ID)
		if perr != nil {
			continue
		}
		if rerr := opts.Repost(ctx, obj.Type, id); rerr != nil {
			return res, fmt.Errorf("exchange: перепроведение %s %s: %w", obj.Type, obj.ID, rerr)
		}
		res.Reposted++
	}
	return res, nil
}

// replicationSourceRef — происхождение реплицированного перехода: канонический
// JSON-массив, а не строка с разделителем (имя плана или узла может содержать
// что угодно, включая сам разделитель).
func replicationSourceRef(plan, fromNode string, messageNo int64) string {
	ref, err := json.Marshal([]any{storage.StageSourceExchange, plan, fromNode, messageNo})
	if err != nil {
		return ""
	}
	return string(ref)
}

// applyObject записывает один объект пакета: шапку (через db.Upsert — тот же
// путь коэрции значений, что и обычное сохранение), затем принудительно ставит
// системные колонки (точная версия/пометка/непроведён), затем табличные части.
// applyEntity применяет объект-сущность (справочник/документ) из пакета. Возвращает
// true, если объект был записан (создан/обновлён/помечен на удаление) — только такие
// изменения хаб ретранслирует спицам.
func applyEntity(ctx context.Context, store *storage.DB, resolver EntityResolver, plan *metadata.ExchangePlan, thisNode, fromNode string, messageNo int64, obj PackageObject, res *LoadResult, opts ApplyOptions) (applied bool, current bool, err error) {
	ent := resolver.GetEntity(obj.Type)
	if ent == nil {
		res.Skipped++ // сущность неизвестна приёмнику
		return false, false, nil
	}
	id, err := uuid.Parse(obj.ID)
	if err != nil {
		res.Skipped++
		return false, false, nil
	}
	// Встречная правка: приёмник менял тот же объект и ещё не отправил изменение
	// источнику → конфликт, разрешаемый правилом плана.
	local, hasLocal, err := store.GetExchangeChange(ctx, plan.Name, obj.Type, obj.ID, fromNode)
	if err != nil {
		return false, false, err
	}
	if hasLocal {
		res.Conflicts++
		win, err := resolveConflict(ctx, store, plan, thisNode, fromNode, ent, id, obj, local.ChangedAt, opts.Hook)
		if err != nil {
			return false, false, err
		}
		if !win {
			res.Skipped++ // локальное изменение победило — не применяем
			return false, false, nil
		}
	} else {
		// Нет встречной правки — идемпотентность по версии.
		localVer, exists, err := store.EntityVersionExists(ctx, ent.Name, id)
		if err != nil {
			return false, false, err
		}
		if exists && obj.Version <= localVer {
			res.Skipped++
			// Равная версия может быть повтором после ошибки перепроведения.
			// Более старая версия никогда не должна менять состояние документа.
			return false, obj.Version == localVer, nil
		}
	}
	if err := applyObject(ctx, store, ent, id, obj, replicationSourceRef(plan.Name, fromNode, messageNo)); err != nil {
		return false, false, err
	}
	if hasLocal {
		if err := store.DeleteExchangeChange(ctx, plan.Name, ent.Name, id.String(), fromNode); err != nil {
			return false, false, err
		}
	}
	if obj.Deletion {
		res.Deleted++
	} else {
		res.Applied++
	}
	return true, true, nil
}

// needsRepost проверяет, что текущая версия пакета относится к проводимому
// документу, который на приёмнике ещё не проведён. Проверка делает повтор после
// ошибки безопасным и не перепроводит уже успешно обработанный документ.
func needsRepost(ctx context.Context, store *storage.DB, resolver EntityResolver, obj PackageObject) (bool, error) {
	ent := resolver.GetEntity(obj.Type)
	if ent == nil || ent.Kind != metadata.KindDocument || !ent.Posting {
		return false, nil
	}
	id, err := uuid.Parse(obj.ID)
	if err != nil {
		return false, nil
	}
	version, exists, err := store.EntityVersionExists(ctx, ent.Name, id)
	if err != nil {
		return false, err
	}
	if !exists || version != obj.Version {
		return false, nil
	}
	row, err := store.GetByID(ctx, ent.Name, id, ent)
	if err != nil {
		return false, err
	}
	return !toBool(row["posted"]), nil
}

func applyObject(ctx context.Context, store *storage.DB, ent *metadata.Entity, id uuid.UUID, obj PackageObject, sourceRef string) error {
	_, exists, err := store.EntityVersionExists(ctx, ent.Name, id)
	if err != nil {
		return err
	}
	if !obj.Tombstone || !exists {
		// Этапы (план 121): запись идёт узким writer-ом репликации, а не обычным
		// Upsert. Он вызывается только после решения «побеждает входящий»,
		// поэтому DSL-хук разрешения конфликта — обычный локальный writer и
		// обхода маршрута не наследует.
		if err := store.ApplyReplicatedEntity(ctx, ent.Name, id, obj.Fields, ent, sourceRef); err != nil {
			return err
		}
	}
	if err := store.SetExchangeObjectState(ctx, ent, id, obj.Version, obj.Deletion); err != nil {
		return err
	}
	if obj.Tombstone {
		return nil
	}
	for _, tp := range ent.TableParts {
		rows, ok := obj.TableParts[tp.Name]
		if !ok {
			continue
		}
		if err := store.UpsertTablePartRows(ctx, ent.Name, tp.Name, id, rows, tp); err != nil {
			return err
		}
	}
	return nil
}

// canonicalHeader канонизирует значения полей шапки объекта для транспорта.
func canonicalHeader(ent *metadata.Entity, row map[string]any) map[string]any {
	out := canonicalRow(ent.Fields, row)
	if ent.Hierarchical {
		out["parent_id"] = canonicalScalar(metadata.Field{}, row["parent_id"])
		out["is_folder"] = toBool(row["is_folder"])
	}
	return out
}

func emptyCanonicalHeader(ent *metadata.Entity) map[string]any {
	out := make(map[string]any, len(ent.Fields)+2)
	for _, f := range ent.Fields {
		out[f.Name] = nil
	}
	if ent.Hierarchical {
		out["parent_id"] = nil
		out["is_folder"] = false
	}
	return out
}

// canonicalRow канонизирует значения одного набора полей (шапка или строка ТЧ).
func canonicalRow(fields []metadata.Field, row map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		out[f.Name] = canonicalScalar(f, row[f.Name])
	}
	return out
}

// canonicalScalar приводит значение поля к транспортному виду, устойчивому к
// JSON-раунд-трипу и пригодному для обратной записи через fieldValueDialect:
//   - nil → nil;
//   - булево → bool;
//   - число → точная десятичная строка (без потери точности через float64);
//   - дата (time.Time) → RFC3339;
//   - остальное (ссылки-UUID, строки, перечисления) → строка.
func canonicalScalar(f metadata.Field, v any) any {
	if v == nil {
		return nil
	}
	switch f.Type {
	case metadata.FieldTypeBool:
		return toBool(v)
	case metadata.FieldTypeDate:
		if t, ok := v.(time.Time); ok {
			return t.UTC().Format(time.RFC3339)
		}
		return fmt.Sprintf("%v", v)
	default:
		if t, ok := v.(time.Time); ok {
			return t.UTC().Format(time.RFC3339)
		}
		// Числа приходят как decimal.Decimal (Stringer даёт точную строку),
		// ссылки/строки — уже строки; fmt.Sprintf покрывает оба случая.
		return fmt.Sprintf("%v", v)
	}
}

// toBool нормализует булево из разных представлений БД (bool, int 0/1).
func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	case float64:
		return t != 0
	case string:
		return t == "true" || t == "1"
	}
	return false
}
