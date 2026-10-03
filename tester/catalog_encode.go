package main

import "math/rand/v2"

// Encode challenges: the model must produce the canonical encoding.

func encC(slug, name string, vars []string, m func(string) []Quirk,
	gen func(p *painter) *Image, dims func(r *rand.Rand, n int) (int, int),
	pred func(m *Image, tr []Chunk) bool) *Challenge {
	return &Challenge{
		Slug: slug, Name: name, Kind: KEncode, Vars: vars, Muts: m,
		Gen: func(r *rand.Rand, v string) *Inst {
			n := varN(v)
			w, h := dims(r, n)
			img := gen(newPainter(r, w, h, n))
			if img == nil {
				return nil
			}
			if pred != nil {
				if _, tr := Encode(img, 0); !pred(img, tr) {
					return nil
				}
			}
			return &Inst{Img: img}
		},
	}
}

func dimsIn(wlo, whi, hlo, hhi int) func(*rand.Rand, int) (int, int) {
	return func(r *rand.Rand, _ int) (int, int) {
		return wlo + r.IntN(whi-wlo+1), hlo + r.IntN(hhi-hlo+1)
	}
}

func anyChunk(tr []Chunk, f func(c Chunk) bool) bool {
	for _, c := range tr {
		if f(c) {
			return true
		}
	}
	return false
}

func countChunks(tr []Chunk, f func(c Chunk) bool) int {
	k := 0
	for _, c := range tr {
		if f(c) {
			k++
		}
	}
	return k
}

func isDL(c Chunk) bool { return c.Op == OpDiff || c.Op == OpLuma5 || c.Op == OpLuma6 }
func isLit(c Chunk) bool {
	return c.Op == OpRGB || c.Op == OpRGBA
}
func alpha(v uint32) uint32 { return v >> 24 }

// lumaFields returns dg, dr-dg, db-dg for a difference chunk.
func lumaFields(c Chunk) [3]int { return [3]int{c.D[1], c.D[0] - c.D[1], c.D[2] - c.D[1]} }

// wraps reports whether any channel difference wraps around 0/255.
func wraps(c Chunk) bool {
	for j := 0; j < 3; j++ {
		s := int(c.Pred>>(8*j)&255) + c.D[j]
		if s < 0 || s > 255 {
			return true
		}
	}
	return false
}

// hits reports whether vals contains every one of want.
func hits(vals map[int]bool, want ...int) bool {
	for _, w := range want {
		if !vals[w] {
			return false
		}
	}
	return true
}

