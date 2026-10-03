package main

// Generator building blocks: an image painter for encode challenges and a
// chunk assembler for decode challenges.

import "math/rand/v2"

// ---- Painter ----

type painter struct {
	r   *rand.Rand
	m   *Image
	pal []uint32
	rep int // pending repeats of prev
	cp  int // pending copies from above
}

func newPainter(r *rand.Rand, w, h, n int) *painter {
	p := &painter{r: r, m: NewImage(w, h, n)}
	for i := 0; i < 2+r.IntN(8); i++ {
		p.pal = append(p.pal, r.Uint32())
	}
	return p
}

func (p *painter) n() int { return p.m.N }

// prev is the previous pixel in raster order, which is always the
// encoder's prev for a canonical encoding.
func (p *painter) prev(x, y int) uint32 {
	switch {
	case x > 0:
		return p.m.At(x-1, y)
	case y > 0:
		return p.m.At(p.m.W-1, y-1)
	}
	return initPix
}

func (p *painter) above(x, y int) uint32 {
	if y == 0 {
		return initPix
	}
	return p.m.At(x, y-1)
}

func (p *painter) pred(x, y int) uint32 { return Pred(p.prev(x, y), p.above(x, y), x) }

// fill paints in raster order. f may set rep or cp to continue a run.
func (p *painter) fill(f func(x, y int) uint32) *Image {
	for y := 0; y < p.m.H; y++ {
		for x := 0; x < p.m.W; x++ {
			var v uint32
			switch {
			case p.rep > 0:
				p.rep--
				v = p.prev(x, y)
			case p.cp > 0 && y > 0:
				p.cp--
				v = p.above(x, y)
			default:
				p.cp = 0
				v = f(x, y)
			}
			p.m.Set(x, y, v)
		}
	}
	return p.m
}

func (p *painter) in(lo, hi int) int { return lo + p.r.IntN(hi-lo+1) }

func (p *painter) pick(weights ...int) int {
	t := 0
	for _, w := range weights {
		t += w
	}
	r := p.r.IntN(t)
	for i, w := range weights {
		if r < w {
			return i
		}
		r -= w
	}
	panic("unreachable")
}

func (p *painter) palv() uint32 { return p.pal[p.r.IntN(len(p.pal))] }

// delta returns b with each channel moved by a value in lo..hi.
func (p *painter) delta(b uint32, lo, hi int) uint32 {
	return addD(b, p.in(lo, hi), p.in(lo, hi), p.in(lo, hi))
}

// luma returns b moved by dg in glo..ghi and dr-dg, db-dg in rlo..rhi.
func (p *painter) luma(b uint32, glo, ghi, rlo, rhi int) uint32 {
	dg := p.in(glo, ghi)
	return addD(b, dg+p.in(rlo, rhi), dg, dg+p.in(rlo, rhi))
}

// withA replaces the alpha byte.
func withA(v uint32, a uint32) uint32 { return v&0xffffff | a<<24 }

// mixed is a general-purpose strategy covering every chunk type.
func (p *painter) mixed(x, y int) uint32 {
	b := p.pred(x, y)
	switch p.pick(3, 2, 4, 3, 2, 3, 2) {
	case 0:
		p.rep = p.r.IntN(20)
		return p.prev(x, y)
	case 1:
		p.cp = p.r.IntN(12)
		return p.above(x, y)
	case 2:
		return p.delta(b, -2, 1)
	case 3:
		return p.luma(b, -16, 15, -16, 15)
	case 4:
		return p.luma(b, -32, 31, -32, 31)
	case 5:
		return p.palv()
	}
	return p.r.Uint32()
}

// ---- Assembler ----

// asm builds a stream chunk by chunk, decoding as it goes (with quirks q,
// normally 0) so generators can consult decoder state.
type asm struct {
	r *rand.Rand
	d *Decoder
}

func newAsm(r *rand.Rand, w, h, n int, q Quirk) *asm {
	d, ok := NewDecoder(header(uint32(w), uint32(h), uint32(n)), q)
	if !ok {
		panic("asm: bad header")
	}
	return &asm{r: r, d: d}
}

func (a *asm) bytes() []byte { return a.d.S }
func (a *asm) done() bool    { return a.d.Done() }
func (a *asm) x() int        { return a.d.X() }
func (a *asm) y() int        { return a.d.Y() }
func (a *asm) w() int        { return int(a.d.W) }
func (a *asm) n() int        { return a.d.N }
func (a *asm) rem() int      { return a.w() - a.x() }
func (a *asm) prev() uint32  { return a.d.P }
func (a *asm) pred() uint32  { return Pred(a.d.P, a.d.above(a.d.Pos), a.x()) }
func (a *asm) above() uint32 { return a.d.above(a.d.Pos) }

