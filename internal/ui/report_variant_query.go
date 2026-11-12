package ui

import (
	"context"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func (s *Server) compileReportVariantQuery(ctx context.Context, rep *reportpkg.Report, variant string, params map[string]any) (query.Result, error) {
	text, values, err := rep.QueryForVariant(variant, params)
	if err != nil {
		return query.Result{}, err
	}
	return s.compileQueryWithRowAccess(ctx, text, values)
}
