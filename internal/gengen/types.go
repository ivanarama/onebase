// Package gengen implements generative project configuration for OneBase.
// It converts a natural-language prompt into a complete project structure
// (YAML metadata + DSL source files) by matching keywords to domain bundles.
package gengen

// DomainRule describes a project domain: keywords for detection, templates
// to copy, and optional addons (e.g. "edi" adds counterparty bank details).
type DomainRule struct {
	Keywords  []string
	Templates []string // priority-ordered; first existing dir wins
	Addons    map[string]Addon
}

// Addon is an optional feature pack that extends a domain bundle.
// It contains additional YAML fields, constants, or DSL files.
type Addon struct {
	Name        string
	Description string
	SourceDir   string // relative to project root
}

// AnalyzeResult is the output of the semantic analyzer.
type AnalyzeResult struct {
	Domain    string   // matched domain name
	Template  string   // resolved path to the template directory
	Addons    []string // requested addon names
	Confident bool     // true if a single domain matched clearly
	Ambiguous []string // tied domain names (when Confident == false)
}

// ResolvedManifest describes the full set of entities requested by the user.
// Produced by the analyzer (Stage 1) or LLM (Stage 2).
type ResolvedManifest struct {
	Domain    string
	Catalogs  []EntitySpec
	Documents []EntitySpec
	Registers []EntitySpec
	Enums     []EnumSpec
	DSLFiles  map[string]string // relative path → content
}

// EntitySpec describes a single entity (catalog, document, register).
type EntitySpec struct {
	Name       string
	Kind       string // "catalog", "document", "register"
	Fields     []FieldSpec
	TableParts []TablePartSpec
	Posting    bool
}

// FieldSpec describes a single field in an entity.
type FieldSpec struct {
	Name      string
	Type      string // "string", "date", "number", "bool", "reference:X", "enum:X"
	RefEntity string // non-empty when Type is "reference:X"
	EnumName  string // non-empty when Type is "enum:X"
}

// TablePartSpec describes a table part (ТЧ) in an entity.
type TablePartSpec struct {
	Name   string
	Fields []FieldSpec
}

// EnumSpec describes an enumeration.
type EnumSpec struct {
	Name   string
	Values []string
}

// DeltaManifest describes the difference between requested and existing manifests.
type DeltaManifest struct {
	NewCatalogs   []EntitySpec // entities that don't exist yet
	NewDocuments  []EntitySpec
	NewRegisters  []EntitySpec
	NewEnums      []EnumSpec
	NewFields     map[string][]FieldSpec     // entity name → new fields to add
	NewTableParts map[string][]TablePartSpec // entity name → new table parts
	NewDSLFiles   map[string]string          // relative path → content
	Conflicts     []Conflict                 // name collisions
}

// Conflict describes a name collision between requested and existing.
type Conflict struct {
	Kind    string // "catalog", "document", "enum", "register"
	Name    string
	Message string // e.g. "Контрагент already exists, use --add-fields instead"
}
