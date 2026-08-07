package ui

import (
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) accountsList(w http.ResponseWriter, r *http.Request) {
	planName := chi.URLParam(r, "plan")
	if dec, err := url.PathUnescape(planName); err == nil {
		planName = dec
	}
	chart := s.reg.GetChartOfAccounts(planName)
	if chart == nil {
		http.Error(w, "plan of accounts not found: "+planName, 404)
		return
	}
	rows, err := s.store.GetAccounts(r.Context(), chart.Name)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "page-accounts", map[string]any{
		"Chart": chart,
		"Rows":  rows,
	})
}

func (s *Server) accountRegMovements(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	ar := s.reg.GetAccountRegister(name)
	if ar == nil {
		http.Error(w, "account register not found: "+name, 404)
		return
	}
	rows, err := s.store.GetAccountMovements(r.Context(), ar.Name, ar)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.resolveAccountRows(r.Context(), rows, ar)
	s.render(w, r, "page-accountreg-movements", map[string]any{
		"Register": ar,
		"Rows":     rows,
	})
}

func (s *Server) accountRegBalances(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	ar := s.reg.GetAccountRegister(name)
	if ar == nil {
		http.Error(w, "account register not found: "+name, 404)
		return
	}

	// resolve associated chart of accounts
	chart := s.reg.GetChartOfAccounts(ar.Accounts)

	asOf := time.Now()
	if d := r.URL.Query().Get("date"); d != "" {
		if t, err := time.Parse("2006-01-02", d); err == nil {
			asOf = t.Add(24*time.Hour - time.Second)
		}
	}

	planName := ""
	if chart != nil {
		planName = chart.Name
	}

	rows, err := s.store.AccountBalances(r.Context(), ar.Name, planName, asOf, ar.Resources, ar.Subconto)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.resolveAccountRows(r.Context(), rows, ar)

	// Sort rows by code
	sort.Slice(rows, func(i, j int) bool {
		ci, _ := rows[i]["code"].(string)
		cj, _ := rows[j]["code"].(string)
		return ci < cj
	})

	s.render(w, r, "page-accountreg-balances", map[string]any{
		"Register": ar,
		"Chart":    chart,
		"Rows":     rows,
		"AsOf":     asOf.Format("2006-01-02"),
	})
}
