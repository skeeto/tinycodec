package main

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"time"
)

//go:embed api.txt
var apiDoc string

type imageJSON struct {
	Width    *int   `json:"width"`
	Height   *int   `json:"height"`
	Channels *int   `json:"channels"`
	Pixels   *[]int `json:"pixels"`
}

func toJSON(m *Image) map[string]any {
	px := make([]int, len(m.Pix))
	for i, b := range m.Pix {
		px[i] = int(b)
	}
	return map[string]any{"width": m.W, "height": m.H, "channels": m.N, "pixels": px}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail400(w http.ResponseWriter, format string, args ...any) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(format, args...)})
}

func newToken() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newRand() *mrand.Rand {
	var seed [32]byte
	rand.Read(seed[:])
	return mrand.New(mrand.NewChaCha8(seed))
}

type API struct{ s *Store }

func (a *API) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, apiDoc)
	})
	mux.HandleFunc("GET /challenges", a.auth(a.list))
	mux.HandleFunc("GET /challenges/{id}", a.auth(a.fetch))
	mux.HandleFunc("POST /challenges/{id}", a.auth(a.submit))
	return mux
}

type keyHandler func(w http.ResponseWriter, r *http.Request, key string)

func (a *API) auth(h keyHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		key = strings.TrimSpace(key)
		if !ok || key == "" || len(key) > 200 {
			writeJSON(w, http.StatusUnauthorized,
				map[string]string{"error": "send your API key as: Authorization: Bearer <key>"})
			return
		}
		h(w, r, key)
	}
}

func (a *API) list(w http.ResponseWriter, r *http.Request, key string) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	ks := a.s.key(key, time.Now())
	type item struct {
		ID     string `json:"id"`
		Kind   Kind   `json:"kind"`
		Needed int    `json:"passes_needed"`
	}
	unsolved := []item{}
	solved := 0
	for _, c := range catalog {
		cs := ks.Chals[c.Slug]
		if cs.solved(c) {
			solved++
			continue
		}
		need := len(c.Vars)
		if cs != nil {
			need -= len(cs.Passed)
		}
		unsolved = append(unsolved, item{c.ID, c.Kind, need})
	}
	sort.Slice(unsolved, func(i, j int) bool { return unsolved[i].ID < unsolved[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"total": len(catalog), "solved": solved, "unsolved": unsolved})
}

