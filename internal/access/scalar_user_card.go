package access

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// ScalarUserCardDescriptor resolves a trusted scalar descriptor through the
// same metadata validation as row access. It does not read data or grant access
// to the catalog; callers must bind the read to the authenticated account UUID.
func ScalarUserCardDescriptor(src *auth.UserCardSource, lookup EntityLookup) (storage.UserCardDescriptor, error) {
	if src == nil || lookup == nil {
		return storage.UserCardDescriptor{}, fmt.Errorf("user_card requires a source and metadata lookup")
	}
	catalog := lookup.GetEntity(src.Catalog)
	if catalog == nil || catalog.Kind != metadata.KindCatalog {
		return storage.UserCardDescriptor{}, fmt.Errorf("user_card: unknown catalog")
	}
	value, tp, err := userCardSelection(src, catalog)
	if err != nil {
		return storage.UserCardDescriptor{}, err
	}
	if tp != nil {
		return storage.UserCardDescriptor{}, fmt.Errorf("user_card: scalar field required")
	}
	return userCardDescriptor(src, &metadata.Entity{Fields: []metadata.Field{value}}, value.Name, lookup, "eq")
}
