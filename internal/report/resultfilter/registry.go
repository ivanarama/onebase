package resultfilter

import (
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"strings"
)

// RegistryLookup is kind-aware. A catalog cannot lend its fields to a register
// with the same name; account/accumulation source name collisions are rejected.
func RegistryLookup(reg *runtime.Registry) Lookup {
	return func(kind, name string) *metadata.Entity {
		if reg == nil {
			return nil
		}
		switch kind {
		case "catalog", "document":
			e := reg.GetEntity(name)
			if e != nil && strings.EqualFold(string(e.Kind), kind) {
				return e
			}
		case "inforeg":
			if r := reg.GetInfoRegister(name); r != nil {
				return storage.InfoRegisterPredicateEntity(r)
			}
		case "register":
			r, a := reg.GetRegister(name), reg.GetAccountRegister(name)
			if r != nil && a != nil {
				return nil
			}
			if r != nil {
				return storage.RegisterPredicateEntity(r)
			}
			if a != nil {
				return storage.AccountRegisterPredicateEntity(a)
			}
		}
		return nil
	}
}
func RegistryEnums(reg *runtime.Registry) EnumLookup {
	return func(name string) []string {
		if reg != nil {
			if e := reg.GetEnum(name); e != nil {
				return e.Values
			}
		}
		return nil
	}
}
