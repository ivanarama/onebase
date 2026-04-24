package metadata

import "strings"

type Kind string

const (
	KindCatalog  Kind = "catalog"
	KindDocument Kind = "document"
)

type FieldType string

const (
	FieldTypeString FieldType = "string"
	FieldTypeDate   FieldType = "date"
	FieldTypeNumber FieldType = "number"
	FieldTypeBool   FieldType = "bool"
)

type Field struct {
	Name      string
	Type      FieldType
	RefEntity string // non-empty when Type starts with "reference:"
}

type TablePart struct {
	Name   string
	Fields []Field
}

type Entity struct {
	Name       string
	Kind       Kind
	Fields     []Field
	TableParts []TablePart
	// Posting enables 1C-style posting semantics: movements are written only
	// when the document is explicitly posted, not on every save.
	Posting bool
}

type Register struct {
	Name       string
	Dimensions []Field // form the grouping key for balances
	Resources  []Field // accumulated (summed with sign based on movement type)
	Attributes []Field // extra data, stored but not aggregated
}

func RegisterTableName(regName string) string {
	return "рег_" + strings.ToLower(regName)
}

func TablePartTableName(entityName, tpName string) string {
	return strings.ToLower(entityName) + "_" + strings.ToLower(tpName)
}

func IsReference(ft FieldType) bool {
	return strings.HasPrefix(string(ft), "reference:")
}

func RefName(ft FieldType) string {
	return strings.TrimPrefix(string(ft), "reference:")
}

func TableName(entityName string) string {
	return strings.ToLower(entityName)
}

func ColumnName(f Field) string {
	col := strings.ToLower(f.Name)
	if f.RefEntity != "" {
		return col + "_id"
	}
	return col
}
