package storage

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ReferenceSourceKey identifies one physical field and its projected entity.
// Registers have a separate stream for each possible document type. Splitting
// them keeps SQL ordering independent of database collation and canonical names.
// Direction is "incoming" or "basis". Names are resolved against metadata;
// none of these strings are accepted as raw SQL identifiers.
type ReferenceSourceKey struct {
	Direction  string
	Kind       string
	Name       string
	TablePart  string
	Field      string
	Projection string
}

// ReferenceRead is an explicit source/field allowlist entry. Omitted sources
// are never read. A nil Predicate means unrestricted rows for this entry only.
// The caller must authorize the field and both recorder components before
// including a register entry. Target RBAC/RLS and the visible-node limit belong
// to the caller; these raw keys must not be exposed directly to a user.
type ReferenceRead struct {
	Source    ReferenceSourceKey
	Predicate *Predicate
	// RowFilterEvaluated is required for guarded entity/owner sources in strict RLS mode.
	RowFilterEvaluated bool
}

type ReferenceNodeKey struct {
	Kind   metadata.Kind
	Entity string
	ID     uuid.UUID
}

// ReferenceCandidate is one unique projected key, with complete relations and
// provenance across all selected sources. Relations are sorted lexically.
type ReferenceCandidate struct {
	Key       ReferenceNodeKey
	Relations []string
	Sources   []ReferenceSourceKey
}

// ReferencePage is bounded per SQL source, not by accessible target count.
// NextCursor is empty only when this source is exhausted.
type ReferencePage struct {
	Keys       []ReferenceNodeKey
	Relation   string
	NextCursor string
}

type referenceProjection struct {
	physical referenceSource
	key      ReferenceSourceKey
	node     *metadata.Entity
	relation string
}

func referenceEntity(name string, src RefSources) (*metadata.Entity, error) {
	if src == nil {
		return nil, fmt.Errorf("reference sources are required")
	}
	var found *metadata.Entity
	for _, e := range src.Entities() {
		if e.Name != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("ambiguous reference entity %q", name)
		}
		found = e
	}
	if found == nil || (found.Kind != metadata.KindDocument && found.Kind != metadata.KindCatalog) {
		return nil, fmt.Errorf("unknown reference entity %q", name)
	}
	return found, nil
}

func referenceBasedOn(e *metadata.Entity, target string) bool {
	if e.Kind != metadata.KindDocument {
		return false
	}
	for _, name := range e.BasedOn {
		if name == target {
			return true
		}
	}
	return false
}

func referenceProjections(rootName string, src RefSources) ([]referenceProjection, error) {
	root, err := referenceEntity(rootName, src)
	if err != nil {
		return nil, err
	}
	var out []referenceProjection
	add := func(s referenceSource, node *metadata.Entity, direction, relation string) {
		out = append(out, referenceProjection{s, ReferenceSourceKey{direction, s.kind, s.name, s.part, s.field.Name, node.Name}, node, relation})
	}
	for _, s := range referenceSources(root.Name, src) {
		if !s.navigable {
			continue
		}
		if s.entity != nil {
			relation := "reference"
			if referenceBasedOn(s.entity, root.Name) {
				relation = "based_on"
			}
			add(s, s.entity, "incoming", relation)
		} else {
			for _, e := range src.Entities() {
				if e.Kind == metadata.KindDocument {
					add(s, e, "incoming", "register")
				}
			}
		}
	}
	// Only actual reference fields whose target is an allowed BasedOn type are
	// inspected. Matching types alone never fabricate an instance relationship.
	for _, target := range src.Entities() {
		if !referenceBasedOn(root, target.Name) {
			continue
		}
		for _, s := range referenceSources(target.Name, src) {
			if s.entity != nil && s.entity.Name == root.Name {
				add(s, target, "basis", "based_on")
			}
		}
	}
	seen := make(map[ReferenceSourceKey]bool)
	for _, s := range out {
		if seen[s.key] {
			return nil, fmt.Errorf("ambiguous reference source %+v", s.key)
		}
		seen[s.key] = true
	}
	sort.Slice(out, func(i, j int) bool { return referenceSourceLess(out[i].key, out[j].key) })
	return out, nil
}

// ReferenceSources lists navigable source fields for incoming references and
// saved BasedOn links. It does no SQL and grants no access. An independent
// information register still participates in CheckRefs, but has no graph node.
func ReferenceSources(rootName string, src RefSources) ([]ReferenceSourceKey, error) {
	projections, err := referenceProjections(rootName, src)
	if err != nil {
		return nil, err
	}
	out := make([]ReferenceSourceKey, len(projections))
	for i, s := range projections {
		out[i] = s.key
	}
	return out, nil
}

func referenceSourceLess(a, b ReferenceSourceKey) bool {
	x, y := [6]string{a.Direction, a.Kind, a.Name, a.TablePart, a.Field, a.Projection}, [6]string{b.Direction, b.Kind, b.Name, b.TablePart, b.Field, b.Projection}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func referenceNodeLess(a, b ReferenceNodeKey) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Entity != b.Entity {
		return a.Entity < b.Entity
	}
	return a.ID.String() < b.ID.String()
}

