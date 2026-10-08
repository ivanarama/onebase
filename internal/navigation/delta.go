package navigation

// Layer identifies the owner of a navigation override. Runtime access remains
// the caller's responsibility; a layout never grants access to metadata targets.
type Layer string

const (
	AdminLayer    Layer = "admin"
	UserLayer     Layer = "user"
	MaxDeltaBytes       = 64 << 10
	MaxOperations       = 1000
)

// Delta stores changes relative to the previous layer rather than a snapshot.
// BaseHash detects configuration changes; valid operations can still be applied
// when the base changes, with removed references reported as stale diagnostics.
type Delta struct {
	Version  int         `json:"version"`
	BaseHash string      `json:"base_hash"`
	Ops      []Operation `json:"ops"`
}

// Operation addresses stable node IDs. ID is used only when adding a custom
// section/group; Node addresses an existing node. Empty Parent denotes the root,
// and empty After denotes the beginning of the appropriate sibling list.
// Pointers distinguish an explicit empty title/icon from an omitted field.
type Operation struct {
	Op     string  `json:"op"`
	Node   string  `json:"node,omitempty"`
	ID     string  `json:"id,omitempty"`
	Parent string  `json:"parent,omitempty"`
	After  string  `json:"after,omitempty"`
	Title  *string `json:"title,omitempty"`
	Icon   *string `json:"icon,omitempty"`
}
