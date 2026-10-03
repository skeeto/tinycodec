package main

import (
	"flag"
	"math/rand/v2"
	"sort"
	"testing"
)

var instances = flag.Int("instances", 200, "instances per challenge variant")

func TestChallenges(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	targeted := Quirk(0)
	ops := map[Op]bool{}
	for _, c := range catalog {
		for _, v := range c.Vars {
			worst, total := 0, 0
			for i := 0; i < *instances; i++ {
				in, tries, err := c.Generate(r, v)
				if err != nil {
					t.Errorf("%s (%s) %s: %v", c.ID, c.Slug, v, err)
					break
				}
				total += tries
				worst = max(worst, tries)
				checkInst(t, c, v, in)
				if c.Kind == KEncode {
					_, tr := Encode(in.Img, 0)
					for _, ch := range tr {
						ops[ch.Op] = true
					}
				}
			}
			if c.Muts != nil {
				for _, q := range c.Muts(v) {
					targeted |= q
				}
			}
			if worst > 300 {
				t.Logf("%s %s/%s: mean tries %.1f, worst %d", c.ID, c.Slug, v,
					float64(total)/float64(*instances), worst)
			}
		}
	}
	for op := OpDiff; op <= OpCopy; op++ {
		if !ops[op] {
			t.Errorf("no encode challenge emits %v", op)
		}
	}
	var missing []string
	for i := range quirkNames {
		if targeted&(1<<i) == 0 {
			missing = append(missing, quirkNames[i])
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("quirks targeted by no challenge: %v", missing)
	}
	t.Logf("%d challenges", len(catalog))
}

func checkInst(t *testing.T, c *Challenge, v string, in *Inst) {
	t.Helper()
	switch c.Kind {
	case KEncode:
		if px := in.Img.W * in.Img.H; px > 1000 {
			t.Errorf("%s %s: image too large (%d pixels)", c.Slug, v, px)
		}
	case KDecode:
		if c.Slug != "v-width-limit" && len(in.Data) > 4000 {
			t.Errorf("%s %s: stream too large (%d bytes)", c.Slug, v, len(in.Data))
		}
		if in.WantOK && in.WantImg.W*in.WantImg.H > 1000 {
			t.Errorf("%s %s: decoded image too large", c.Slug, v)
		}
	}
}
