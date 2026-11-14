package pridentity

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestAcceptedPromptPilotGoldenBytes(t *testing.T) {
	data, err := os.ReadFile("testdata/pr-identity-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name string `json:"name"`
			REST struct {
				ID     string `json:"node_id"`
				Number int    `json:"number"`
				Base   struct {
					Repo struct {
						ID   string `json:"node_id"`
						Name string `json:"full_name"`
					} `json:"repo"`
					Ref string `json:"ref"`
				} `json:"base"`
				Head struct {
					Repo struct {
						ID   string `json:"node_id"`
						Name string `json:"full_name"`
					} `json:"repo"`
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				} `json:"head"`
			} `json:"rest"`
			Hex string `json:"canonical_utf8_hex"`
			SHA string `json:"identity_sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 2 {
		t.Fatal("paired fixture changed")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			r := c.REST
			p := PRIdentityV1{r.Base.Repo.ID, r.Base.Repo.Name, r.Base.Ref, r.ID, r.Number, r.Head.Repo.ID, r.Head.Repo.Name, r.Head.Ref, r.Head.SHA, r.Base.Repo.ID != r.Head.Repo.ID}
			b, err := p.CanonicalBytes()
			if err != nil || hex.EncodeToString(b) != c.Hex {
				t.Fatalf("canonical UTF-8 drift: %x, %v", b, err)
			}
			digest, err := p.SHA256()
			if err != nil || digest != c.SHA {
				t.Fatalf("digest drift: %s, %v", digest, err)
			}
		})
	}
}

func TestIdentityRejectsUnrepresentableOrContradictoryMetadata(t *testing.T) {
	valid := PRIdentityV1{"R", "owner/repo", "main", "PR", 42, "R", "owner/repo", "ветка/проверка", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false}
	for name, change := range map[string]func(*PRIdentityV1){
		"invalid UTF-8":                  func(p *PRIdentityV1) { p.HeadRef = "\xff" },
		"newline":                        func(p *PRIdentityV1) { p.HeadRef = "branch\nhead-sha=x" },
		"NUL":                            func(p *PRIdentityV1) { p.PRID = "PR\x00" },
		"missing repository":             func(p *PRIdentityV1) { p.HeadRepositoryID = "" },
		"database ID and names disagree": func(p *PRIdentityV1) { p.HeadRepositoryID = "OTHER" },
		"wrong cross flag":               func(p *PRIdentityV1) { p.CrossRepository = true },
		"portable number":                func(p *PRIdentityV1) { p.PRNumber = 0 },
		"non-main":                       func(p *PRIdentityV1) { p.BaseRef = "release" },
		"uppercase SHA":                  func(p *PRIdentityV1) { p.HeadSHA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
	} {
		t.Run(name, func(t *testing.T) {
			p := valid
			change(&p)
			if _, err := p.CanonicalBytes(); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}
