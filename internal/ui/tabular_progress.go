package ui

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/shopspring/decimal"
)

const tabularProgressType = "application/x-ndjson"

// Clients without streaming support receive the ordinary JSON form response.
func tabularProgressBuiltin(w http.ResponseWriter, r *http.Request, enc *json.Encoder) interpreter.BuiltinFunc {
	last := 0
	return func(args []any, _ string, _ int) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("СообщитьПрогрессФормирования: нужны число завершённых этапов и общее число этапов")
		}
		done, doneOK := args[0].(decimal.Decimal)
		total, totalOK := args[1].(decimal.Decimal)
		if !doneOK || !totalOK || !done.IsInteger() || !total.IsInteger() || done.IsNegative() || !total.IsPositive() ||
			done.GreaterThan(total) || total.GreaterThan(decimal.NewFromInt(1000000)) {
			return nil, fmt.Errorf("СообщитьПрогрессФормирования: неверное число этапов")
		}
		percent := int(100 * done.IntPart() / total.IntPart())
		if r.Header.Get("Accept") != tabularProgressType || percent <= last || percent >= 100 {
			return nil, nil
		}
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		if last == 0 {
			// Publication may set a cookie, so allocate it before sending headers.
			if err := ensureTabularSession(w, r); err != nil {
				return nil, err
			}
			w.Header().Set("Content-Type", tabularProgressType)
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Accel-Buffering", "no")
		}
		last = percent
		if err := enc.Encode(struct {
			Progress int `json:"progress"`
		}{percent}); err != nil {
			return nil, err
		}
		err := http.NewResponseController(w).Flush()
		if err == http.ErrNotSupported {
			return nil, nil
		}
		return nil, err
	}
}
