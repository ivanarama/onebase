package ui

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// Preserve the historical user message while carrying a typed native denial.
// A user-raised error with the same text must not become an authorization result.
type querySourceAccessDenied struct{ source string }

func (e *querySourceAccessDenied) Error() string {
	return fmt.Sprintf("нет доступа к объекту: %s", e.source)
}
func (e *querySourceAccessDenied) Unwrap() error { return interpreter.ErrRowAccessDenied }

func reportResultErrorStatus(err error) int {
	if errors.Is(err, interpreter.ErrRowAccessDenied) {
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}