func encodeChallenges() []*Challenge {
	small := dimsIn(3, 12, 2, 8)
	return []*Challenge{
		encC("e-initial-prev", "First pixel equals the initial prev {0,0,0,255}",
			chans(1, 3, 4), muts(QInitPrev),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if x == 0 && y == 0 {
						return initPix
					}
					return p.mixed(x, y)
				})
			}, small,
			func(_ *Image, tr []Chunk) bool { return tr[0].Op == OpRun || tr[0].Op == OpLongRun }),

		encC("e-zero-index", "Pixel {0,0,0,0} matches the zero-initialized array (OP_INDEX 0)",
			chans(4), muts(QArrayInit),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if p.r.IntN(6) == 0 {
						return 0
					}
					return p.mixed(x, y)
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpIndex && c.V == 0 })
			}),

		encC("e-row0-pred", "First-row prediction averages prev with the implicit above",
			chans(1, 3, 4), muts(QRow0Prev),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if p.r.IntN(3) == 0 {
						return p.luma(p.pred(x, y), -16, 15, -8, 7)
					}
					return p.delta(p.pred(x, y), -2, 1)
				})
			}, dimsIn(4, 16, 1, 3),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return isDL(c) && c.Y == 0 && c.X > 0 })
			}),

		encC("e-row0-nocopy", "First-row pixel equal to the implicit above is not a copy",
			chans(1, 3, 4), muts(QCopyRow0),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if y == 0 && p.r.IntN(3) == 0 {
						return initPix
					}
					return p.mixed(x, y)
				})
			}, dimsIn(4, 14, 1, 4),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool {
					return c.Y == 0 && c.X > 0 && c.V == initPix && c.Prev != initPix
				})
			}),

		encC("e-col0-pred", "First-column prediction is the pixel above",
			chans(1, 3, 4), muts(QCol0Avg),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if x == 0 && y > 0 {
						return p.delta(p.above(x, y), -2, 1)
					}
					return p.mixed(x, y)
				})
			}, dimsIn(2, 8, 3, 10),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool {
					return isDL(c) && c.X == 0 && c.Y > 0 && c.Prev != c.Above
				})
			}),

		encC("e-pred-round", "Prediction average rounds down",
			chans(1, 3, 4), muts(QPredRound),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if p.r.IntN(5) == 0 {
						return p.r.Uint32()
					}
					return p.delta(p.pred(x, y), -2, 1)
				})
			}, small, nil),

		encC("e-alpha-literals", "Alpha changes: RGBA, RGB inheriting a non-opaque alpha, and DIFF/LUMA keeping it",
			chans(4), muts(QRGBOpaque, QPredAvgA),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					b := p.pred(x, y)
					switch p.pick(4, 2, 2, 1) {
					case 0:
						return p.delta(b, -2, 1)
					case 1:
						return withA(p.luma(b, -16, 15, -16, 15), uint32(p.r.IntN(256)))
					case 2:
						return withA(p.r.Uint32(), alpha(p.prev(x, y)))
					}
					return p.r.Uint32()
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpRGBA && c.X > 0 }) &&
					anyChunk(tr, func(c Chunk) bool { return c.Op == OpRGB && alpha(c.Prev) != 255 }) &&
					anyChunk(tr, func(c Chunk) bool { return isDL(c) && alpha(c.V) != 255 })
			}),

		encC("e-col0-alpha-above", "First column: alpha equal to above but not prev uses DIFF/LUMA",
			chans(4), muts(QGatePrev, QCol0A),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if x == 0 && y > 0 {
						return p.delta(p.above(x, y), -2, 1)
					}
					if p.r.IntN(3) == 0 {
						return p.r.Uint32()
					}
					return p.delta(p.pred(x, y), -2, 1)
				})
			}, dimsIn(2, 6, 3, 10),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool {
					return isDL(c) && c.X == 0 && c.Y > 0 && alpha(c.Above) != alpha(c.Prev)
				})
			}),

		encC("e-col0-alpha-prev", "First column: alpha equal to prev but not above uses OP_RGB",
			chans(4), muts(QRGBvsPred, QCol0A),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if x == 0 && y > 0 {
						return withA(p.delta(p.above(x, y), -2, 1), alpha(p.prev(x, y)))
					}
					if p.r.IntN(3) == 0 {
						return p.r.Uint32()
					}
					return p.delta(p.pred(x, y), -2, 1)
				})
			}, dimsIn(2, 6, 3, 10),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool {
					return c.Op == OpRGB && c.X == 0 && c.Y > 0 && alpha(c.Above) != alpha(c.V)
				})
			}),

		encC("e-diff-codes", "Every reachable OP_DIFF code, including range edges",
			chans(1, 2, 3, 4), muts(QDiffSwap, QRangeNarrowLo, QRangeNarrowHi),
			func(p *painter) *Image {
				var codes [][3]int
				for i := 0; i < 64; i++ {
					codes = append(codes, [3]int{i&3 - 2, i>>2&3 - 2, i>>4&3 - 2})
				}
				p.r.Shuffle(len(codes), func(i, j int) { codes[i], codes[j] = codes[j], codes[i] })
				k := 0
				return p.fill(func(x, y int) uint32 {
					if x == 0 {
						return p.r.Uint32()
					}
					d := codes[k%64]
					k++
					return addD(p.pred(x, y), d[0], d[1], d[2])
				})
			}, func(r *rand.Rand, n int) (int, int) {
				switch n {
				case 1:
					return 5 + r.IntN(4), 3
				case 2:
					return 8 + r.IntN(4), 4
				}
				return 12 + r.IntN(4), 12
			},
			func(m *Image, tr []Chunk) bool {
				seen := map[byte]bool{}
				for _, c := range tr {
					if c.Op == OpDiff {
						seen[c.Bytes[0]] = true
					}
				}
				want := map[int]int{1: 4, 2: 16, 3: 64, 4: 64}[m.N]
				return len(seen) == want
			}),

		encC("e-diff-wrap", "OP_DIFF differences wrap around 0 and 255",
			chans(1, 3, 4), muts(QNoWrap),
			func(p *painter) *Image {
				edge := []int{0, 1, 254, 255}
				return p.fill(func(x, y int) uint32 {
					v := uint32(0)
					for j := 0; j < 4; j++ {
						v |= uint32(edge[p.r.IntN(4)]) << (8 * j)
					}
					return withA(v, 255)
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpDiff && wraps(c) })
			}),

		encC("e-diff-outside", "Differences just outside the OP_DIFF range use OP_LUMA5",
			chans(1, 3, 4), muts(QRangeWide, QRangeNarrowLo),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					return p.delta(p.pred(x, y), -3, 2)
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool {
					return c.Op == OpLuma5 && rangeq(-3, 2, 0).in(c.D[:]...)
				})
			}),

		encC("e-luma5", "OP_LUMA5 with random fields",
			chans(1, 2, 3, 4), mutsN(func(n int) []Quirk {
				if n == 1 {
					return []Quirk{QLumaSwap}
				}
				return []Quirk{QLumaSwap, QLumaAbs}
			}),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					return p.luma(p.pred(x, y), -16, 15, -16, 15)
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return countChunks(tr, func(c Chunk) bool { return c.Op == OpLuma5 }) >= 6
			}),

		encC("e-luma5-edges", "OP_LUMA5 fields at -16 and 15",
			chans(1, 3, 4), muts(QRangeNarrowLo, QRangeNarrowHi),
			func(p *painter) *Image {
				ext := func() int { return []int{-16, 15, p.in(-16, 15)}[p.r.IntN(3)] }
				return p.fill(func(x, y int) uint32 {
					dg := ext()
					return addD(p.pred(x, y), dg+ext(), dg, dg+ext())
				})
			}, dimsIn(6, 12, 3, 8),
			func(m *Image, tr []Chunk) bool {
				var f [3]map[int]bool
				for i := range f {
					f[i] = map[int]bool{}
				}
				for _, c := range tr {
					if c.Op == OpLuma5 {
						for i, v := range lumaFields(c) {
							f[i][v] = true
						}
					}
				}
				if m.N == 1 {
					return hits(f[1], -16, 15)
				}
				return hits(f[0], -16, 15) && hits(f[1], -16, 15) && hits(f[2], -16, 15)
			}),

		encC("e-luma-relative", "Luma fields are relative to dg: dr and db may exceed the field range",
			chans(3, 4), muts(QLumaAbs),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					s := 1 - 2*p.r.IntN(2)
					if p.r.IntN(2) == 0 {
						dg := s * p.in(8, 15)
						return addD(p.pred(x, y), dg+s*p.in(5, 15), dg, dg+p.in(-16, 15))
					}
					dg := s * p.in(17, 31)
					return addD(p.pred(x, y), dg+s*p.in(10, 31), dg, dg+p.in(-32, 31))
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpLuma5 && !rangeq(-16, 15, 0).in(c.D[0]) }) &&
					anyChunk(tr, func(c Chunk) bool { return c.Op == OpLuma6 && !rangeq(-32, 31, 0).in(c.D[0]) })
			}),

		encC("e-luma6-tags", "OP_LUMA6 with every reachable tag; payload byte order",
			chans(1, 2, 3, 4), muts(QLuma6BE, QLumaSwap),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					return p.luma(p.pred(x, y), -32, 31, -32, 31)
				})
			}, small,
			func(m *Image, tr []Chunk) bool {
				tags := map[byte]bool{}
				for _, c := range tr {
					if c.Op == OpLuma6 {
						tags[c.Bytes[0]] = true
					}
				}
				if m.N == 1 {
					return tags[0xf6]
				}
				return len(tags) == 4
			}),

		encC("e-luma6-edges", "OP_LUMA6 fields at -32 and 31, and at the LUMA5 boundary",
			chans(1, 3, 4), muts(QRangeNarrowLo, QRangeNarrowHi, QRangeWide),
			func(p *painter) *Image {
				ext := func() int { return []int{-32, 31, -17, 16, p.in(-32, 31)}[p.r.IntN(5)] }
				return p.fill(func(x, y int) uint32 {
					dg := ext()
					return addD(p.pred(x, y), dg+ext(), dg, dg+ext())
				})
			}, dimsIn(10, 16, 4, 8),
			func(m *Image, tr []Chunk) bool {
				var f [3]map[int]bool
				for i := range f {
					f[i] = map[int]bool{}
				}
				for _, c := range tr {
					if c.Op == OpLuma6 {
						for i, v := range lumaFields(c) {
							f[i][v] = true
						}
					}
				}
				if m.N == 1 {
					return hits(f[1], -32, 31, 16)
				}
				return hits(f[0], -32, 31) && hits(f[1], -32, 31) && hits(f[2], -32, 31) &&
					(f[0][16] || f[1][16] || f[2][16])
			}),

		encC("e-luma6-outside", "Differences just outside the OP_LUMA6 range use a literal",
			chans(1, 3, 4), muts(QRangeWide),
			func(p *painter) *Image {
				ext := func() int { return []int{-33, 32, -32, 31, p.in(-32, 31)}[p.r.IntN(5)] }
				return p.fill(func(x, y int) uint32 {
					dg := []int{0, p.in(-20, 20)}[p.r.IntN(2)]
					return addD(p.pred(x, y), dg+ext(), dg, dg+ext())
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool {
					f := lumaFields(c)
					return c.Op == OpRGB && c.X > 0 && rangeq(-33, 32, 0).in(f[:]...)
				})
			}),

		encC("e-luma-wrap", "OP_LUMA5/OP_LUMA6 differences wrap around 0 and 255",
			chans(1, 3, 4), muts(QNoWrap),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					v := uint32(0)
					for j := 0; j < 4; j++ {
						c := p.in(-20, 20)
						v |= uint32(c&255) << (8 * j)
					}
					return withA(v, 255)
				})
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return (c.Op == OpLuma5 || c.Op == OpLuma6) && wraps(c) })
			}),

		encC("e-index", "OP_INDEX for recurring values (hash includes implicit alpha)",
			chans(1, 3, 4), mutsN(func(n int) []Quirk {
				if n < 4 {
					return []Quirk{QHashA0, QHash6}
				}
				return []Quirk{QHash6}
			}),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 { return p.palv() })
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return countChunks(tr, func(c Chunk) bool { return c.Op == OpIndex }) >= 5
			}),

		encC("e-index-precedence", "OP_INDEX takes precedence over OP_DIFF",
			chans(1, 3, 4), muts(QIndexLate),
			func(p *painter) *Image {
				base := p.r.Uint32()
				var pal []uint32
				for i := 0; i < 5; i++ {
					pal = append(pal, p.delta(base, -1, 1))
				}
				return p.fill(func(x, y int) uint32 { return pal[p.r.IntN(len(pal))] })
			}, small,
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool {
					return c.Op == OpIndex && rangeq(-2, 1, 0).in(s8(c.V-c.Pred), s8(c.V>>8-c.Pred>>8), s8(c.V>>16-c.Pred>>16))
				})
			}),

		encC("e-runs-not-indexed", "Run and copy pixels never enter the array",
			chans(1, 3, 4), muts(QRunInsert),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					switch p.pick(2, 2, 3) {
					case 0:
						p.cp = p.r.IntN(4)
						return p.above(x, y)
					case 1:
						p.rep = p.r.IntN(3)
						return p.palv()
					}
					return p.palv()
				})
			}, small, nil),

		encC("e-run-short", "OP_RUN for runs of 1 to 16",
			chans(1, 3, 4), muts(QRun15, QRunBias),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					p.rep = []int{1, 16, 15, p.in(1, 16)}[p.r.IntN(4)]
					return p.r.Uint32()
				})
			}, dimsIn(24, 40, 1, 3),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpRun && c.K == 16 }) &&
					anyChunk(tr, func(c Chunk) bool { return c.Op == OpRun && c.K == 1 })
			}),

		encC("e-run-17", "OP_LONGRUN from 17 pixels",
			chans(1, 3, 4), muts(QRun15, QLongBias),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					p.rep = []int{16, 17, p.in(17, 30)}[p.r.IntN(3)]
					return p.r.Uint32()
				})
			}, dimsIn(30, 50, 1, 3),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpLongRun && c.K == 17 }) &&
					anyChunk(tr, func(c Chunk) bool { return c.Op == OpRun && c.K == 16 })
			}),

		encC("e-run-256", "OP_LONGRUN of 256, and longer runs split",
			chans(1, 3), muts(QCap255, QLongBias),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if x == 0 {
						p.rep = 256 + p.r.IntN(p.m.W-257)
					}
					return p.r.Uint32()
				})
			}, dimsIn(262, 300, 1, 2),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpLongRun && c.K == 256 })
			}),

		encC("e-run-rowend", "Runs stop at the row end; the next row starts a new run of prev",
			chans(1, 3, 4), muts(QCopyFirst),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if p.r.IntN(2) == 0 {
						p.rep = p.r.IntN(30)
						return p.prev(x, y)
					}
					return p.palv()
				})
			}, dimsIn(4, 16, 3, 8),
			func(m *Image, tr []Chunk) bool {
				for i := 1; i < len(tr); i++ {
					a, b := tr[i-1], tr[i]
					if a.X+a.K == m.W && b.X == 0 && (b.Op == OpRun || b.Op == OpLongRun) && a.Op != OpCopy {
						return true
					}
				}
				return false
			}),

		encC("e-copy", "OP_COPY from the row above",
			chans(1, 3, 4), nil,
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if y > 0 && p.r.IntN(2) == 0 {
						p.cp = p.r.IntN(20)
						return p.above(x, y)
					}
					return p.mixed(x, y)
				})
			}, dimsIn(6, 20, 2, 6),
			func(_ *Image, tr []Chunk) bool {
				return countChunks(tr, func(c Chunk) bool { return c.Op == OpCopy && c.K > 1 }) >= 2
			}),

		encC("e-copy-1", "OP_COPY for a single pixel",
			chans(1, 3, 4), muts(QCopyMin2),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if y > 0 && p.r.IntN(3) == 0 {
						return p.above(x, y)
					}
					return p.r.Uint32()
				})
			}, dimsIn(4, 12, 2, 5),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpCopy && c.K == 1 })
			}),

		encC("e-copy-256", "OP_COPY of 256, and longer copies split",
			chans(1, 3), muts(QCap255, QLongBias),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if y > 0 && x == 0 {
						p.cp = 256 + p.r.IntN(p.m.W-257)
						return p.above(x, y)
					}
					return p.r.Uint32()
				})
			}, dimsIn(262, 290, 2, 2),
			func(_ *Image, tr []Chunk) bool {
				return anyChunk(tr, func(c Chunk) bool { return c.Op == OpCopy && c.K == 256 })
			}),

		encC("e-run-kind", "Run of prev beats copy; run kind is fixed by its first pixel",
			chans(1, 3, 4), muts(QCopyFirst, QKindSwitch),
			func(p *painter) *Image {
				pal := p.pal[:2]
				return p.fill(func(x, y int) uint32 {
					switch p.pick(2, 2, 3) {
					case 0:
						p.cp = p.r.IntN(5)
						return p.above(x, y)
					case 1:
						p.rep = p.r.IntN(4)
						return p.prev(x, y)
					}
					return pal[p.r.IntN(2)]
				})
			}, dimsIn(5, 14, 3, 7), nil),

		encC("e-prev-after-copy", "After OP_COPY, prev is the last copied pixel",
			chans(1, 3, 4), muts(QPrevAfterCopy),
			func(p *painter) *Image {
				return p.fill(func(x, y int) uint32 {
					if y > 0 && p.r.IntN(3) == 0 {
						p.cp = p.r.IntN(4)
						return p.above(x, y)
					}
					return p.delta(p.pred(x, y), -2, 1)
				})
			}, dimsIn(4, 12, 3, 6), nil),

		encC("e-width1", "Single-column image",
			chans(1, 3, 4), muts(QCopyFirst),
			func(p *painter) *Image { return p.fill(p.mixed) },
			dimsIn(1, 1, 20, 60), nil),

		encC("e-height1", "Single-row image",
			chans(1, 3, 4), muts(QRow0Prev),
			func(p *painter) *Image { return p.fill(p.mixed) },
			dimsIn(30, 120, 1, 1), nil),

		encC("e-noise", "Random noise: literals only, OP_RGB below 4 channels",
			chans(1, 2, 3), nil,
			func(p *painter) *Image { return p.fill(func(x, y int) uint32 { return p.r.Uint32() }) },
			small, nil),

		encC("e-mixed", "Mixed content",
			chans(1, 2, 3, 4), nil,
			func(p *painter) *Image { return p.fill(p.mixed) },
			dimsIn(8, 20, 8, 16), nil),
	}
}
