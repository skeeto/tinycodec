package main

import (
	"crypto/subtle"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

var tmpl = template.Must(template.New("").Parse(dashboardHTML))

type Dashboard struct {
	s        *Store
	password string
}

func (d *Dashboard) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", d.index)
	mux.HandleFunc("GET /key/{key}", d.key)
	mux.HandleFunc("GET /key/{key}/{id}", d.failures)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pw, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(d.password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="TCOM tester"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func totalVariants() int {
	n := 0
	for _, c := range catalog {
		n += len(c.Vars)
	}
	return n
}

type keySummary struct {
	Key, Path                       string
	Solved, Passes, Attempts, Fails int
	FirstTry, First, Last           string
}

func summarize(ks *KeyState) keySummary {
	s := keySummary{Key: ks.Key, Path: url.PathEscape(ks.Key), First: fmtTime(ks.First), Last: fmtTime(ks.Last)}
	tried, firstPass := 0, 0
	for _, c := range catalog {
		cs := ks.Chals[c.Slug]
		if cs == nil {
			continue
		}
		if cs.solved(c) {
			s.Solved++
		}
		for _, v := range c.Vars {
			if _, ok := cs.Passed[v]; ok {
				s.Passes++
			}
		}
		s.Attempts += cs.Attempts
		s.Fails += cs.Fails
		if cs.FirstTry != 0 {
			tried++
			if cs.FirstTry == 1 {
				firstPass++
			}
		}
	}
	s.FirstTry = "—"
	if tried > 0 {
		s.FirstTry = fmt.Sprintf("%d / %d", firstPass, tried)
	}
	return s
}

func (d *Dashboard) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (d *Dashboard) index(w http.ResponseWriter, r *http.Request) {
	d.s.mu.Lock()
	all := make([]*KeyState, 0, len(d.s.keys))
	for _, ks := range d.s.keys {
		all = append(all, ks)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Last.After(all[j].Last) })
	keys := make([]keySummary, len(all))
	for i, ks := range all {
		keys[i] = summarize(ks)
	}
	d.s.mu.Unlock()
	d.render(w, "index", map[string]any{"Keys": keys, "Total": len(catalog), "TotalVariants": totalVariants()})
}

type varCell struct {
	Label  string
	Passed bool
}

type chalRow struct {
	ID, Slug, Name  string
	Kind            Kind
	Vars            []varCell
	Attempts, Fails int
	FirstTry        int
	Solved          string
}

func (d *Dashboard) key(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	d.s.mu.Lock()
	ks := d.s.keys[key]
	if ks == nil {
		d.s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	sum := summarize(ks)
	var rows []chalRow
	for _, c := range catalog {
		cs := ks.Chals[c.Slug]
		row := chalRow{ID: c.ID, Slug: c.Slug, Name: c.Name, Kind: c.Kind}
		for _, v := range c.Vars {
			passed := false
			if cs != nil {
				_, passed = cs.Passed[v]
			}
			row.Vars = append(row.Vars, varCell{v, passed})
		}
		if cs != nil {
			row.Attempts, row.Fails, row.FirstTry = cs.Attempts, cs.Fails, cs.FirstTry
			row.Solved = fmtTime(cs.SolvedAt)
		}
		rows = append(rows, row)
	}
	d.s.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	d.render(w, "key", map[string]any{
		"Key": key, "KeyPath": url.PathEscape(key), "First": sum.First, "Last": sum.Last,
		"Solved": sum.Solved, "Total": len(catalog), "Passes": sum.Passes,
		"TotalVariants": totalVariants(), "Attempts": sum.Attempts, "Fails": sum.Fails,
		"FirstTry": sum.FirstTry, "Rows": rows,
	})
}

type failView struct {
	Time, Variant, Diff, Input, Expected, Got string
}

func clip(s string) string {
	const max = 40000
	if len(s) > max {
		return s[:max] + fmt.Sprintf("… (%d more characters)", len(s)-max)
	}
	return s
}

func (d *Dashboard) failures(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	c := challengeByID(r.PathValue("id"))
	d.s.mu.Lock()
	ks := d.s.keys[key]
	if ks == nil || c == nil {
		d.s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	var fails []failView
	if cs := ks.Chals[c.Slug]; cs != nil {
		for i := len(cs.Failures) - 1; i >= 0; i-- {
			e := cs.Failures[i]
			fails = append(fails, failView{fmtTime(e.Time), e.Variant, e.Diff,
				clip(e.Input), clip(e.Expected), clip(e.Got)})
		}
	}
	d.s.mu.Unlock()
	d.render(w, "failures", map[string]any{
		"Key": key, "KeyName": url.PathEscape(key), "ID": c.ID, "Slug": c.Slug,
		"Name": c.Name, "Kind": c.Kind, "Failures": fails,
	})
}