// emit appends a chunk, reporting whether it decoded.
func (a *asm) emit(b ...byte) bool {
	if a.done() {
		return false
	}
	a.d.S = append(a.d.S, b...)
	return a.d.Step()
}

// raw appends bytes without decoding them.
func (a *asm) raw(b ...byte) { a.d.S = append(a.d.S, b...) }

func cDiff(dr, dg, db int) []byte {
	return []byte{byte((dr + 2) | (dg+2)<<2 | (db+2)<<4)}
}

func cLuma5(dg, rg, bg int) []byte {
	z := (dg + 16) | (rg+16)<<5 | (bg+16)<<10
	return []byte{byte(0x40 + z>>8), byte(z)}
}

func cLuma6(dg, rg, bg int) []byte {
	z := (dg + 32) | (rg+32)<<6 | (bg+32)<<12
	return []byte{byte(0xf4 + z>>16), byte(z), byte(z >> 8)}
}

func cIndex(i int) []byte  { return []byte{byte(0xc0 + i)} }
func cRun(k int) []byte    { return []byte{byte(0xdf + k)} }
func cLong(k int) []byte   { return []byte{0xf2, byte(k - 1)} }
func cCopy(k int) []byte   { return []byte{0xf3, byte(k - 1)} }
func cRGB(v uint32) []byte { return []byte{0xf0, byte(v), byte(v >> 8), byte(v >> 16)} }
func cRGBA(v uint32) []byte {
	return []byte{0xf1, byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

// soup weights for random valid chunks.
type soupW struct {
	Diff, Luma5, Luma6, Index, Run, Long, Copy, RGB, RGBA int
	Stray                                                 bool // allow bytes beyond the channel count
}

var soupAll = soupW{3, 3, 2, 2, 2, 1, 2, 2, 1, true}
var soupClean = soupW{3, 3, 2, 2, 2, 1, 2, 2, 1, false}

// chunk returns one random valid chunk for the current position.
func (a *asm) chunk(s soupW) []byte {
	p := &painter{r: a.r}
	n := a.n()
	free := func(i int) bool { return s.Stray || i < n }
	field := func(i, lo, hi int) int {
		if !free(i) {
			return 0
		}
		return p.in(lo, hi)
	}
	maxk := func(m int) int { return min(m, a.rem()) }
	for {
		op := p.pick(s.Diff, s.Luma5, s.Luma6, s.Index, s.Run, s.Long, s.Copy, s.RGB, s.RGBA)
		switch op {
		case 0:
			return cDiff(field(0, -2, 1), field(1, -2, 1), field(2, -2, 1))
		case 1, 2:
			lim := 16
			if op == 2 {
				lim = 32
			}
			dg := field(1, -lim, lim-1)
			// keep missing channels at zero difference unless strays
			rg, bg := field(0, -lim, lim-1), field(2, -lim, lim-1)
			if !free(0) {
				rg = -dg
			}
			if !free(2) {
				bg = -dg
			}
			if rg < -lim || rg > lim-1 || bg < -lim || bg > lim-1 {
				continue
			}
			if lim == 16 {
				return cLuma5(dg, rg, bg)
			}
			return cLuma6(dg, rg, bg)
		case 3:
			return cIndex(a.r.IntN(32))
		case 4:
			return cRun(1 + a.r.IntN(maxk(16)))
		case 5:
			return cLong(1 + a.r.IntN(maxk(256)))
		case 6:
			if a.y() == 0 {
				continue
			}
			return cCopy(1 + a.r.IntN(maxk(256)))
		case 7:
			v := a.r.Uint32()
			if !s.Stray {
				v = norm(v, n)
			}
			return cRGB(v)
		case 8:
			v := a.r.Uint32()
			if !s.Stray {
				v = norm(v, n)
			}
			return cRGBA(v)
		}
	}
}

// soup emits random chunks until the image is complete.
func (a *asm) soup(s soupW) {
	for !a.done() {
		if !a.emit(a.chunk(s)...) {
			panic("asm: soup chunk rejected")
		}
	}
}

// soupN emits up to count random chunks, leaving at least keep pixels.
func (a *asm) soupN(s soupW, count, keep int) {
	for i := 0; i < count && a.d.Total-a.d.Pos > int64(keep); i++ {
		c := a.chunk(s)
		// avoid consuming the reserved pixels with a long run
		if c[0] >= 0xe0 && c[0] <= 0xf3 && c[0] != 0xf0 && c[0] != 0xf1 {
			continue
		}
		if !a.emit(c...) {
			panic("asm: soup chunk rejected")
		}
	}
}

// probe emits OP_INDEX at the slot where prev should have been stored,
// revealing whether a decoder hashed the same value.
func (a *asm) probe() bool { return a.emit(cIndex(Hash(a.prev()))...) }
