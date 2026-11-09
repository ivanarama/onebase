// Package pridentity implements the read-only identity schema from plan 196 A.
// An identity is metadata, never a lease or permission to mutate a repository.
package pridentity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// PRIdentityV1 matches PromptPilot's accepted pp-pr-identity-v1 byte contract.
// Repository IDs are GraphQL global IDs (REST node_id), not database IDs.
type PRIdentityV1 struct {
	BaseRepositoryID string `json:"base_repository_id"`
	BaseRepository   string `json:"base_repository"`
	BaseRef          string `json:"base_ref"`
	PRID             string `json:"pr_id"`
	PRNumber         int    `json:"pr_number"`
	HeadRepositoryID string `json:"head_repository_id"`
	HeadRepository   string `json:"head_repository"`
	HeadRef          string `json:"head_ref"`
	HeadSHA          string `json:"head_sha"`
	CrossRepository  bool   `json:"cross_repository"`
}

func validText(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// CanonicalBytes validates and serializes strict UTF-8, without normalization,
// with LF separators and a final LF. Its field order is part of the contract.
func (p PRIdentityV1) CanonicalBytes() ([]byte, error) {
	fields := []struct{ name, value string }{
		{"base-repository-id", p.BaseRepositoryID},
		{"base-repository", p.BaseRepository},
		{"base-ref", p.BaseRef},
		{"pr-id", p.PRID},
		{"pr-number", strconv.Itoa(p.PRNumber)},
		{"head-repository-id", p.HeadRepositoryID},
		{"head-repository", p.HeadRepository},
		{"head-ref", p.HeadRef},
		{"head-sha", p.HeadSHA},
		{"cross-repository", strconv.FormatBool(p.CrossRepository)},
	}
	for _, f := range fields {
		if !validText(f.value) {
			return nil, fmt.Errorf("invalid PR identity field: %s", f.name)
		}
	}
	if p.PRNumber < 1 || p.PRNumber > 2147483647 || p.BaseRef != "main" || !shaPattern.MatchString(p.HeadSHA) {
		return nil, fmt.Errorf("invalid PR identity number, base ref or head SHA")
	}
	if !repositoryPattern.MatchString(p.BaseRepository) || !repositoryPattern.MatchString(p.HeadRepository) {
		return nil, fmt.Errorf("invalid PR identity repository name")
	}
	sameID, sameName := p.BaseRepositoryID == p.HeadRepositoryID, p.BaseRepository == p.HeadRepository
	if sameID != sameName || p.CrossRepository == sameID {
		return nil, fmt.Errorf("inconsistent PR identity repository metadata")
	}
	var b strings.Builder
	b.WriteString("pp-pr-identity-v1\n")
	for _, f := range fields {
		b.WriteString(f.name + "=" + f.value + "\n")
	}
	return []byte(b.String()), nil
}

// SHA256 returns the digest of the validated canonical bytes.
func (p PRIdentityV1) SHA256() (string, error) {
	b, err := p.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
