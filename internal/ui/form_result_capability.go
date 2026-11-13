package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

func (s *Server) resultCapabilityBuiltin(r *http.Request, proc *processor.Processor, ctxSource interpreter.CtxSource, allowed bool) interpreter.BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		if !allowed || proc == nil || proc.External || len(args) != 1 {
			return false, nil
		}
		name, ok := args[0].(string)
		if !ok {
			return false, nil
		}
		var binding processor.ResultCapability
		found := false
		for key, candidate := range proc.ResultCapabilities {
			if strings.EqualFold(key, name) {
				if found { // ambiguous metadata is never an authority
					return false, nil
				}
				binding, found = candidate, true
			}
		}
		if !found {
			return false, nil
		}
		if ctxSource == nil || ctxSource.Ctx() == nil {
			return false, nil
		}
		request := r.WithContext(ctxSource.Ctx())
		context, valid := s.checkedResultFormContext(request, proc.Name)
		if !valid {
			return false, nil
		}
		u := auth.UserFromContext(request.Context())
		ir := s.reg.GetInfoRegister(binding.Register)
		source := s.reg.GetEntity(binding.SourceEntity)
		if binding.Validate(ir, source) != nil ||
			!strings.EqualFold(metadata.RefName(processor.ResultCapabilityField(ir.Dimensions, binding.ReferenceField).Type), context.request.reference.Type) {
			return false, nil
		}
		// Nonadmin interactive users must not be able to manufacture a positive
		// proof in the index. Admin already has access to the source catalog.
		if !u.IsAdmin && (u.Has("inforeg", ir.Name, "write") || u.Has("inforeg", ir.Name, "delete") || u.Has("inforeg", ir.Name, "create")) {
			return false, nil
		}
		dec, err := s.rowDecisionFor(request.Context(), "inforeg", ir.Name, "read", storage.InfoRegisterPredicateEntity(ir))
		if err != nil || !dec.Allowed {
			return false, nil
		}
		policies := s.fieldDecisionsFor(request.Context(), "inforeg", ir.Name, storage.InfoRegisterPredicateEntity(ir))
		for _, field := range []string{binding.ReferenceField, binding.OwnerField, binding.EnabledField, binding.SourceIDField, binding.SourceVersionField, binding.ObservedAtField} {
			if policy, exists := fieldDecisionByName(policies, field); exists && policy.Masked() {
				return false, nil
			}
		}
		rows, err := s.store.InfoRegPage(request.Context(), ir, storage.RegFilter{
			Dims: map[string]string{binding.ReferenceField: context.request.reference.UUID, binding.OwnerField: u.ID}, RowFilter: dec.Predicate,
		}, 2, 0)
		if err != nil || len(rows) != 1 {
			return false, nil
		}
		row := rows[0]
		if !resultCapabilityTrue(row[binding.EnabledField]) {
			return false, nil
		}
		observed := runtime.AsTime(row[binding.ObservedAtField])
		now := time.Now()
		if observed.IsZero() || observed.After(now) || now.Sub(observed) > time.Duration(binding.MaxAgeSeconds)*time.Second {
			return false, nil
		}
		id, err := uuid.Parse(fmt.Sprint(row[binding.SourceIDField]))
		if err != nil || id == uuid.Nil {
			return false, nil
		}
		version, err := decimal.NewFromString(fmt.Sprint(row[binding.SourceVersionField]))
		if err != nil || !version.IsPositive() {
			return false, nil
		}
		// Deliberately not an object reader: only these two system columns are
		// fetched. No catalog ACL is granted and no URL/credentials/labels load.
		var liveVersion int64
		var marked bool
		sql := "SELECT _version, deletion_mark FROM " + metadata.TableName(source.Name) + " WHERE id = " + s.store.Dialect().Placeholder(1)
		if err := s.store.QueryRow(request.Context(), sql, id.String()).Scan(&liveVersion, &marked); err != nil || marked ||
			!version.Equal(decimal.NewFromInt(liveVersion)) || time.Now().After(context.expires) {
			return false, nil
		}
		return true, nil
	}
}

func resultCapabilityTrue(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case int64:
		return value == 1
	case int:
		return value == 1
	case string:
		return value == "1" || strings.EqualFold(value, "true")
	}
	return false
}