func (db *DB) referenceSQL(rootID uuid.UUID, s referenceProjection, predicate *Predicate) (string, []any, error) {
	d := db.dialect
	p := s.physical
	args := []any{idArg(d, rootID)}
	from := p.table + " r"
	qualifier := "r"
	idColumn := "r.id"
	where := "r." + p.column + " = " + d.Placeholder(1)
	if p.part != "" {
		from += " JOIN " + metadata.TableName(p.entity.Name) + " o ON o.id = r.parent_id"
		qualifier = "o"
		idColumn = "o.id"
	}
	if p.recorder != "" {
		from += " JOIN " + metadata.TableName(s.node.Name) + " n ON n.id = r." + p.recorder
		args = append(args, s.node.Name)
		where += " AND r." + p.recorderType + " = " + d.Placeholder(len(args))
		idColumn = "n.id"
	}
	if s.key.Direction == "basis" {
		ownerColumn := "r.id"
		if p.part != "" {
			ownerColumn = "o.id"
		}
		where = ownerColumn + " = " + d.Placeholder(1)
		from += " JOIN " + metadata.TableName(s.node.Name) + " n ON n.id = r." + p.column
		idColumn = "n.id"
	}
	cond, condArgs, _, err := PredicateSQLQualified(d, p.predicateEntity, predicate, len(args)+1, qualifier)
	if err != nil {
		return "", nil, err
	}
	if cond != "" {
		where += " AND (" + cond + ")"
		args = append(args, condArgs...)
	}
	// Empty UUID never identifies a navigable record, even in malformed data.
	args = append(args, idArg(d, uuid.Nil))
	where += " AND " + idColumn + " <> " + d.Placeholder(len(args))
	return "SELECT DISTINCT " + idColumn + " AS node_id FROM " + from + " WHERE " + where, args, nil
}

type referenceCursor struct {
	Version int
	Query   string
	After   uuid.UUID
}

// ReadReferencePage reads a single authorized field/projection with a keyset
// cursor bound to root, source and compiled predicate. It checks rows.Err and
// returns no partial page on query, Scan, iteration or cursor errors.
func (db *DB) ReadReferencePage(ctx context.Context, rootName string, rootID uuid.UUID, src RefSources, read ReferenceRead, pageSize int, cursor string) (ReferencePage, error) {
	if rootID == uuid.Nil || pageSize < 1 || pageSize > 1000 {
		return ReferencePage{}, fmt.Errorf("invalid reference root or page size")
	}
	projections, err := referenceProjections(rootName, src)
	if err != nil {
		return ReferencePage{}, err
	}
	var selected *referenceProjection
	for i := range projections {
		if projections[i].key == read.Source {
			selected = &projections[i]
			break
		}
	}
	if selected == nil {
		return ReferencePage{}, fmt.Errorf("unknown reference source %+v", read.Source)
	}
	return db.readReferencePage(ctx, rootName, rootID, *selected, read, pageSize, cursor)
}

func (db *DB) referenceReadGuard(s referenceProjection, read ReferenceRead) error {
	if s.physical.entity != nil && db.rlsGuard != nil && !read.RowFilterEvaluated && db.rlsGuard(strings.ToLower(s.physical.entity.Name)) {
		return fmt.Errorf("reference source %s: row access was not evaluated", s.physical.entity.Name)
	}
	return nil
}

