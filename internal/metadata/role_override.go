package metadata

// RoleOverride narrows visibility or changes presentation for matching users.
// Explicit exclusions win over include_admin. Entries apply in document order.
type RoleOverride struct {
	Roles        []string `yaml:"roles,omitempty"`
	ExceptRoles  []string `yaml:"except_roles,omitempty"`
	IncludeAdmin bool     `yaml:"include_admin,omitempty"`
	Background   *string  `yaml:"background,omitempty"`
	Visible      *bool    `yaml:"visible,omitempty"`
}
