package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

var jsonMarshal = json.Marshal

// Permission holds allowed operations per entity kind.
type Permission struct {
	AIDataAccess bool                `yaml:"ai_data_access"`
	Catalogs     map[string][]string `yaml:"catalogs"`
	Documents    map[string][]string `yaml:"documents"`
	Registers    map[string][]string `yaml:"registers"`
	InfoRegs     map[string][]string `yaml:"inforegs"`
	Reports      map[string][]string `yaml:"reports"`
	Processors   map[string][]string `yaml:"processors"`
	RowAccess    RowAccess           `yaml:"row_access"`
	FieldAccess  FieldAccess         `yaml:"field_access"`
}

// Role is a named set of permissions.
type Role struct {
	ID          string
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Permissions Permission `yaml:"permissions"`
}

func (p *Permission) UnmarshalYAML(value *yaml.Node) error {
	type plain Permission
	var canonical plain
	if err := value.Decode(&canonical); err != nil {
		return err
	}
	parsed := normalizePermission(Permission(canonical))
	if value.Kind == yaml.MappingNode {
		parsed = mergePermissions(parsed, permissionFromYAMLMap(value))
	}
	*p = parsed
	return nil
}

// Has reports whether the user has permission for (kind, entity, op).
// kind: "catalog"|"document"|"register"|"inforeg"|"report"|"processor"
// op:   "read"|"write"|"delete"|"post"|"unpost"|"run"
func (u *User) Has(kind, entity, op string) bool {
	if u.IsAdmin {
		return true
	}
	// Обработки используют opt-in семантику ради обратной совместимости: роль,
	// которая НЕ объявляет секцию `processors` (map == nil), разрешает все
	// обработки (прежнее поведение, когда прав на обработки не существовало).
	// Роль с объявленной секцией ограничивает доступ перечисленными. Пустой
	// `processors: {}` (non-nil) запрещает все обработки.
	if kind == "processor" {
		for _, r := range u.Roles {
			if r.Permissions.Processors == nil {
				return true
			}
			for _, allowed := range r.Permissions.Processors[entity] {
				if permissionOpMatches(allowed, op) {
					return true
				}
			}
		}
		return false
	}
	for _, r := range u.Roles {
		if PermissionHas(r.Permissions, kind, entity, op) {
			return true
		}
	}
	return false
}

func PermissionHas(p Permission, kind, entity, op string) bool {
	var m map[string][]string
	switch kind {
	case "catalog":
		m = p.Catalogs
	case "document":
		m = p.Documents
	case "register":
		m = p.Registers
	case "inforeg":
		m = p.InfoRegs
	case "report":
		m = p.Reports
	case "processor":
		if p.Processors == nil {
			return true
		}
		m = p.Processors
	}
	for _, allowed := range m[entity] {
		if permissionOpMatches(allowed, op) {
			return true
		}
	}
	return false
}

func permissionOpMatches(allowed, op string) bool {
	for _, item := range splitPermissionOps(allowed) {
		if strings.EqualFold(item, op) {
			return true
		}
	}
	return false
}

func splitPermissionOps(raw string) []string {
	parts := strings.Split(raw, ",")
	ops := make([]string, 0, len(parts))
	for _, part := range parts {
		op := strings.ToLower(strings.TrimSpace(part))
		if op != "" {
			ops = append(ops, op)
		}
	}
	return ops
}

func normalizePermission(p Permission) Permission {
	return Permission{
		AIDataAccess: p.AIDataAccess,
		Catalogs:     normalizePermissionMap(p.Catalogs),
		Documents:    normalizePermissionMap(p.Documents),
		Registers:    normalizePermissionMap(p.Registers),
		InfoRegs:     normalizePermissionMap(p.InfoRegs),
		Reports:      normalizePermissionMap(p.Reports),
		Processors:   normalizePermissionMap(p.Processors),
		RowAccess:    normalizeRowAccess(p.RowAccess),
		FieldAccess:  normalizeFieldAccess(p.FieldAccess),
	}
}

func normalizePermissionMap(m map[string][]string) map[string][]string {
	if m == nil {
		return nil
	}
	out := make(map[string][]string, len(m))
	for entity, ops := range m {
		seen := make(map[string]bool, len(ops))
		for _, raw := range ops {
			for _, op := range splitPermissionOps(raw) {
				if seen[op] {
					continue
				}
				out[entity] = append(out[entity], op)
				seen[op] = true
			}
		}
	}
	return out
}

