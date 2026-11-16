package query

import "github.com/ivantit66/onebase/internal/provenance"

// SQL input domain is stricter than host metadata snapshots. The shared codec
// binds types without reclassifying any input as a trusted literal or permission.
func lineageArgumentsSupported(values []any) bool {
	_, err := provenance.InputDigest(values)
	return err == nil
}
func lineageTypedDigest(value any) ([32]byte, bool) {
	digest, err := provenance.TypedDigest(value)
	return digest, err == nil
}
