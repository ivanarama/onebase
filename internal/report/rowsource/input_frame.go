package rowsource

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/provenance"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

// AcceptedParameter is host-captured transport text plus the value already
// normalized by the existing transport parser. It is not a client descriptor.
// Capture validates its concrete scalar type against the canonical metadata;
// no caller-supplied origin, Type, Source, UUID flag or default marker is read.
type AcceptedParameter struct {
	Text   string
	Parsed any
}
type InputSelection struct {
	Parameters map[string]AcceptedParameter
	Variant    *string // nil=missing; pointer to "" is an explicitly accepted empty
}

// InputFrame binds inputs before execution. It is NOT a successful producer
// receipt, final-schema permission, declassification or snapshot capability.
// Only CaptureInputFrame can create its private state. No values/digests are
// exposed by inspection or serialization, and this frame is not injected in DSL.
type InputFrame struct{ state *inputFrameState }
type InputBindingSummary struct{ Kind, Slot, ID, Type, RefRoot, Presence, Channel string }
type InputFrameSummary struct {
	Report, Variant, Phase string
	Bindings               []InputBindingSummary
}

type originKind uint8

const (
	sourceFieldOrigin originKind = iota + 1
	fixedLiteralOrigin
	userInputOrigin
	serverDefaultOrigin
	serverContextOrigin
	validatedProgramOrigin
	maskedOrigin
)

func (k originKind) name() string {
	switch k {
	case sourceFieldOrigin:
		return "source-field"
	case fixedLiteralOrigin:
		return "fixed-literal"
	case userInputOrigin:
		return "user-input"
	case serverDefaultOrigin:
		return "server-default"
	case serverContextOrigin:
		return "server-context"
	case validatedProgramOrigin:
		return "validated-program"
	case maskedOrigin:
		return "masked"
	}
	return ""
}

type inputBinding struct {
	Kind                                       string
	Slot, ID, Type, RefRoot, Presence, Channel string
	Value, DefaultExpression                   [32]byte
	Controls                                   [][32]byte
	UserControlled                             bool // absence/default never loses its user-controllable slot
}
type frameIdentity struct {
	Report, Variant, Policy, Timezone, Dialect                string
	Generation                                                uint64
	CodecFormat                                               int
	Query, Module, Profile, Metadata, Actor, Compiled, Inputs [32]byte
}
type inputFrameState struct {
	identity frameIdentity
	inputs   []inputBinding
	params   map[string]any
	code     CodeLookup
	db       *storage.DB
	registry *runtime.Registry
	timezone *time.Location // process-local immutable location identity, not JSON
	seal     *inputSeal
}
type inputSeal struct{ version string }
type compileSnapshot struct {
	Result     query.Result
	RowFilters map[query.SourceRef]*storage.Predicate
}

const framePolicyVersion = "rowsource-strict-inputs/b3-c5/1"

func (InputFrame) String() string             { return "ReportInputFrame" }
func (InputFrame) GoString() string           { return "ReportInputFrame" }
func (InputFrame) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "ReportInputFrame") }
func (InputFrame) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("row source: input frame is server-only")
}
func (*InputFrame) UnmarshalJSON([]byte) error {
	return fmt.Errorf("row source: input frame cannot come from JSON")
}

func (f *InputFrame) Inspect() (InputFrameSummary, error) {
	if f == nil || f.state == nil || f.state.seal == nil {
		return InputFrameSummary{}, fmt.Errorf("row source: input frame missing")
	}
	s := f.state
	out := InputFrameSummary{Report: s.identity.Report, Variant: s.identity.Variant, Phase: "inputs-only"}
	for _, b := range s.inputs {
		out.Bindings = append(out.Bindings, InputBindingSummary{b.Kind, b.Slot, b.ID, b.Type, b.RefRoot, b.Presence, b.Channel})
	}
	return out, nil
}