func mergePermissions(dst, src Permission) Permission {
	if src.AIDataAccess {
		dst.AIDataAccess = true
	}
	dst.Catalogs = mergePermissionMap(dst.Catalogs, src.Catalogs)
	dst.Documents = mergePermissionMap(dst.Documents, src.Documents)
	dst.Registers = mergePermissionMap(dst.Registers, src.Registers)
	dst.InfoRegs = mergePermissionMap(dst.InfoRegs, src.InfoRegs)
	dst.Reports = mergePermissionMap(dst.Reports, src.Reports)
	dst.Processors = mergePermissionMap(dst.Processors, src.Processors)
	dst.RowAccess = mergeRowAccess(dst.RowAccess, src.RowAccess)
	dst.FieldAccess = mergeFieldAccess(dst.FieldAccess, src.FieldAccess)
	return normalizePermission(dst)
}

func mergePermissionMap(dst, src map[string][]string) map[string][]string {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = make(map[string][]string, len(src))
	}
	for entity, ops := range src {
		dst[entity] = append(dst[entity], ops...)
	}
	return dst
}

func permissionFromYAMLMap(node *yaml.Node) Permission {
	var p Permission
	if node == nil || node.Kind != yaml.MappingNode {
		return p
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		value := node.Content[i+1]
		switch {
		case permissionBoolKey(key):
			if b, ok := yamlBool(value); ok {
				p.AIDataAccess = p.AIDataAccess || b
			}
		case permissionWrapperKey(key):
			p = mergePermissions(p, permissionFromYAMLMap(value))
		case rowAccessKey(key):
			var ra RowAccess
			if err := value.Decode(&ra); err == nil {
				p.RowAccess = mergeRowAccess(p.RowAccess, ra)
			}
		case fieldAccessKey(key):
			var fa FieldAccess
			if err := value.Decode(&fa); err == nil {
				p.FieldAccess = mergeFieldAccess(p.FieldAccess, fa)
			}
		default:
			if kind := permissionKindFromKey(key); kind != "" {
				setPermissionMap(&p, kind, decodeYAMLPermissionMap(value))
			}
		}
	}
	return normalizePermission(p)
}

func decodeYAMLPermissionMap(node *yaml.Node) map[string][]string {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string][]string, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		entity := node.Content[i].Value
		value := node.Content[i+1]
		switch value.Kind {
		case yaml.SequenceNode:
			for _, item := range value.Content {
				out[entity] = append(out[entity], splitPermissionOps(item.Value)...)
			}
		case yaml.ScalarNode:
			out[entity] = append(out[entity], splitPermissionOps(value.Value)...)
		}
	}
	return normalizePermissionMap(out)
}

func yamlBool(node *yaml.Node) (bool, bool) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(node.Value)) {
	case "true", "1", "yes", "y", "да", "истина":
		return true, true
	case "false", "0", "no", "n", "нет", "ложь":
		return false, true
	default:
		return false, false
	}
}

// AllowsAIDataAccess reports whether the user may receive AI chat data tools
// when the database-level ai.data_scope allows non-admin access. User-level
// AIDataAccess remains supported; role-level ai_data_access makes demo and
// template roles declarative.
func (u *User) AllowsAIDataAccess() bool {
	if u == nil {
		return false
	}
	if u.IsAdmin || u.AIDataAccess {
		return true
	}
	for _, r := range u.Roles {
		if r.Permissions.AIDataAccess {
			return true
		}
	}
	return false
}

// HasAnyRole reports whether the user holds at least one of the named roles
// (case-insensitive). Admins always pass. Used to gate HTTP-services declaring
// a `roles:` list (план 61).
func (u *User) HasAnyRole(names []string) bool {
	if u.IsAdmin {
		return true
	}
	for _, want := range names {
		for _, r := range u.Roles {
			if strings.EqualFold(r.Name, want) {
				return true
			}
		}
	}
	return false
}

// EnsureRolesSchema creates the _roles and _user_roles tables if they don't exist.
func (r *Repo) EnsureRolesSchema(ctx context.Context) error {
	d := r.db.Dialect()
	rolesDDL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _roles (
			id %s PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			permissions %s NOT NULL DEFAULT '{}',
			updated_at %s DEFAULT %s
		)`, d.TypeUUID(), d.TypeJSON(), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := r.db.Exec(ctx, rolesDDL); err != nil {
		return fmt.Errorf("auth: create _roles: %w", err)
	}
	userRolesDDL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _user_roles (
			user_id %s REFERENCES _users(id) ON DELETE CASCADE,
			role_id %s REFERENCES _roles(id) ON DELETE CASCADE,
			PRIMARY KEY (user_id, role_id)
		)`, d.TypeUUID(), d.TypeUUID())
	if _, err := r.db.Exec(ctx, userRolesDDL); err != nil {
		return fmt.Errorf("auth: create _user_roles: %w", err)
	}
	return nil
}

