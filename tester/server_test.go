package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

type client struct {
	t   *testing.T
	url string
	key string
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.url+path, rd)
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func imageFrom(m map[string]any) *Image {
	px := m["pixels"].([]any)
	img := &Image{W: int(m["width"].(float64)), H: int(m["height"].(float64)), N: int(m["channels"].(float64))}
	for _, v := range px {
		img.Pix = append(img.Pix, byte(v.(float64)))
	}
	return img
}

// answer computes a bot's answer using the reference codec with quirks q.
func answer(kind string, input map[string]any, q Quirk) any {
	if kind == "encode" {
		out, err := safeEncode(imageFrom(input), q)
		if err != nil {
			out = nil
		}
		return map[string]string{"data": hex.EncodeToString(out)}
	}
	data, _ := hex.DecodeString(input["data"].(string))
	m, ok, err := safeDecode(data, q)
	if err != nil || !ok {
		return map[string]bool{"valid": false}
	}
	return map[string]any{"valid": true, "image": toJSON(m)}
}

// attempt fetches an instance of id and submits the q-bot's answer.
func (c *client) attempt(id string, q Quirk) string {
	code, inst := c.do("GET", "/challenges/"+id, nil)
	if code != 200 {
		c.t.Fatalf("fetch %s: %d %v", id, code, inst)
	}
	ans := answer(inst["kind"].(string), inst["input"].(map[string]any), q)
	code, res := c.do("POST", "/challenges/"+id, map[string]any{"instance": inst["instance"], "answer": ans})
	if code == 400 && q != 0 {
		return "fail" // e.g. a lax decoder "decoding" a negative width
	}
	if code != 200 {
		c.t.Fatalf("submit %s: %d %v", id, code, res)
	}
	return res["result"].(string)
}

func newServer(t *testing.T, logPath string) (*Store, *httptest.Server) {
	s, err := OpenStore(logPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&API{s: s}).routes())
	t.Cleanup(srv.Close)
	return s, srv
}

func TestPerfectBot(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	s, srv := newServer(t, logPath)
	c := &client{t, srv.URL, "perfect"}
	for {
		_, list := c.do("GET", "/challenges", nil)
		unsolved := list["unsolved"].([]any)
		if len(unsolved) == 0 {
			break
		}
		for _, u := range unsolved {
			id := u.(map[string]any)["id"].(string)
			if r := c.attempt(id, 0); r != "pass" {
				t.Fatalf("%s: perfect bot got %s", id, r)
			}
		}
	}
	_, list := c.do("GET", "/challenges", nil)
	if int(list["solved"].(float64)) != len(catalog) {
		t.Fatalf("solved %v of %d", list["solved"], len(catalog))
	}

	// Replaying the log reproduces the dashboard summary.
	want := summarize(s.keys["perfect"])
	s.log.Close()
	s2, err := OpenStore(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := summarize(s2.keys["perfect"]); got != want {
		t.Fatalf("replay mismatch:\n%+v\n%+v", got, want)
	}
}

// Every targeted quirk fails each challenge that targets it, on every
// non-twin variant.
func TestMutantBots(t *testing.T) {
	_, srv := newServer(t, "")
	for i := range quirkNames {
		q := Quirk(1) << i
		c := &client{t, srv.URL, "mutant-" + quirkNames[i]}
		fails := 0
		for _, ch := range catalog {
			targets := false
			for _, v := range ch.Vars {
				if ch.Muts != nil {
					for _, mq := range ch.Muts(v) {
						targets = targets || mq == q
					}
				}
			}
			if !targets {
				continue
			}
			chFails := 0
			for k := 0; k < 3*len(ch.Vars); k++ {
				if c.attempt(ch.ID, q) == "fail" {
					chFails++
				}
			}
			if chFails == 0 {
				t.Errorf("quirk %s never failed %s", quirkNames[i], ch.Slug)
			}
			fails += chFails
		}
		if fails == 0 {
			t.Errorf("quirk %s never failed", quirkNames[i])
		}
	}
}

func TestRequestErrors(t *testing.T) {
	_, srv := newServer(t, "")
	anon := &client{t, srv.URL, ""}
	if code, _ := anon.do("GET", "/challenges", nil); code != 401 {
		t.Errorf("missing key: %d", code)
	}
	c := &client{t, srv.URL, "errors"}
	if code, _ := c.do("GET", "/challenges/c999", nil); code != 404 {
		t.Errorf("unknown challenge: %d", code)
	}
	var enc, dec *Challenge
	for _, ch := range catalog {
		if ch.Kind == KEncode && enc == nil {
			enc = ch
		}
		if ch.Kind == KDecode && dec == nil {
			dec = ch
		}
	}
	_, inst := c.do("GET", "/challenges/"+enc.ID, nil)
	tok := inst["instance"]
	for _, bad := range []any{
		map[string]any{"instance": tok},
		map[string]any{"instance": tok, "answer": map[string]any{"data": "abc"}},
		map[string]any{"instance": tok, "answer": map[string]any{"data": "zz"}},
		map[string]any{"instance": tok, "answer": map[string]any{"valid": true}},
	} {
		if code, out := c.do("POST", "/challenges/"+enc.ID, bad); code != 400 {
			t.Errorf("bad encode answer %v: %d %v", bad, code, out)
		}
	}
	// Format errors don't consume the instance.
	code, out := c.do("POST", "/challenges/"+enc.ID, map[string]any{"instance": tok, "answer": map[string]any{"data": "00"}})
	if code != 200 || out["result"] != "fail" {
		t.Errorf("wrong answer: %d %v", code, out)
	}
	if code, _ := c.do("POST", "/challenges/"+enc.ID, map[string]any{"instance": tok, "answer": map[string]any{"data": "00"}}); code != 409 {
		t.Errorf("reused instance: %d", code)
	}

	_, inst = c.do("GET", "/challenges/"+dec.ID, nil)
	tok = inst["instance"]
	for _, bad := range []any{
		map[string]any{"instance": tok, "answer": map[string]any{"data": "00"}},
		map[string]any{"instance": tok, "answer": map[string]any{"valid": true}},
		map[string]any{"instance": tok, "answer": map[string]any{"valid": true, "image": map[string]any{"width": 1, "height": 1, "channels": 1, "pixels": []int{1, 2}}}},
		map[string]any{"instance": tok, "answer": map[string]any{"valid": true, "image": map[string]any{"width": 1, "height": 1, "channels": 1, "pixels": []int{256}}}},
	} {
		if code, out := c.do("POST", "/challenges/"+dec.ID, bad); code != 400 {
			t.Errorf("bad decode answer %v: %d %v", bad, code, out)
		}
	}
	// A new fetch replaces the instance.
	c.do("GET", "/challenges/"+dec.ID, nil)
	if code, _ := c.do("POST", "/challenges/"+dec.ID, map[string]any{"instance": tok, "answer": map[string]any{"valid": false}}); code != 409 {
		t.Errorf("replaced instance: %d", code)
	}
}
