package main

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
)

type Kind string

const (
	KEncode Kind = "encode"
	KDecode Kind = "decode"
)

// Inst is one generated challenge instance with its expected answer.
type Inst struct {
	Img  *Image // encode input
	Data []byte // decode input

	Want    []byte // encode: expected stream
	WantOK  bool   // decode: expected validity
	WantImg *Image // decode: expected image when valid
}

type Challenge struct {
	ID      string // opaque, shown to models
	Slug    string // stable internal name, used in the log
	Name    string // description for the operator
	Kind    Kind
	Invalid bool     // non-twin variants expect an invalid stream
	Vars    []string // variants; all must pass to solve
	Muts    func(v string) []Quirk
	Gen     func(r *rand.Rand, v string) *Inst
}

// Variant helpers: "n3" is a channel count; a "-twin" suffix marks the
// valid near-miss of an invalid-stream challenge.
func chans(ns ...int) []string {
	var v []string
	for _, n := range ns {
		v = append(v, "n"+strconv.Itoa(n))
	}
	return v
}

func withTwins(vs []string) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v, v+"-twin")
	}
	return out
}

func isTwin(v string) bool { return strings.HasSuffix(v, "-twin") }

func varN(v string) int {
	v = strings.TrimSuffix(v, "-twin")
	if strings.HasPrefix(v, "n") {
		if n, err := strconv.Atoi(v[1:]); err == nil {
			return n
		}
	}
	return 3
}

func muts(qs ...Quirk) func(string) []Quirk {
	return func(v string) []Quirk {
		if isTwin(v) {
			return nil
		}
		return qs
	}
}

// mutsN selects mutants that apply to the variant's channel count.
func mutsN(f func(n int) []Quirk) func(string) []Quirk {
	return func(v string) []Quirk {
		if isTwin(v) {
			return nil
		}
		return f(varN(v))
	}
}

const maxTries = 3000

// Generate produces an instance whose expected answer differs from every
// target mutant's answer.
func (c *Challenge) Generate(r *rand.Rand, v string) (*Inst, int, error) {
	var qs []Quirk
	if c.Muts != nil {
		qs = c.Muts(v)
	}
tries:
	for try := 1; try <= maxTries; try++ {
		in := c.Gen(r, v)
		if in == nil {
			continue
		}
		switch c.Kind {
		case KEncode:
			in.Want, _ = Encode(in.Img, 0)
			for _, q := range qs {
				got, err := safeEncode(in.Img, q)
				if err == nil && bytes.Equal(got, in.Want) {
					continue tries
				}
			}
		case KDecode:
			in.WantImg, in.WantOK = Decode(in.Data, 0)
			if c.Invalid && in.WantOK != isTwin(v) {
				continue
			}
			for _, q := range qs {
				m, ok, err := safeDecode(in.Data, q)
				if err == nil && sameDecode(m, ok, in.WantImg, in.WantOK) {
					continue tries
				}
			}
		}
		return in, try, nil
	}
	return nil, maxTries, fmt.Errorf("%s/%s: no instance after %d tries", c.Slug, v, maxTries)
}

func sameDecode(a *Image, aok bool, b *Image, bok bool) bool {
	if aok != bok {
		return false
	}
	if !aok {
		return true
	}
	return a.W == b.W && a.H == b.H && a.N == b.N && bytes.Equal(a.Pix, b.Pix)
}

// Catalog with opaque IDs assigned by a fixed shuffle of the slugs.
var catalog = buildCatalog()

func buildCatalog() []*Challenge {
	all := append(encodeChallenges(), decodeChallenges()...)
	all = append(all, invalidChallenges()...)
	seen := map[string]bool{}
	for _, c := range all {
		if seen[c.Slug] {
			panic("duplicate slug " + c.Slug)
		}
		seen[c.Slug] = true
	}
	key := func(s string) uint64 {
		h := fnv.New64a()
		h.Write([]byte("tcom-ids:" + s))
		return h.Sum64()
	}
	sort.Slice(all, func(i, j int) bool { return key(all[i].Slug) < key(all[j].Slug) })
	for i, c := range all {
		c.ID = fmt.Sprintf("c%02d", i+1)
	}
	return all
}

func challengeByID(id string) *Challenge {
	for _, c := range catalog {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func challengeBySlug(slug string) *Challenge {
	for _, c := range catalog {
		if c.Slug == slug {
			return c
		}
	}
	return nil
}
