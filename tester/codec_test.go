package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func unhex(t testing.TB, s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVectors(t *testing.T) {
	vectors := []struct {
		w, h, n  int
		raw, enc string
	}{
		{1, 1, 1, "00", "54434f4d 01000000 01000000 01000000 e0"},
		{2, 1, 3, "000000 ffffff", "54434f4d 02000000 01000000 03000000 e015"},
		{1, 1, 4, "00000000", "54434f4d 01000000 01000000 04000000 c0"},
		{3, 2, 1, "000000 000000", "54434f4d 03000000 02000000 01000000 e2e2"},
		{4, 4, 4,
			"0a141eff 0a141eff 0b151fff 0a141eff 0a141eff 0a141eff 0b151fff 0a141eff" +
				"c8c8c8ff c8c8c800 c8c8c800 c8c8c800 c8c8c800 c9c7c601 05060708 090a0b0c",
			"54434f4d 04000000 04000000 04000000 f6b4a5 e0 957b c2 e1 f301 f0c8c8c8" +
				"f1c8c8c800 e1 e0 f1c9c7c601 f105060708 f1090a0b0c"},
	}
	for i, v := range vectors {
		m := &Image{W: v.w, H: v.h, N: v.n, Pix: unhex(t, v.raw)}
		want := unhex(t, v.enc)
		got, _ := Encode(m, 0)
		if !bytes.Equal(got, want) {
			t.Errorf("vector %d: encode %x, want %x", i, got, want)
		}
		dm, ok := Decode(want, 0)
		if !ok || !bytes.Equal(dm.Pix, m.Pix) {
			t.Errorf("vector %d: decode failed", i)
		}
	}
}

func TestCorpus(t *testing.T) {
	f, err := os.Open("testdata/corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<24)
	nenc, ndec := 0, 0
	for sc.Scan() {
		var e struct {
			Type     string
			W, H, N  int
			Raw, Enc string
			Data     string
			OK       bool
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		switch e.Type {
		case "enc":
			m := &Image{W: e.W, H: e.H, N: e.N, Pix: unhex(t, e.Raw)}
			got, _ := Encode(m, 0)
			if !bytes.Equal(got, unhex(t, e.Enc)) {
				t.Fatalf("encode mismatch %dx%dx%d", e.W, e.H, e.N)
			}
			nenc++
		case "dec":
			m, ok := Decode(unhex(t, e.Data), 0)
			if ok != e.OK || (ok && !bytes.Equal(m.Pix, unhex(t, e.Raw))) {
				t.Fatalf("decode mismatch on %s", e.Data)
			}
			ndec++
		}
	}
	if nenc == 0 || ndec == 0 {
		t.Fatal("empty corpus")
	}
	t.Logf("%d encodes, %d decodes match the original", nenc, ndec)
}

// Decoder quirks that only relax validation must not change valid output.
func TestHeaderRules(t *testing.T) {
	ok := func(w, h, n uint32, body ...byte) bool {
		_, ok := Decode(append(header(w, h, n), body...), 0)
		return ok
	}
	if !ok(1, 1, 1, 0xe0) || !ok(maxWidth, 1, 1, append(bytes.Repeat([]byte{0xf2, 0xff}, 3906), 0xf2, 0x3f)...) {
		t.Error("valid header rejected")
	}
	for _, c := range [][3]uint32{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {1, 1, 5}, {maxWidth + 1, 1, 1}, {1 << 31, 1, 1}} {
		if ok(c[0], c[1], c[2], 0xe0) {
			t.Errorf("header %v accepted", c)
		}
	}
	if _, ok := Decode([]byte("TCOM"), 0); ok {
		t.Error("short header accepted")
	}
}