// CaptureInputFrame accepts host-selected request/preset values, not a JSON
// evidence descriptor. Current scalar values must already be parsed by the
// existing host parser. Metadata defaults needing clock/constants/context are
// deliberately STOP until those dependencies have their own bound host inputs.
func CaptureInputFrame(ctx context.Context, db *storage.DB, reg *runtime.Registry, name string, request, preset InputSelection, lookup CodeLookup) (*InputFrame, error) {
	if db == nil || reg == nil || lookup == nil {
		return nil, fmt.Errorf("row source: input frame requires host context")
	}
	ctx, cancel := context.WithTimeout(storage.ReadOnlyContext(ctx), 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	generation := reg.Generation()
	rep := reg.GetReport(name)
	if rep == nil || rep.External || rep.RowSource == nil {
		return nil, fmt.Errorf("row source: configured report missing")
	}
	if errs := rep.RowSource.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("row source: invalid source metadata")
	}
	if u := auth.UserFromContext(ctx); u != nil && !u.Has("report", rep.Name, "run") {
		return nil, fmt.Errorf("row source: report run denied")
	}
	variant, variantBinding, err := captureVariant(rep, request.Variant, preset.Variant)
	if err != nil {
		return nil, err
	}
	params, bindings, err := captureParameters(reg, rep, request.Parameters, preset.Parameters)
	if err != nil {
		return nil, err
	}
	bindings = append([]inputBinding{variantBinding}, bindings...)
	// Context bindings are private and explicitly non-literal. The location object
	// identity also participates in process-local revalidation of timezone rules.
	timezone := time.Local
	zoneName := timezone.String()
	zoneDigest, err := provenance.InputDigest(zoneName)
	if err != nil {
		return nil, err
	}
	bindings = append(bindings, inputBinding{Kind: serverContextOrigin.name(), Slot: "context", ID: "timezone", Type: "string", Presence: "value", Channel: "server", Value: zoneDigest})
	metadataBefore, err := frameMetadataDigest(reg, rep)
	if err != nil {
		return nil, err
	}
	actorBefore, err := frameActorDigest(ctx)
	if err != nil {
		return nil, err
	}
	code, err := lookup(ctx, rep.Name)
	if err != nil {
		return nil, fmt.Errorf("row source: module unavailable")
	}
	p, err := compileProgram(code, rep.RowSource.Procedure)
	if err != nil {
		return nil, err
	}
	id, err := captureFrameIdentity(ctx, db, reg, rep, variant, params, bindings, p.evidence)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reg.Generation() != generation || id.Generation != generation || time.Local != timezone || id.Metadata != metadataBefore || id.Actor != actorBefore {
		return nil, fmt.Errorf("row source: input identity changed during capture")
	}
	return &InputFrame{state: &inputFrameState{identity: id, inputs: bindings, params: params, code: lookup, db: db, registry: reg, timezone: timezone, seal: &inputSeal{version: "inputs-only/1"}}}, nil
}

