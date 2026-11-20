package main

import (
	"fmt"
	"os"
	"regexp"

	"github.com/ivantit66/onebase/internal/pridentity"
	"github.com/ivantit66/onebase/internal/reviewrouting"
)

type apiRepository struct {
	NodeID   string `json:"node_id"`
	FullName string `json:"full_name"`
}

type gqlRepositoryIdentity struct {
	ID            string `json:"id"`
	NameWithOwner string `json:"nameWithOwner"`
}

type inventoryIdentity struct {
	identity pridentity.PRIdentityV1
	digest   string
	baseOID  string
}

// prepareIdentityInventory only selects metadata for a separate read-only view.
// The ordinary v1 protocol does not consume this inventory as authority.
func prepareIdentityInventory(prs []apiPull, repo, expectedID string) ([]apiPull, map[int]inventoryIdentity, []finding) {
	valid := make([]apiPull, 0, len(prs))
	identities := make(map[int]inventoryIdentity, len(prs))
	findings := []finding{}
	counts := make(map[int]int, len(prs))
	for _, pr := range prs {
		counts[pr.Number]++
	}
	for _, pr := range prs {
		if counts[pr.Number] != 1 {
			findings = append(findings, finding{Severity: "yellow", Code: "invalid_pr_identity", PR: pr.Number, Message: "duplicate PR identity"})
			continue
		}
		identity, err := pullIdentity(pr, repo, expectedID)
		if err != nil {
			findings = append(findings, finding{Severity: "yellow", Code: "invalid_pr_identity", PR: pr.Number, Message: err.Error()})
			continue
		}

		identities[pr.Number] = identity
		valid = append(valid, pr)
	}
	return valid, identities, findings
}

var identitySHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func pullIdentity(pr apiPull, repo, configuredID string) (inventoryIdentity, error) {
	if pr.Base.Repo == nil || pr.Head.Repo == nil {
		return inventoryIdentity{}, fmt.Errorf("missing PR identity repository (possibly deleted fork)")
	}
	expectedID := configuredID
	if pr.ExpectedBaseRepository != nil {
		if pr.ExpectedBaseRepository.FullName != repo || pr.ExpectedBaseRepository.NodeID == "" ||
			(configuredID != "" && configuredID != pr.ExpectedBaseRepository.NodeID) {
			return inventoryIdentity{}, fmt.Errorf("unexpected inventory repository metadata")
		}
		expectedID = pr.ExpectedBaseRepository.NodeID
	}
	if expectedID == "" || pr.Base.Repo.NodeID != expectedID || pr.Base.Repo.FullName != repo {
		return inventoryIdentity{}, fmt.Errorf("unexpected PR identity base repository; offline inventory requires -base-repository-id")
	}
	if pr.State != "open" || pr.Draft || !identitySHA.MatchString(pr.Base.SHA) {
		return inventoryIdentity{}, fmt.Errorf("PR identity requires an open ready PR and an integration base SHA")
	}
	p := pridentity.PRIdentityV1{
		BaseRepositoryID: pr.Base.Repo.NodeID, BaseRepository: pr.Base.Repo.FullName,
		BaseRef: pr.Base.Ref, PRID: pr.NodeID, PRNumber: pr.Number,
		HeadRepositoryID: pr.Head.Repo.NodeID, HeadRepository: pr.Head.Repo.FullName,
		HeadRef: pr.Head.Ref, HeadSHA: pr.Head.SHA, CrossRepository: pr.Base.Repo.NodeID != pr.Head.Repo.NodeID,
	}
	if pr.CrossRepository != nil {
		p.CrossRepository = *pr.CrossRepository
	}
	digest, err := p.SHA256()
	if err != nil {
		return inventoryIdentity{}, err
	}
	return inventoryIdentity{identity: p, digest: digest, baseOID: pr.Base.SHA}, nil
}

func attachIdentityInventory(result *report, identities map[int]inventoryIdentity) {
	result.IdentityInventoryVersion = 1
	result.IdentityAuthority = "read-only"
	result.IdentityV2Enabled = new(bool)
	attach := func(items []candidate) {
		for i := range items {
			if p, ok := identities[items[i].Number]; ok && items[i].Head != "" {
				identity := p.identity
				items[i].PRIdentity = &identity
				items[i].IdentitySHA256 = p.digest
				items[i].BaseRefOID = p.baseOID
			}
		}
	}
	for _, items := range [][]candidate{
		result.ReviewCandidates, result.ReviewDispatchCandidates, result.ReviewBacklog,
		result.ContentReviewCandidates, result.ParallelReviewCandidates, result.ReviewedWaitingShip,
		result.MergeCandidates, result.MergeExecutable, result.FixCandidates, result.HumanWaiting,
	} {
		attach(items)
	}
	if result.IntegrationOwner != nil {
		if p, ok := identities[result.IntegrationOwner.Number]; ok {
			identity := p.identity
			result.IntegrationOwner.PRIdentity = &identity
			result.IntegrationOwner.IdentitySHA256 = p.digest
			result.IntegrationOwner.BaseRefOID = p.baseOID
		}
	}
}

// The separate inventory accepts bounded strict JSON; encoding/json alone
// silently replaces invalid UTF-8 and unpaired escaped UTF-16 surrogates.
func validateIdentityJSON(data []byte) error {
	_, err := reviewrouting.Decode(data, 8<<20)
	return err
}
func validateIdentityFixture(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return validateIdentityJSON(data)
}
