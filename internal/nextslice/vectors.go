package nextslice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// GoldenFile is shared with external consumers; it contains observations only.
type GoldenFile struct {
	Schema  string   `json:"schema"`
	Vectors []Vector `json:"vectors"`
}
type Vector struct {
	Name        string      `json:"name"`
	Kind        string      `json:"kind"`
	PlanPath    string      `json:"plan_path"`
	Plan        string      `json:"plan"`
	Record      string      `json:"record,omitempty"`
	Observation Observation `json:"observation"`
	Now         string      `json:"now"`
	WantError   bool        `json:"want_error,omitempty"`
	WantState   string      `json:"want_state,omitempty"`
	WantClaim   string      `json:"want_claim,omitempty"`
}

func CheckVectors(data []byte) (int, error) {
	var f GoldenFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return 0, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return 0, fmt.Errorf("trailing JSON")
	}
	if f.Schema != "pp-next-slice-golden-v1" || len(f.Vectors) == 0 {
		return 0, fmt.Errorf("unsupported or empty vectors")
	}
	seen := map[string]bool{}
	for i, v := range f.Vectors {
		if v.Name == "" || seen[v.Name] {
			return i, fmt.Errorf("missing or duplicate vector name")
		}
		seen[v.Name] = true
		var err error
		switch v.Kind {
		case "manifest":
			_, err = ParseManifest(v.PlanPath, []byte(v.Plan))
		case "declaration":
			_, err = ParseDeclaration(v.Record)
		case "root":
			_, err = ParseRoot(v.Record)
		case "phase":
			_, err = ParsePhase(v.Record)
		case "reduce":
			var m Manifest
			m, err = ParseManifest(v.PlanPath, []byte(v.Plan))
			if err == nil {
				var now time.Time
				now, err = time.Parse(time.RFC3339Nano, v.Now)
				if err == nil {
					result := Reduce(m, v.Observation, "ivanarama", now)
					if result.State != v.WantState || result.Claim != v.WantClaim || result.Authoritative || result.Executable {
						err = fmt.Errorf("got state=%s claim=%s authoritative=%v executable=%v, want %s/%s", result.State, result.Claim, result.Authoritative, result.Executable, v.WantState, v.WantClaim)
					}
				}
			}
		default:
			return i, fmt.Errorf("%s: unknown vector kind", v.Name)
		}
		if (err != nil) != v.WantError {
			return i, fmt.Errorf("%s: error=%v want_error=%v", v.Name, err, v.WantError)
		}
	}
	return len(f.Vectors), nil
}
