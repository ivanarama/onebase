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

type Entity struct {
	Name   string
	Kind   Kind
	Fields []Field
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
