package resultfilter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

var ErrScanLimit = errors.New("отбор не завершён: превышен предел исходных строк; уточните параметры отчёта")

const DefaultMaxScan = 100000
const DefaultTimeout = 30 * time.Second

type Options struct {
	MaxRows  int
	Offset   int
	CountAll bool
	MaxScan  int
	Timeout  time.Duration
}
type Result struct {
	Rows      []map[string]any
	Columns   []string
	Total     int
	Truncated bool
	Scanned   int
}

// Run retains at most MaxRows accepted rows. Source scanning and elapsed time
// are bounded independently of matches. No partial result is returned on scan
// exhaustion/cancellation, so consumers cannot present incomplete grand totals.
func Run(ctx context.Context, db *storage.DB, res *query.Result, plan *Plan, opts Options) (Result, error) {
	var result Result
	if opts.Offset < 0 || opts.MaxRows < 0 {
		return result, fmt.Errorf("некорректная страница отчёта")
	}
	if opts.MaxScan <= 0 {
		opts.MaxScan = DefaultMaxScan
	}
	if opts.Timeout <= 0 || opts.Timeout > DefaultTimeout {
		opts.Timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	var physical []string
	cols, err := db.VisitQuery(ctx, res.SQL, res.Args, func(row map[string]any, cols []string) (bool, error) {
		result.Scanned++
		if result.Scanned > opts.MaxScan {
			return false, ErrScanLimit
		}
		if physical == nil {
			physical = append([]string(nil), cols...)
			seen := map[string]bool{}
			for _, col := range physical {
				if seen[col] {
					return false, fmt.Errorf("неоднозначные колонки результата")
				}
				seen[col] = true
			}
		}
		labels := append([]string(nil), cols...)
		one := []map[string]any{row}
		query.RestoreLongLabels(res, one, labels)
		query.NormalizeColumns(res, one)
		matches, err := plan.Match(row)
		if err != nil {
			return false, err
		}
		if !matches {
			return true, nil
		}
		result.Total++
		if result.Total <= opts.Offset {
			return true, nil
		}
		if opts.MaxRows > 0 && len(result.Rows) >= opts.MaxRows {
			result.Truncated = true
			return opts.CountAll, nil
		}
		result.Rows = append(result.Rows, row)
		return true, nil
	})
	if err != nil {
		return Result{}, err
	}
	result.Columns = append([]string(nil), cols...)
	query.RestoreLongLabels(res, nil, result.Columns)
	return result, nil
}