// SyncRoles upserts YAML roles into _roles table.
func (r *Repo) SyncRoles(ctx context.Context, roles []*Role) error {
	d := r.db.Dialect()
	jc := d.JSONCast()
	for _, role := range roles {
		permJSON, err := marshalPermissions(role.Permissions)
		if err != nil {
			return err
		}
		q := fmt.Sprintf(
			`INSERT INTO _roles (id, name, description, permissions, updated_at)
			 VALUES (%s, %s, %s, %s%s, %s)
			 ON CONFLICT (name) DO UPDATE SET description=EXCLUDED.description, permissions=EXCLUDED.permissions, updated_at=%s`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), jc, d.Now(), d.Now())
		newID := uuid.New().String()
		if _, err := r.db.Exec(ctx, q, newID, role.Name, role.Description, permJSON); err != nil {
			return fmt.Errorf("auth: sync role %s: %w", role.Name, err)
		}
		var id string
		qSel := fmt.Sprintf(`SELECT id FROM _roles WHERE name=%s`, d.Placeholder(1))
		if err := r.db.QueryRow(ctx, qSel, role.Name).Scan(&id); err != nil {
			return fmt.Errorf("auth: read role id %s: %w", role.Name, err)
		}
		role.ID = id
	}
	return nil
}

// ListRoles returns all roles from the database.
func (r *Repo) ListRoles(ctx context.Context) ([]*Role, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, name, description, permissions FROM _roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []*Role
	for rows.Next() {
		role := &Role{}
		var permJSON []byte
		if err := rows.Scan(&role.ID, &role.Name, &role.Description, &permJSON); err != nil {
			return nil, err
		}
		role.Permissions = unmarshalPermissions(permJSON)
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// GetRolesForUser loads all roles assigned to a user.
func (r *Repo) GetRolesForUser(ctx context.Context, userID string) ([]*Role, error) {
	d := r.db.Dialect()
	q := fmt.Sprintf(`
		SELECT rl.id, rl.name, rl.description, rl.permissions
		FROM _roles rl
		JOIN _user_roles ur ON ur.role_id = rl.id
		WHERE ur.user_id = %s
		ORDER BY rl.name`, d.Placeholder(1))
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []*Role
	for rows.Next() {
		role := &Role{}
		var permJSON []byte
		if err := rows.Scan(&role.ID, &role.Name, &role.Description, &permJSON); err != nil {
			return nil, err
		}
		role.Permissions = unmarshalPermissions(permJSON)
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// GetUserRoleIDs returns the set of role IDs assigned to a user.
func (r *Repo) GetUserRoleIDs(ctx context.Context, userID string) (map[string]bool, error) {
	d := r.db.Dialect()
	q := fmt.Sprintf(`SELECT role_id FROM _user_roles WHERE user_id = %s`, d.Placeholder(1))
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

// AssignRole assigns a role to a user (idempotent).
func (r *Repo) AssignRole(ctx context.Context, userID, roleID string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(
		`INSERT INTO _user_roles (user_id, role_id) VALUES (%s, %s) ON CONFLICT DO NOTHING`,
		d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, userID, roleID)
	return err
}

// UnassignRole removes a role from a user.
func (r *Repo) UnassignRole(ctx context.Context, userID, roleID string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _user_roles WHERE user_id = %s AND role_id = %s`,
		d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, userID, roleID)
	return err
}

// DeleteRoleByName removes a role and (via ON DELETE CASCADE) its assignments.
func (r *Repo) DeleteRoleByName(ctx context.Context, name string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _roles WHERE name = %s`, d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, name)
	return err
}

// LoadRolesYAML reads all *.yaml files from dir and returns Role slices.
// LoadRoleFile парсит один YAML-файл роли.
func LoadRoleFile(path string) (*Role, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var role Role
	if err := yaml.Unmarshal(data, &role); err != nil {
		return nil, fmt.Errorf("auth: parse role %s: %w", filepath.Base(path), err)
	}
	return &role, nil
}

func LoadRolesYAML(dir string) ([]*Role, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: readdir roles %s: %w", dir, err)
	}
	var roles []*Role
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		role, err := LoadRoleFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// marshalPermissions converts Permission to JSON string.
func marshalPermissions(p Permission) (string, error) {
	p = normalizePermission(p)
	type permJSON struct {
		AIDataAccess bool                `json:"ai_data_access,omitempty"`
		Catalogs     map[string][]string `json:"catalogs,omitempty"`
		Documents    map[string][]string `json:"documents,omitempty"`
		Registers    map[string][]string `json:"registers,omitempty"`
		InfoRegs     map[string][]string `json:"inforegs,omitempty"`
		Reports      map[string][]string `json:"reports,omitempty"`
		Processors   map[string][]string `json:"processors"`
		RowAccess    RowAccess           `json:"row_access,omitempty"`
		FieldAccess  FieldAccess         `json:"field_access,omitempty"`
	}
	b, err := jsonMarshal(permJSON(p))
	if err != nil {
		return "{}", err
	}
	return string(b), nil
}

// unmarshalPermissions parses JSONB permissions from the database.
func unmarshalPermissions(data []byte) Permission {
	if len(data) == 0 {
		return Permission{}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Permission{}
	}
	return normalizePermission(permissionFromJSONMap(raw))
}

func permissionFromJSONMap(raw map[string]json.RawMessage) Permission {
	var p Permission
	for key, value := range raw {
		switch {
		case permissionBoolKey(key):
			var b bool
			if err := json.Unmarshal(value, &b); err == nil && b {
				p.AIDataAccess = true
			}
		case permissionWrapperKey(key):
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(value, &nested); err == nil {
				p = mergePermissions(p, permissionFromJSONMap(nested))
			}
		case rowAccessKey(key):
			var ra RowAccess
			if err := json.Unmarshal(value, &ra); err == nil {
				p.RowAccess = mergeRowAccess(p.RowAccess, ra)
			}
		case fieldAccessKey(key):
			var fa FieldAccess
			if err := json.Unmarshal(value, &fa); err == nil {
				p.FieldAccess = mergeFieldAccess(p.FieldAccess, fa)
			}
		default:
			if kind := permissionKindFromKey(key); kind != "" {
				if m, ok := decodeJSONPermissionMap(value); ok {
					setPermissionMap(&p, kind, m)
				}
			}
		}
	}
	return normalizePermission(p)
}

func decodeJSONPermissionMap(data json.RawMessage) (map[string][]string, bool) {
	if strings.EqualFold(strings.TrimSpace(string(data)), "null") {
		return nil, true
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	out := make(map[string][]string, len(raw))
	for entity, value := range raw {
		out[entity] = append(out[entity], decodeJSONPermissionOps(value)...)
	}
	return normalizePermissionMap(out), true
}

func decodeJSONPermissionOps(data json.RawMessage) []string {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		var out []string
		for _, raw := range list {
			out = append(out, splitPermissionOps(raw)...)
		}
		return out
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		return splitPermissionOps(single)
	}
	return nil
}

func setPermissionMap(p *Permission, kind string, m map[string][]string) {
	switch kind {
	case "catalog":
		p.Catalogs = mergePermissionMap(p.Catalogs, m)
	case "document":
		p.Documents = mergePermissionMap(p.Documents, m)
	case "register":
		p.Registers = mergePermissionMap(p.Registers, m)
	case "inforeg":
		p.InfoRegs = mergePermissionMap(p.InfoRegs, m)
	case "report":
		p.Reports = mergePermissionMap(p.Reports, m)
	case "processor":
		p.Processors = mergePermissionMap(p.Processors, m)
	}
}

func permissionBoolKey(key string) bool {
	switch normalizePermissionKey(key) {
	case "aidataaccess", "aiдоступкданным", "доступкданнымии":
		return true
	default:
		return false
	}
}

func permissionWrapperKey(key string) bool {
	switch normalizePermissionKey(key) {
	case "permissions", "permission", "policies", "policy", "политики", "права":
		return true
	default:
		return false
	}
}

func permissionKindFromKey(key string) string {
	switch normalizePermissionKey(key) {
	case "catalog", "catalogs", "справочник", "справочники":
		return "catalog"
	case "document", "documents", "документ", "документы":
		return "document"
	case "register", "registers", "accumulationregister", "accumulationregisters",
		"регистр", "регистры", "регистрнакопления", "регистрынакопления", "регистрынакоплений",
		"регистрынакопленияибухгалтерии":
		return "register"
	case "inforeg", "inforegs", "inforegister", "inforegisters", "informationregister", "informationregisters",
		"регистрсведений", "регистрысведений", "регистрысведения":
		return "inforeg"
	case "report", "reports", "отчет", "отчеты":
		return "report"
	case "processor", "processors", "обработка", "обработки":
		return "processor"
	default:
		return ""
	}
}

func normalizePermissionKey(key string) string {
	key = strings.ToLower(strings.ReplaceAll(key, "ё", "е"))
	var b strings.Builder
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