func (db *DB) readReferencePage(ctx context.Context, rootName string, rootID uuid.UUID, selected referenceProjection, read ReferenceRead, pageSize int, cursor string) (ReferencePage, error) {
	if err := db.referenceReadGuard(selected, read); err != nil {
		return ReferencePage{}, err
	}
	query, args, err := db.referenceSQL(rootID, selected, read.Predicate)
	if err != nil {
		return ReferencePage{}, err
	}
	identity, err := json.Marshal([]any{rootName, rootID, read.Source, query, args})
	if err != nil {
		return ReferencePage{}, fmt.Errorf("reference cursor identity: %w", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(identity))
	if cursor != "" {
		if len(cursor) > 1400 {
			return ReferencePage{}, fmt.Errorf("invalid reference cursor")
		}
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(data) > 1024 {
			return ReferencePage{}, fmt.Errorf("invalid reference cursor")
		}
		var c referenceCursor
		if err = json.Unmarshal(data, &c); err != nil || c.Version != 1 || c.Query != digest || c.After == uuid.Nil {
			return ReferencePage{}, fmt.Errorf("reference cursor does not match query")
		}
		args = append(args, idArg(db.dialect, c.After))
		// All projected SQL identifiers come from this subquery, never the cursor.
		query = "SELECT node_id FROM (" + query + ") candidates WHERE node_id > " + db.dialect.Placeholder(len(args))
	}
	args = append(args, pageSize+1)
	query += " ORDER BY node_id LIMIT " + db.dialect.Placeholder(len(args))
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return ReferencePage{}, fmt.Errorf("reference source %+v: %w", read.Source, err)
	}
	defer rows.Close()
	page := ReferencePage{Relation: selected.relation}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return ReferencePage{}, err
		}
		page.Keys = append(page.Keys, ReferenceNodeKey{Kind: selected.node.Kind, Entity: selected.node.Name, ID: id})
	}
	if err := rows.Err(); err != nil {
		return ReferencePage{}, err
	}
	if len(page.Keys) > pageSize {
		page.Keys = page.Keys[:pageSize]
		data, err := json.Marshal(referenceCursor{1, digest, page.Keys[pageSize-1].ID})
		if err != nil {
			return ReferencePage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

// ReferenceIterator performs bounded k-way merge. Next returns one complete
// unique key; callers may reject it by target policy and continue until they
// find enough accessible keys. There is no global visible-node limit here.
// It holds no SQL cursors between calls. Any error poisons the iterator.
type ReferenceIterator struct {
	db       *DB
	rootName string
	rootID   uuid.UUID
	pageSize int
	streams  []*referenceStream
	queue    referenceHeap
	started  bool
	err      error
}

type referenceStream struct {
	projection referenceProjection
	read       ReferenceRead
	page       ReferencePage
	pos        int
	index      int
}

type referenceHeap []*referenceStream

func (h referenceHeap) Len() int { return len(h) }
func (h referenceHeap) Less(i, j int) bool {
	a, b := h[i].page.Keys[h[i].pos], h[j].page.Keys[h[j].pos]
	if a != b {
		return referenceNodeLess(a, b)
	}
	return h[i].index < h[j].index
}
func (h referenceHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *referenceHeap) Push(x any)   { *h = append(*h, x.(*referenceStream)) }
func (h *referenceHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return x
}

// NewReferenceIterator validates an explicit source allowlist and prepares a
// merge of bounded SQL streams. No SQL is executed until Next.
func (db *DB) NewReferenceIterator(rootName string, rootID uuid.UUID, src RefSources, reads []ReferenceRead, pageSize int) (*ReferenceIterator, error) {
	if rootID == uuid.Nil || pageSize < 1 || pageSize > 1000 {
		return nil, fmt.Errorf("invalid reference root or page size")
	}
	projections, err := referenceProjections(rootName, src)
	if err != nil {
		return nil, err
	}
	known := make(map[ReferenceSourceKey]referenceProjection, len(projections))
	for _, s := range projections {
		known[s.key] = s
	}
	ordered := append([]ReferenceRead(nil), reads...)
	sort.Slice(ordered, func(i, j int) bool { return referenceSourceLess(ordered[i].Source, ordered[j].Source) })
	it := &ReferenceIterator{db: db, rootName: rootName, rootID: rootID, pageSize: pageSize}
	seen := make(map[ReferenceSourceKey]bool)
	for i, r := range ordered {
		s, ok := known[r.Source]
		if !ok || seen[r.Source] {
			return nil, fmt.Errorf("unknown or duplicate reference source %+v", r.Source)
		}
		seen[r.Source] = true
		// Compile all policies before the first SQL read, including empty sources.
		if err := db.referenceReadGuard(s, r); err != nil {
			return nil, err
		}
		if _, _, err := db.referenceSQL(rootID, s, r.Predicate); err != nil {
			return nil, err
		}
		it.streams = append(it.streams, &referenceStream{read: r, index: i, projection: s})
	}
	return it, nil
}

func (it *ReferenceIterator) load(ctx context.Context, s *referenceStream, cursor string) error {
	page, err := it.db.readReferencePage(ctx, it.rootName, it.rootID, s.projection, s.read, it.pageSize, cursor)
	if err != nil {
		return err
	}
	s.page = page
	s.pos = 0
	if len(page.Keys) > 0 {
		heap.Push(&it.queue, s)
	}
	return nil
}

func (it *ReferenceIterator) Next(ctx context.Context) (*ReferenceCandidate, error) {
	if it.err != nil {
		return nil, it.err
	}
	if err := ctx.Err(); err != nil {
		it.err = err
		return nil, err
	}
	if !it.started {
		it.started = true
		for _, s := range it.streams {
			if err := it.load(ctx, s, ""); err != nil {
				it.err = err
				return nil, err
			}
		}
	}
	if len(it.queue) == 0 {
		return nil, nil
	}
	key := it.queue[0].page.Keys[it.queue[0].pos]
	out := &ReferenceCandidate{Key: key}
	relations := make(map[string]bool)
	for len(it.queue) > 0 && it.queue[0].page.Keys[it.queue[0].pos] == key {
		s := heap.Pop(&it.queue).(*referenceStream)
		relations[s.page.Relation] = true
		out.Sources = append(out.Sources, s.read.Source)
		s.pos++
		if s.pos < len(s.page.Keys) {
			heap.Push(&it.queue, s)
		} else if s.page.NextCursor != "" {
			if err := it.load(ctx, s, s.page.NextCursor); err != nil {
				it.err = err
				return nil, err
			}
		}
	}
	for relation := range relations {
		out.Relations = append(out.Relations, relation)
	}
	sort.Strings(out.Relations)
	return out, nil
}
