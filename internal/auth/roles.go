package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

var jsonMarshal = json.Marshal

// Permission holds allowed operations per entity kind.
type Permission struct {
	Catalogs  map[string][]string `yaml:"catalogs"`
	Documents map[string][]string `yaml:"documents"`
	Registers map[string][]string `yaml:"registers"`
	InfoRegs  map[string][]string `yaml:"inforegs"`
	Reports   map[string][]string `yaml:"reports"`
}

// Role is a named set of permissions.
type Role struct {
	ID          string
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Permissions Permission  `yaml:"permissions"`
}

// Has reports whether the user has permission for (kind, entity, op).
// kind: "catalog"|"document"|"register"|"inforeg"|"report"
// op:   "read"|"write"|"delete"|"post"|"unpost"|"run"
func (u *User) Has(kind, entity, op string) bool {
	if u.IsAdmin {
		return true
	}
	for _, r := range u.Roles {
		var m map[string][]string
		switch kind {
		case "catalog":
			m = r.Permissions.Catalogs
		case "document":
			m = r.Permissions.Documents
		case "register":
			m = r.Permissions.Registers
		case "inforeg":
			m = r.Permissions.InfoRegs
		case "report":
			m = r.Permissions.Reports
		}
		for _, allowed := range m[entity] {
			if allowed == op {
				return true
			}
		}
	}
	return false
}

// EnsureRolesSchema creates the _roles and _user_roles tables if they don't exist.
func (r *Repo) EnsureRolesSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _roles (
			id UUID PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			permissions JSONB NOT NULL DEFAULT '{}',
			updated_at TIMESTAMPTZ DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("auth: create _roles: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _user_roles (
			user_id UUID REFERENCES _users(id) ON DELETE CASCADE,
			role_id UUID REFERENCES _roles(id) ON DELETE CASCADE,
			PRIMARY KEY (user_id, role_id)
		)`)
	if err != nil {
		return fmt.Errorf("auth: create _user_roles: %w", err)
	}
	return nil
}

// SyncRoles upserts YAML roles into _roles table.
func (r *Repo) SyncRoles(ctx context.Context, roles []*Role) error {
	for _, role := range roles {
		permJSON, err := marshalPermissions(role.Permissions)
		if err != nil {
			return err
		}
		var id string
		err = r.pool.QueryRow(ctx,
			`INSERT INTO _roles (id, name, description, permissions, updated_at)
			 VALUES ($1, $2, $3, $4::jsonb, now())
			 ON CONFLICT (name) DO UPDATE SET description=$3, permissions=$4::jsonb, updated_at=now()
			 RETURNING id`,
			uuid.New().String(), role.Name, role.Description, permJSON,
		).Scan(&id)
		if err != nil {
			return fmt.Errorf("auth: sync role %s: %w", role.Name, err)
		}
		role.ID = id
	}
	return nil
}

// ListRoles returns all roles from the database.
func (r *Repo) ListRoles(ctx context.Context) ([]*Role, error) {
	rows, err := r.pool.Query(ctx,
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
	rows, err := r.pool.Query(ctx, `
		SELECT rl.id, rl.name, rl.description, rl.permissions
		FROM _roles rl
		JOIN _user_roles ur ON ur.role_id = rl.id
		WHERE ur.user_id = $1
		ORDER BY rl.name`, userID)
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
	rows, err := r.pool.Query(ctx,
		`SELECT role_id FROM _user_roles WHERE user_id = $1`, userID)
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
	_, err := r.pool.Exec(ctx,
		`INSERT INTO _user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, roleID)
	return err
}

// UnassignRole removes a role from a user.
func (r *Repo) UnassignRole(ctx context.Context, userID, roleID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM _user_roles WHERE user_id = $1 AND role_id = $2`, userID, roleID)
	return err
}

// LoadRolesYAML reads all *.yaml files from dir and returns Role slices.
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
		data, err := os.ReadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		var role Role
		if err := yaml.Unmarshal(data, &role); err != nil {
			return nil, fmt.Errorf("auth: parse role %s: %w", item.Name(), err)
		}
		roles = append(roles, &role)
	}
	return roles, nil
}

// marshalPermissions converts Permission to JSON string.
func marshalPermissions(p Permission) (string, error) {
	type permJSON struct {
		Catalogs  map[string][]string `json:"catalogs,omitempty"`
		Documents map[string][]string `json:"documents,omitempty"`
		Registers map[string][]string `json:"registers,omitempty"`
		InfoRegs  map[string][]string `json:"inforegs,omitempty"`
		Reports   map[string][]string `json:"reports,omitempty"`
	}
	b, err := jsonMarshal(permJSON{
		Catalogs:  p.Catalogs,
		Documents: p.Documents,
		Registers: p.Registers,
		InfoRegs:  p.InfoRegs,
		Reports:   p.Reports,
	})
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
	var raw struct {
		Catalogs  map[string][]string `json:"catalogs"`
		Documents map[string][]string `json:"documents"`
		Registers map[string][]string `json:"registers"`
		InfoRegs  map[string][]string `json:"inforegs"`
		Reports   map[string][]string `json:"reports"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Permission{}
	}
	return Permission{
		Catalogs:  raw.Catalogs,
		Documents: raw.Documents,
		Registers: raw.Registers,
		InfoRegs:  raw.InfoRegs,
		Reports:   raw.Reports,
	}
}