func (a *API) fetch(w http.ResponseWriter, r *http.Request, key string) {
	c := challengeByID(r.PathValue("id"))
	if c == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such challenge"})
		return
	}

	// Pick a variant not yet passed, or any once solved.
	a.s.mu.Lock()
	cs := a.s.key(key, time.Now()).chal(c.Slug)
	var pending []string
	for _, v := range c.Vars {
		if _, ok := cs.Passed[v]; !ok {
			pending = append(pending, v)
		}
	}
	a.s.mu.Unlock()
	if len(pending) == 0 {
		pending = c.Vars
	}
	rng := newRand()
	v := pending[rng.IntN(len(pending))]
	inst, _, err := c.Generate(rng, v)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "instance generation failed, try again"})
		return
	}

	tok := newToken()
	a.s.mu.Lock()
	cs.cur = &issued{token: tok, variant: v, inst: inst}
	a.s.mu.Unlock()

	var input any
	if c.Kind == KEncode {
		input = toJSON(inst.Img)
	} else {
		input = map[string]string{"data": hex.EncodeToString(inst.Data)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": c.ID, "kind": c.Kind, "instance": tok, "input": input})
}

func (a *API) submit(w http.ResponseWriter, r *http.Request, key string) {
	c := challengeByID(r.PathValue("id"))
	if c == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such challenge"})
		return
	}
	var body struct {
		Instance string          `json:"instance"`
		Answer   json.RawMessage `json:"answer"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<24))
	if err := dec.Decode(&body); err != nil {
		fail400(w, "request body is not valid JSON: %v", err)
		return
	}
	if body.Instance == "" || len(body.Answer) == 0 || string(body.Answer) == "null" {
		fail400(w, `body must be {"instance": "<token>", "answer": ...}`)
		return
	}

	// Parse before taking the instance, so format errors don't consume it.
	var (
		gotBytes []byte
		gotOK    bool
		gotImg   *Image
	)
	if c.Kind == KEncode {
		var ans struct {
			Data *string `json:"data"`
		}
		if err := json.Unmarshal(body.Answer, &ans); err != nil || ans.Data == nil {
			fail400(w, `encode answer must be {"data": "<hex>"}`)
			return
		}
		b, err := hex.DecodeString(*ans.Data)
		if err != nil {
			fail400(w, "data is not valid hex: %v", err)
			return
		}
		gotBytes = b
	} else {
		var ans struct {
			Valid *bool      `json:"valid"`
			Image *imageJSON `json:"image"`
		}
		if err := json.Unmarshal(body.Answer, &ans); err != nil || ans.Valid == nil {
			fail400(w, `decode answer must be {"valid": false} or {"valid": true, "image": {...}}`)
			return
		}
		gotOK = *ans.Valid
		if gotOK {
			m, msg := parseImage(ans.Image)
			if msg != "" {
				fail400(w, "%s", msg)
				return
			}
			gotImg = m
		}
	}

	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	cs := a.s.key(key, time.Now()).chal(c.Slug)
	cur := cs.cur
	if cur == nil || cur.token != body.Instance {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "unknown, replaced, or already answered instance; fetch a new one"})
		return
	}
	cs.cur = nil

	e := &Event{Time: time.Now().UTC(), Key: key, Challenge: c.Slug, Variant: cur.variant}
	in := cur.inst
	if c.Kind == KEncode {
		e.Pass = bytes.Equal(gotBytes, in.Want)
		if !e.Pass {
			e.Input = jsonString(toJSON(in.Img))
			e.Expected = hex.EncodeToString(in.Want)
			e.Got = hex.EncodeToString(gotBytes)
			e.Diff = diffBytes(in.Want, gotBytes)
		}
	} else {
		e.Pass = sameDecode(gotImg, gotOK, in.WantImg, in.WantOK)
		if !e.Pass {
			e.Input = hex.EncodeToString(in.Data)
			e.Expected = describeDecode(in.WantImg, in.WantOK)
			e.Got = describeDecode(gotImg, gotOK)
			e.Diff = diffDecode(in.WantImg, in.WantOK, gotImg, gotOK)
		}
	}
	if err := a.s.record(e); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record result"})
		return
	}
	result := map[bool]string{true: "pass", false: "fail"}[e.Pass]
	writeJSON(w, http.StatusOK, map[string]string{"result": result})
}

func parseImage(j *imageJSON) (*Image, string) {
	if j == nil || j.Width == nil || j.Height == nil || j.Channels == nil || j.Pixels == nil {
		return nil, `image must have "width", "height", "channels" and "pixels"`
	}
	w, h, n, px := *j.Width, *j.Height, *j.Channels, *j.Pixels
	if w < 0 || h < 0 || n < 0 || int64(w)*int64(h)*int64(n) != int64(len(px)) {
		return nil, fmt.Sprintf("pixels has %d values; width*height*channels is %d",
			len(px), int64(w)*int64(h)*int64(n))
	}
	m := &Image{W: w, H: h, N: n, Pix: make([]byte, len(px))}
	for i, v := range px {
		if v < 0 || v > 255 {
			return nil, fmt.Sprintf("pixels[%d] = %d is outside 0-255", i, v)
		}
		m.Pix[i] = byte(v)
	}
	return m, ""
}

func describeDecode(m *Image, ok bool) string {
	if !ok {
		return `{"valid":false}`
	}
	return jsonString(map[string]any{"valid": true, "image": toJSON(m)})
}

func diffBytes(want, got []byte) string {
	n := min(len(want), len(got))
	for i := 0; i < n; i++ {
		if want[i] != got[i] {
			return fmt.Sprintf("first difference at byte %d: expected %02x, got %02x (lengths %d and %d)",
				i, want[i], got[i], len(want), len(got))
		}
	}
	return fmt.Sprintf("expected %d bytes, got %d; identical up to byte %d", len(want), len(got), n)
}

func diffDecode(want *Image, wok bool, got *Image, gok bool) string {
	switch {
	case wok && !gok:
		return "expected valid, got invalid"
	case !wok && gok:
		return "expected invalid, got valid"
	}
	if want.W != got.W || want.H != got.H || want.N != got.N {
		return fmt.Sprintf("expected %dx%d with %d channels, got %dx%d with %d",
			want.W, want.H, want.N, got.W, got.H, got.N)
	}
	nd := 0
	first := -1
	for i := range want.Pix {
		if want.Pix[i] != got.Pix[i] {
			nd++
			if first < 0 {
				first = i
			}
		}
	}
	p := first / want.N
	return fmt.Sprintf("first difference at pixel (%d,%d) channel %d: expected %d, got %d (%d bytes differ)",
		p%want.W, p/want.W, first%want.N, want.Pix[first], got.Pix[first], nd)
}
