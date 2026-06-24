// Package aicontext keeps the historical compact prompt-context API.
//
// New code should use internal/aicontract directly; this wrapper preserves the
// existing UI/runtime call sites while sharing the same implementation.
package aicontext

import "github.com/ivantit66/onebase/internal/aicontract"

type NamedTitle = aicontract.NamedTitle
type Input = aicontract.TextInput

func SchemaText(in Input) string {
	return aicontract.SchemaText(in)
}
