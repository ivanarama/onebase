package storage_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

type graphSources struct {
	entities []*metadata.Entity
	regs     []*metadata.Register
	info     []*metadata.InfoRegister
	accounts []*metadata.AccountRegister
}

func (s graphSources) Entities() []*metadata.Entity                  { return s.entities }
func (s graphSources) Registers() []*metadata.Register               { return s.regs }
func (s graphSources) InfoRegisters() []*metadata.InfoRegister       { return s.info }
func (s graphSources) AccountRegisters() []*metadata.AccountRegister { return s.accounts }

func graphRef(name, target string) metadata.Field {
	return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + target), RefEntity: target}
}
func graphString(name string) metadata.Field {
	return metadata.Field{Name: name, Type: metadata.FieldTypeString}
}
func graphID(i int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i)) }
func graphMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func graphReads(t *testing.T, root string, src graphSources, direction string) []storage.ReferenceRead {
	t.Helper()
	keys, err := storage.ReferenceSources(root, src)
	graphMust(t, err)
	var out []storage.ReferenceRead
	for _, k := range keys {
		if k.Direction == direction {
			out = append(out, storage.ReferenceRead{Source: k})
		}
	}
	return out
}
func graphCollect(t *testing.T, db *storage.DB, root string, id uuid.UUID, src graphSources, reads []storage.ReferenceRead, size int) []storage.ReferenceCandidate {
	t.Helper()
	it, err := db.NewReferenceIterator(root, id, src, reads, size)
	graphMust(t, err)
	var out []storage.ReferenceCandidate
	for {
		c, err := it.Next(context.Background())
		graphMust(t, err)
		if c == nil {
			return out
		}
		out = append(out, *c)
		if len(out) > 100 {
			t.Fatal("iterator did not terminate")
		}
	}
}

func TestReferenceGraphAllSourcesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "Основание", Kind: metadata.KindCatalog, Fields: []metadata.Field{graphString("Название")}}
		doc := &metadata.Entity{Name: "Документ", Kind: metadata.KindDocument, BasedOn: []string{target.Name}, Fields: []metadata.Field{graphRef("Основа", target.Name), graphRef("Другая", target.Name), graphString("Доступ")}, TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{graphRef("Основа", target.Name), graphRef("Другая", target.Name), graphString("Доступ")}}}}
		peer := &metadata.Entity{Name: "ЯДокумент", Kind: metadata.KindDocument, Fields: []metadata.Field{graphRef("Ссылка", target.Name)}}
		cat := &metadata.Entity{Name: "Ссылающийся", Kind: metadata.KindCatalog, Fields: []metadata.Field{graphRef("Ссылка", target.Name)}}
		reg := &metadata.Register{Name: "Накопление", Dimensions: []metadata.Field{graphRef("Измерение", target.Name)}, Resources: []metadata.Field{graphRef("Ресурс", target.Name)}, Attributes: []metadata.Field{graphRef("Атрибут", target.Name), graphString("Доступ")}}
		info := &metadata.InfoRegister{Name: "Сведения", Recorder: true, Dimensions: []metadata.Field{graphString("Ключ"), graphRef("Измерение", target.Name)}, Resources: []metadata.Field{graphRef("Ресурс", target.Name), graphString("Доступ")}}
		independent := &metadata.InfoRegister{Name: "Независимый", Dimensions: []metadata.Field{graphRef("Измерение", target.Name)}}
		account := &metadata.AccountRegister{Name: "Бухгалтерия", Accounts: "План", Resources: []metadata.Field{graphRef("Ресурс", target.Name)}, Subconto: []metadata.Field{graphRef("Партнёр", target.Name)}}
		src := graphSources{entities: []*metadata.Entity{target, doc, peer, cat}, regs: []*metadata.Register{reg}, info: []*metadata.InfoRegister{info, independent}, accounts: []*metadata.AccountRegister{account}}
		graphMust(t, db.Migrate(ctx, src.entities))
		graphMust(t, db.MigrateRegisters(ctx, src.regs))
		graphMust(t, db.MigrateInfoRegisters(ctx, src.info))
		graphMust(t, db.MigrateAccountRegisters(ctx, src.accounts))
		graphMust(t, db.EnsureAccountsTable(ctx))
		graphMust(t, db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{{Name: "План", Accounts: []metadata.Account{{Code: "01"}}}}))
		root := graphID(100)
		graphMust(t, db.Upsert(ctx, target.Name, root, map[string]any{"Название": "Корень"}, target))
		for i := 1; i <= 5; i++ {
			id := graphID(i)
			access := "да"
			if i == 1 {
				access = "нет"
			}
			graphMust(t, db.Upsert(ctx, doc.Name, id, map[string]any{"Основа": root, "Другая": root, "Доступ": access}, doc))
			rows := []map[string]any{{"Основа": root, "Другая": root, "Доступ": "строка"}, {"Основа": root, "Другая": root, "Доступ": "строка"}}
			graphMust(t, db.UpsertTablePartRows(ctx, doc.Name, "Строки", id, rows, doc.TableParts[0]))
			mov := map[string]any{"Измерение": root, "Ресурс": root, "Атрибут": root, "Доступ": access}
			graphMust(t, db.WriteMovements(ctx, reg.Name, doc.Name, id, []map[string]any{mov, mov}, reg, nil))
			graphMust(t, db.WriteInfoMovements(ctx, info.Name, doc.Name, id, []map[string]any{{"Ключ": fmt.Sprint(i), "Измерение": root, "Ресурс": root, "Доступ": access}}, info, nil))
			graphMust(t, db.WriteAccountMovements(ctx, account.Name, doc.Name, id, []map[string]any{{"СчётДт": "01", "СчётКт": "01", "Ресурс": root, "Субконто1": root}}, account, nil))
		}
		graphMust(t, db.Upsert(ctx, peer.Name, graphID(6), map[string]any{"Ссылка": root}, peer))
		graphMust(t, db.Upsert(ctx, cat.Name, graphID(7), map[string]any{"Ссылка": root}, cat))
		graphMust(t, db.InfoRegSet(ctx, independent, map[string]any{"Измерение": root}, nil, nil))
		// All exist physically and count for deletion; none can navigate to a card.
		for i, typ := range []string{"__synthetic_balance", "Неизвестный", target.Name, doc.Name} {
			if i < 3 {
				graphMust(t, db.Upsert(ctx, doc.Name, graphID(80+i), map[string]any{"Доступ": "нет"}, doc))
			}
			graphMust(t, db.WriteMovements(ctx, reg.Name, typ, graphID(80+i), []map[string]any{{"Измерение": root, "Ресурс": root, "Атрибут": root, "Доступ": "нет"}}, reg, nil))
			graphMust(t, db.WriteInfoMovements(ctx, info.Name, typ, graphID(80+i), []map[string]any{{"Ключ": fmt.Sprintf("bad-%d", i), "Измерение": root, "Ресурс": root, "Доступ": "нет"}}, info, nil))
			graphMust(t, db.WriteAccountMovements(ctx, account.Name, typ, graphID(80+i), []map[string]any{{"СчётДт": "01", "СчётКт": "01", "Ресурс": root, "Субконто1": root}}, account, nil))
		}
		// Even malformed recorder columns on an independent register cannot
		// turn its row into a card. IDs for wrong recorder types above point to
		// real documents, so dropping the type condition would expose extra keys.
		_, err := db.Exec(ctx, "UPDATE "+metadata.InfoRegTableName(independent.Name)+" SET recorder="+db.Dialect().Placeholder(1)+", recorder_type="+db.Dialect().Placeholder(2), graphID(80).String(), doc.Name)
		graphMust(t, err)
		reads := graphReads(t, target.Name, src, "incoming")
		out := graphCollect(t, db, target.Name, root, src, reads, 1)
		if len(out) != 7 {
			t.Fatalf("expected 7 unique cards, got %+v", out)
		}
		if out[0].Key.Entity != cat.Name || out[0].Key.ID != graphID(7) {
			t.Fatalf("kind ordering: %+v", out)
		}
		for i := 1; i <= 5; i++ {
			c := out[i]
			if c.Key.ID != graphID(i) || c.Key.Entity != doc.Name || !reflect.DeepEqual(c.Relations, []string{"based_on", "register"}) || len(c.Sources) != 11 {
				t.Fatalf("incomplete merged key: %+v", c)
			}
		}
		if !reflect.DeepEqual(out[6].Relations, []string{"reference"}) {
			t.Fatalf("reference classification: %+v", out[6])
		}
		// Shuffled registry/source order and different page size preserve output.
		reversed := src
		reversed.entities = []*metadata.Entity{cat, peer, doc, target}
		if got := graphCollect(t, db, target.Name, root, reversed, graphReads(t, target.Name, reversed, "incoming"), 2); !reflect.DeepEqual(out, got) {
			t.Fatalf("unstable merge: %+v", got)
		}
		refs, err := db.CheckRefs(ctx, target.Name, root, src)
		graphMust(t, err)
		if len(refs) != 14 {
			t.Fatalf("shared inventory lost a field: %+v", refs)
		}
		counts := map[string]int{}
		for _, r := range refs {
			counts[r.EntityName+"."+r.FieldName] = r.Count
		}
		for _, c := range out {
			for _, s := range c.Sources {
				label := s.Name
				switch s.Kind {
				case "register":
					label = "РегистрНакопления." + s.Name
				case "inforeg":
					label = "РегистрСведений." + s.Name
				case "accountreg":
					label = "РегистрБухгалтерии." + s.Name
				}
				if s.TablePart != "" {
					label += "." + s.TablePart
				}
				if counts[label+"."+s.Field] == 0 {
					t.Fatalf("graph source missing from CheckRefs: %+v", s)
				}
			}
		}
		// Each family filters before DISTINCT/paging/projection. For the table
		// part, the field Доступ exists on both tables; only owner Доступ is valid.
		for _, family := range []string{"header", "tablepart", "register", "inforeg", "accountreg"} {
			t.Run(family, func(t *testing.T) {
				var selected []storage.ReferenceRead
				for _, r := range reads {
					k := r.Source
					match := k.Projection == doc.Name && ((family == "header" && k.Name == doc.Name && k.TablePart == "") || (family == "tablepart" && k.Name == doc.Name && k.TablePart != "") || k.Kind == family)
					if !match {
						continue
					}
					r.Predicate = &storage.Predicate{Field: "Доступ", Value: "да"}
					if family == "accountreg" {
						r.Predicate = &storage.Predicate{Field: "регистратор", Op: "ne", Value: graphID(1)}
					}
					selected = append(selected, r)
				}
				if len(selected) == 0 {
					t.Fatal("missing test source")
				}
				got := graphCollect(t, db, target.Name, root, src, selected, 1)
				if len(got) != 4 || got[0].Key.ID != graphID(2) {
					t.Fatalf("source predicate not applied: %+v", got)
				}
			})
		}
		db.SetStrictRLSGuard(func(name string) bool { return name == strings.ToLower(doc.Name) })
		for _, r := range reads {
			if r.Source.Name != doc.Name {
				continue
			}
			if p, err := db.ReadReferencePage(ctx, target.Name, root, src, r, 1, ""); err == nil || len(p.Keys) != 0 {
				t.Fatal("strict RLS read bypassed", r.Source)
			}
			if _, err := db.NewReferenceIterator(target.Name, root, src, []storage.ReferenceRead{r}, 1); err == nil {
				t.Fatal("strict RLS iterator bypassed", r.Source)
			}
			r.RowFilterEvaluated = true
			r.Predicate = &storage.Predicate{Field: "Доступ", Value: "да"}
			if got := graphCollect(t, db, target.Name, root, src, []storage.ReferenceRead{r}, 1); len(got) != 4 {
				t.Fatal("evaluated policy lost", got)
			}
		}
		db.SetStrictRLSGuard(nil)
		// Empty allowlist grants nothing, even on an otherwise populated graph.
		if got := graphCollect(t, db, target.Name, root, src, nil, 1); len(got) != 0 {
			t.Fatal(got)
		}
		basis := graphCollect(t, db, doc.Name, graphID(1), src, graphReads(t, doc.Name, src, "basis"), 1)
		if len(basis) != 1 || basis[0].Key.ID != root || len(basis[0].Sources) != 4 {
			t.Fatalf("saved basis: %+v", basis)
		}
		deniedBasis := graphReads(t, doc.Name, src, "basis")
		for i := range deniedBasis {
			deniedBasis[i].Predicate = &storage.Predicate{Field: "Доступ", Value: "да"}
		}
		if got := graphCollect(t, db, doc.Name, graphID(1), src, deniedBasis, 1); len(got) != 0 {
			t.Fatalf("hidden owner exposed its basis: %+v", got)
		}
		if got := graphCollect(t, db, peer.Name, graphID(6), src, graphReads(t, peer.Name, src, "basis"), 1); len(got) != 0 {
			t.Fatalf("type without BasedOn fabricated a basis: %+v", got)
		}
		graphMust(t, db.Upsert(ctx, doc.Name, graphID(20), map[string]any{"Доступ": "да"}, doc))
		if got := graphCollect(t, db, doc.Name, graphID(20), src, graphReads(t, doc.Name, src, "basis"), 1); len(got) != 0 {
			t.Fatalf("BasedOn without saved reference fabricated a basis: %+v", got)
		}
		// Individual cursor is bound to root/field/predicate. Deleting a key
		// already read must not skip the next key as OFFSET would.
		var read storage.ReferenceRead
		for _, r := range reads {
			if r.Source.Name == doc.Name && r.Source.TablePart == "" {
				read = r
				break
			}
		}
		page, err := db.ReadReferencePage(ctx, target.Name, root, src, read, 1, "")
		graphMust(t, err)
		if len(page.Keys) != 1 || page.NextCursor == "" {
			t.Fatalf("first source page: %+v", page)
		}
		otherField := read
		if otherField.Source.Field == "Основа" {
			otherField.Source.Field = "Другая"
		} else {
			otherField.Source.Field = "Основа"
		}
		changed := read
		changed.Predicate = &storage.Predicate{Field: "Доступ", Value: "да"}
		for _, bad := range []struct {
			root   uuid.UUID
			read   storage.ReferenceRead
			cursor string
		}{{root, read, "broken"}, {root, changed, page.NextCursor}, {root, otherField, page.NextCursor}, {graphID(101), read, page.NextCursor}} {
			if p, err := db.ReadReferencePage(ctx, target.Name, bad.root, src, bad.read, 1, bad.cursor); err == nil || len(p.Keys) != 0 {
				t.Fatalf("invalid cursor accepted: %+v %v", p, err)
			}
		}
		graphMust(t, db.Delete(ctx, doc.Name, page.Keys[0].ID))
		second, err := db.ReadReferencePage(ctx, target.Name, root, src, read, 1, page.NextCursor)
		graphMust(t, err)
		if len(second.Keys) != 1 || second.Keys[0].ID != graphID(2) {
			t.Fatalf("delete skipped key: %+v", second)
		}
	})
}

func TestReferenceGraphFailClosedMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "Цель", Kind: metadata.KindCatalog}
		doc := &metadata.Entity{Name: "Источник", Kind: metadata.KindDocument, Fields: []metadata.Field{graphRef("Цель", target.Name)}}
		src := graphSources{entities: []*metadata.Entity{target, doc}}
		graphMust(t, db.Migrate(ctx, src.entities))
		reads := graphReads(t, target.Name, src, "incoming")
		for _, size := range []int{0, -1, 1001} {
			if _, err := db.NewReferenceIterator(target.Name, graphID(1), src, reads, size); err == nil {
				t.Fatal("unbounded page accepted")
			}
		}
		bad := append([]storage.ReferenceRead(nil), reads...)
		bad[0].Source.Field = "Цель; DROP TABLE источник"
		if _, err := db.NewReferenceIterator(target.Name, graphID(1), src, bad, 1); err == nil {
			t.Fatal("SQL source accepted")
		}
		bad = append([]storage.ReferenceRead(nil), reads...)
		bad[0].Predicate = &storage.Predicate{Field: "Неизвестное", Value: "x"}
		if _, err := db.NewReferenceIterator(target.Name, graphID(1), src, bad, 1); err == nil {
			t.Fatal("broken policy ignored")
		}
		if _, err := db.NewReferenceIterator(target.Name, graphID(1), src, append(reads, reads...), 1); err == nil {
			t.Fatal("duplicate source accepted")
		}
		// Missing column and missing table must preserve deletion fail-closed
		// and reject a graph rather than returning a partial candidate set.
		for _, broken := range []string{"column", "table"} {
			t.Run(broken, func(t *testing.T) {
				brokenSrc := src
				copyDoc := *doc
				copyDoc.Fields = append([]metadata.Field(nil), doc.Fields...)
				if broken == "column" {
					copyDoc.Fields[0].Name = "Отсутствует"
				} else {
					copyDoc.Name = "НетТаблицы"
				}
				brokenSrc.entities = []*metadata.Entity{target, &copyDoc}
				if refs, err := db.CheckRefs(ctx, target.Name, graphID(1), brokenSrc); err == nil || refs != nil {
					t.Fatalf("CheckRefs swallowed %s: %v %v", broken, refs, err)
				}
				it, err := db.NewReferenceIterator(target.Name, graphID(1), brokenSrc, graphReads(t, target.Name, brokenSrc, "incoming"), 1)
				graphMust(t, err)
				if c, err := it.Next(ctx); err == nil || c != nil {
					t.Fatalf("graph swallowed %s: %+v %v", broken, c, err)
				}
				if c, err := it.Next(ctx); err == nil || c != nil {
					t.Fatal("failed iterator resumed")
				}
			})
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		it, err := db.NewReferenceIterator(target.Name, graphID(1), src, reads, 1)
		graphMust(t, err)
		if c, err := it.Next(canceled); err == nil || c != nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("cancellation lost: %v %v", c, err)
		}
	})
}