// Revalidate repeats the strict compile/read/RLS/field/output-policy boundary
// against the current supplied host actor and registry. It reads no result rows
// and grants no permission for a final schema or persisted snapshot.
func (f *InputFrame) Revalidate(ctx context.Context, db *storage.DB, reg *runtime.Registry) error {
	if f == nil || f.state == nil || f.state.seal == nil {
		return fmt.Errorf("row source: input frame missing")
	}
	s := f.state
	if db != s.db || reg != s.registry || time.Local != s.timezone {
		return fmt.Errorf("row source: input frame host changed")
	}
	ctx, cancel := context.WithTimeout(storage.ReadOnlyContext(ctx), 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	rep := reg.GetReport(s.identity.Report)
	if rep == nil || rep.External || rep.RowSource == nil {
		return fmt.Errorf("row source: input frame report changed")
	}
	if u := auth.UserFromContext(ctx); u != nil && !u.Has("report", rep.Name, "run") {
		return fmt.Errorf("row source: report run denied")
	}
	metadataBefore, err := frameMetadataDigest(reg, rep)
	if err != nil {
		return err
	}
	actorBefore, err := frameActorDigest(ctx)
	if err != nil {
		return err
	}
	code, err := s.code(ctx, rep.Name)
	if err != nil {
		return fmt.Errorf("row source: module unavailable")
	}
	p, err := compileProgram(code, rep.RowSource.Procedure)
	if err != nil {
		return err
	}
	id, err := captureFrameIdentity(ctx, db, reg, rep, s.identity.Variant, s.params, s.inputs, p.evidence)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if id != s.identity || reg.Generation() != id.Generation || id.Metadata != metadataBefore || id.Actor != actorBefore || time.Local != s.timezone {
		return fmt.Errorf("row source: stale input frame")
	}
	return nil
}

func captureVariant(rep *report.Report, request, preset *string) (string, inputBinding, error) {
	value := request
	channel := "request"
	if value == nil {
		value = preset
		channel = "preset"
	}
	raw := ""
	presence := "missing"
	kind := serverDefaultOrigin
	if value != nil {
		raw = *value
		presence = "value"
		kind = userInputOrigin
		if raw == "" {
			presence = "explicit-empty"
		}
	}
	canonical := ""
	if raw != "" {
		count := 0
		for _, v := range rep.Variants {
			if strings.EqualFold(v.Name, raw) {
				canonical = v.Name
				count++
			}
		}
		if count != 1 {
			return "", inputBinding{}, fmt.Errorf("row source: variant missing or ambiguous")
		}
	}
	digest, err := provenance.InputDigest(canonical)
	if err != nil {
		return "", inputBinding{}, err
	}
	if value == nil {
		channel = "host"
	}
	binding := inputBinding{Kind: kind.name(), Slot: "variant", ID: "variant", Type: "string", Presence: presence, Channel: channel, Value: digest, UserControlled: true}
	if value == nil {
		binding.DefaultExpression, err = provenance.TypedDigest(struct{ Report, Expression string }{rep.Name, "canonical-base-variant/1"})
		if err != nil {
			return "", inputBinding{}, err
		}
	}
	return canonical, binding, nil
}
func canonicalAccepted(raw map[string]AcceptedParameter, params []report.Param) (map[string]AcceptedParameter, error) {
	known := map[string]string{}
	for _, p := range params {
		key := strings.ToLower(p.Name)
		if p.Name == "" || known[key] != "" {
			return nil, fmt.Errorf("row source: parameter metadata ambiguous")
		}
		known[key] = p.Name
	}
	if len(raw) > 256 {
		return nil, fmt.Errorf("row source: too many input slots")
	}
	out := map[string]AcceptedParameter{}
	for name, value := range raw {
		marker := strings.HasPrefix(strings.ToLower(name), "__has.")
		key := name
		if marker {
			key = name[len("__has."):]
		}
		canonical := known[strings.ToLower(key)]
		if canonical == "" {
			return nil, fmt.Errorf("row source: unknown input slot")
		}
		if marker {
			canonical = "__has." + canonical
		}
		if _, duplicate := out[canonical]; duplicate {
			return nil, fmt.Errorf("row source: ambiguous input slot")
		}
		if len(value.Text) > maxValueBytes {
			return nil, fmt.Errorf("row source: input too large")
		}
		out[canonical] = value
	}
	return out, nil
}
func captureParameters(reg *runtime.Registry, rep *report.Report, request, preset map[string]AcceptedParameter) (map[string]any, []inputBinding, error) {
	if len(rep.Params) > 128 {
		return nil, nil, fmt.Errorf("row source: too many parameter slots")
	}
	req, err := canonicalAccepted(request, rep.Params)
	if err != nil {
		return nil, nil, err
	}
	pre, err := canonicalAccepted(preset, rep.Params)
	if err != nil {
		return nil, nil, err
	}
	values := map[string]any{}
	var bindings []inputBinding
	bytes := 0
	for i, p := range rep.Params {
		input, present := req[p.Name]
		channel := "request"
		if !present {
			if _, marker := req["__has."+p.Name]; marker {
				present = true
				parsed, err := report.ParseParamValue("", p, report.ParamParseForm)
				if err != nil {
					return nil, nil, err
				}
				input = AcceptedParameter{Parsed: parsed}
			}
		}
		if !present {
			input, present = pre[p.Name]
			channel = "preset"
			if !present {
				if _, marker := pre["__has."+p.Name]; marker {
					present = true
					parsed, err := report.ParseParamValue("", p, report.ParamParseForm)
					if err != nil {
						return nil, nil, err
					}
					input = AcceptedParameter{Parsed: parsed}
				}
			}
		}
		kind := userInputOrigin
		presence := "value"
		value := input.Parsed
		if present && input.Text == "" {
			presence = "explicit-empty"
		}
		if !present {
			if p.Default != "" {
				return nil, nil, fmt.Errorf("row source: default dependencies are not bound")
			}
			kind = serverDefaultOrigin
			presence = "missing"
			channel = "host"
			value, err = report.ParseParamValue("", p, report.ParamParseForm)
			if err != nil {
				return nil, nil, err
			}
		}
		root, err := validateParameterType(reg, p, value)
		if err != nil {
			return nil, nil, err
		}
		size, err := valueBytes(value)
		if err != nil {
			return nil, nil, err
		}
		bytes += size
		if bytes > maxBytes {
			return nil, nil, fmt.Errorf("row source: input budget")
		}
		digest, err := provenance.InputDigest(value)
		if err != nil {
			return nil, nil, err
		}
		b := inputBinding{Kind: kind.name(), Slot: "report-param", ID: fmt.Sprintf("%d:%s", i, p.Name), Type: p.Type, RefRoot: root, Presence: presence, Channel: channel, Value: digest, UserControlled: true}
		if !present {
			// Both absent accepted channels still control the default slot. Preserve
			// them as explicit user-input dependencies, not merely equal typed bytes.
			nilDigest, err := provenance.InputDigest(nil)
			if err != nil {
				return nil, nil, err
			}
			for _, source := range []string{"request", "preset"} {
				control := inputBinding{Kind: userInputOrigin.name(), Slot: b.Slot, ID: b.ID, Type: b.Type, RefRoot: b.RefRoot, Presence: "missing", Channel: source, Value: nilDigest, UserControlled: true}
				key, err := provenance.TypedDigest(control)
				if err != nil {
					return nil, nil, err
				}
				bindings = append(bindings, control)
				b.Controls = append(b.Controls, key)
			}
			b.DefaultExpression, err = provenance.TypedDigest(struct {
				Param  report.Param
				Parser string
			}{p, "missing/form-parser/1"})
			if err != nil {
				return nil, nil, err
			}
		}
		bindings = append(bindings, b)
		values[p.Name] = value
	}
	return values, bindings, nil
}
func validateParameterType(reg *runtime.Registry, p report.Param, value any) (string, error) {
	if _, err := provenance.InputDigest(value); err != nil {
		return "", err
	}
	fail := func() (string, error) { return "", fmt.Errorf("row source: parameter type or root is not proved") }
	if strings.HasPrefix(p.Type, "reference:") {
		name := strings.TrimPrefix(p.Type, "reference:")
		entity := reg.GetEntity(name)
		if entity == nil || !strings.EqualFold(entity.Name, name) {
			return fail()
		}
		if value != nil {
			v, ok := value.(string)
			if !ok {
				return fail()
			}
			if v != "" {
				if _, err := uuid.Parse(v); err != nil {
					return fail()
				}
			}
		}
		return entity.Name, nil
	}
	switch p.Type {
	case "string":
		if value != nil {
			if _, ok := value.(string); !ok {
				return fail()
			}
		}
	case "select":
		if value != nil {
			v, ok := value.(string)
			if !ok {
				return fail()
			}
			allowed := v == ""
			for _, option := range p.Options {
				allowed = allowed || option == v
			}
			if !allowed {
				return fail()
			}
		}
	case "number":
		if value != nil {
			switch value.(type) {
			case int, int64, float64, decimal.Decimal:
			default:
				return fail()
			}
		}
	case "bool":
		if value != nil {
			if _, ok := value.(bool); !ok {
				return fail()
			}
		}
	case "date", "datetime":
		if value != nil {
			if _, ok := value.(time.Time); !ok {
				return fail()
			}
		}
	default:
		return fail()
	}
	return "", nil
}

func captureFrameIdentity(ctx context.Context, db *storage.DB, reg *runtime.Registry, rep *report.Report, variant string, params map[string]any, inputs []inputBinding, evidence *programEvidence) (frameIdentity, error) {
	id := frameIdentity{CodecFormat: provenance.FormatVersion, Report: rep.Name, Variant: variant, Policy: framePolicyVersion, Timezone: time.Local.String(), Dialect: db.Dialect().Name(), Generation: reg.Generation(), Query: sha256.Sum256([]byte(rep.Query)), Module: evidence.module, Profile: evidence.profile}
	if variant != "" {
		n := 0
		for _, v := range rep.Variants {
			if v.Name == variant {
				n++
			}
		}
		if n != 1 {
			return id, fmt.Errorf("row source: variant changed")
		}
	}
	var err error
	id.Metadata, err = frameMetadataDigest(reg, rep)
	if err != nil {
		return id, err
	}
	id.Actor, err = frameActorDigest(ctx)
	if err != nil {
		return id, err
	}
	id.Inputs, err = provenance.TypedDigest(inputs)
	if err != nil {
		return id, fmt.Errorf("row source: unsupported input snapshot: %w", err)
	}
	e := &execution{db: db, reg: reg, lookup: resultfilter.RegistryLookup(reg), fields: map[string][]resultfilter.Field{}, originKeys: map[string]bool{}}
	if _, err := e.compile(ctx, rep.Query, params); err != nil {
		return id, err
	}
	type rowFilterSnapshot struct {
		Source    query.SourceRef
		Predicate *storage.Predicate
	}
	filters := make([]rowFilterSnapshot, 0, len(e.lastCompile.RowFilters))
	for source, predicate := range e.lastCompile.RowFilters {
		filters = append(filters, rowFilterSnapshot{source, predicate})
	}
	sort.Slice(filters, func(i, j int) bool {
		a, b := filters[i].Source, filters[j].Source
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.TablePart < b.TablePart
	})
	id.Compiled, err = provenance.TypedDigest(struct {
		Result  query.Result
		Filters []rowFilterSnapshot
	}{e.lastCompile.Result, filters})
	if err != nil {
		return id, fmt.Errorf("row source: unsupported compiled snapshot: %w", err)
	}
	return id, nil
}

// Keep the compiler from treating encoding/json as a supported frame transport.
var _ json.Marshaler = (*InputFrame)(nil)
var _ json.Unmarshaler = (*InputFrame)(nil)

func frameMetadataDigest(reg *runtime.Registry, rep *report.Report) ([32]byte, error) {
	type metadataSnapshot struct {
		Report    *report.Report
		Entities  []*metadata.Entity
		Registers []*metadata.Register
		Info      []*metadata.InfoRegister
		Accounts  []*metadata.AccountRegister
		Enums     []*metadata.Enum
	}
	snapshot := metadataSnapshot{rep, reg.Entities(), reg.Registers(), reg.InfoRegisters(), reg.AccountRegisters(), reg.Enums()}
	sort.Slice(snapshot.Entities, func(i, j int) bool {
		return string(snapshot.Entities[i].Kind)+"/"+snapshot.Entities[i].Name < string(snapshot.Entities[j].Kind)+"/"+snapshot.Entities[j].Name
	})
	sort.Slice(snapshot.Registers, func(i, j int) bool { return snapshot.Registers[i].Name < snapshot.Registers[j].Name })
	sort.Slice(snapshot.Info, func(i, j int) bool { return snapshot.Info[i].Name < snapshot.Info[j].Name })
	sort.Slice(snapshot.Accounts, func(i, j int) bool { return snapshot.Accounts[i].Name < snapshot.Accounts[j].Name })
	sort.Slice(snapshot.Enums, func(i, j int) bool { return snapshot.Enums[i].Name < snapshot.Enums[j].Name })
	return provenance.TypedDigest(snapshot)
}

func frameActorDigest(ctx context.Context) ([32]byte, error) {
	type actorSnapshot struct {
		Present                   bool
		ID, Login, Name, Language string
		Admin                     bool
		Attrs                     map[string]any
		Roles                     []*auth.Role
	}
	actor := actorSnapshot{}
	if u := auth.UserFromContext(ctx); u != nil {
		actor = actorSnapshot{true, u.ID, u.Login, u.FullName, u.Lang, u.IsAdmin, u.Attrs, u.Roles}
	}
	return provenance.TypedDigest(actor)
}
