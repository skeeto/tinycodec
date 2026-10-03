package main

import "math/rand/v2"

// Decode challenges: the model must decode the stream or report it invalid.

func decC(slug, name string, vars []string, m func(string) []Quirk,
	gen func(r *rand.Rand, v string) []byte) *Challenge {
	return &Challenge{
		Slug: slug, Name: name, Kind: KDecode, Vars: vars, Muts: m,
		Gen: func(r *rand.Rand, v string) *Inst {
			data := gen(r, v)
			if data == nil {
				return nil
			}
			return &Inst{Data: data}
		},
	}
}

func invC(slug, name string, vars []string, m func(string) []Quirk,
	gen func(r *rand.Rand, v string) []byte) *Challenge {
	c := decC(slug, name, withTwins(vars), m, gen)
	c.Invalid = true
	return c
}

func rdims(r *rand.Rand, wlo, whi, hlo, hhi int) (int, int) {
	return wlo + r.IntN(whi-wlo+1), hlo + r.IntN(hhi-hlo+1)
}

// build runs f on a fresh assembler, recovering from assembly failures.
func build(r *rand.Rand, w, h, n int, q Quirk, f func(a *asm) bool) (out []byte) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	a := newAsm(r, w, h, n, q)
	if !f(a) {
		return nil
	}
	return a.bytes()
}

// strayValue returns a value carrying bytes beyond n channels.
func strayValue(r *rand.Rand, n int) uint32 {
	for {
		v := r.Uint32()
		if v != norm(v, n) {
			return v
		}
	}
}

func decodeChallenges() []*Challenge {
	return []*Challenge{
		decC("d-roundtrip", "Canonical stream of mixed content",
			chans(1, 2, 3, 4), nil,
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 6, 20, 4, 14)
				p := newPainter(r, w, h, varN(v))
				out, _ := Encode(p.fill(p.mixed), 0)
				return out
			}),

		decC("d-soup", "Non-canonical chunk sequence, every chunk type",
			chans(1, 2, 3, 4), nil,
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 1, 20, 1, 12)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					a.soup(soupAll)
					return true
				})
			}),

		decC("d-initial", "Initial state: prev {0,0,0,255}, zeroed array",
			chans(1, 3, 4), mutsN(func(n int) []Quirk {
				if n < 4 { // only visible through a later hash lookup
					return []Quirk{QInitPrev}
				}
				return []Quirk{QInitPrev, QArrayInit}
			}),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 3, 10, 1, 4)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					first := [][]byte{cRun(1 + r.IntN(min(3, w))), cDiff(r.IntN(4)-2, r.IntN(4)-2, r.IntN(4)-2),
						cIndex(0), cIndex(r.IntN(32)), cLuma5(r.IntN(9)-4, r.IntN(9)-4, r.IntN(9)-4),
						cRGB(r.Uint32())}
					if !a.emit(first[r.IntN(len(first))]...) {
						return false
					}
					if r.IntN(2) == 0 {
						a.probe() // reveals a stray alpha inherited from the initial prev
					}
					for !a.done() && r.IntN(3) > 0 {
						a.emit(cIndex(r.IntN(32))...)
					}
					a.soup(soupAll)
					return true
				})
			}),

		decC("d-index0-stray", "OP_INDEX 0 first yields {0,0,0,0}; its zero alpha carries on",
			chans(1, 2, 3), muts(QDNormalize),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 10, 1, 4)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					rgb := r.Uint32()
					a.emit(cIndex(0)...)
					a.emit(cRGB(rgb)...)
					a.probe()
					a.soup(soupAll)
					return true
				})
			}),

		decC("d-predictor", "Prediction in the first row, first column, and interior",
			chans(1, 3, 4), mutsN(func(n int) []Quirk {
				if n < 4 { // a wrong implicit alpha is invisible below 4 channels
					return []Quirk{QRow0Prev, QCol0Avg, QPredRound}
				}
				return []Quirk{QRow0Prev, QRow0Above, QCol0Avg, QPredRound}
			}),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 3, 10, 3, 8)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					a.soup(soupW{Diff: 6, Luma5: 3, Luma6: 2, RGB: 2, Stray: false})
					return true
				})
			}),

		decC("d-col0-alpha", "First-column prediction takes alpha from above",
			chans(4), muts(QCol0A),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 2, 6, 3, 8)
				return build(r, w, h, 4, 0, func(a *asm) bool {
					a.soup(soupW{Diff: 5, Luma5: 2, RGBA: 3})
					return true
				})
			}),

		decC("d-interior-alpha", "Interior prediction copies alpha from prev, not an average",
			chans(4), muts(QPredAvgA),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 3, 8, 2, 6)
				return build(r, w, h, 4, 0, func(a *asm) bool {
					a.soup(soupW{Diff: 5, Luma5: 2, Luma6: 1, RGBA: 3})
					return true
				})
			}),

		decC("d-diff-codes", "Every OP_DIFF code, with wraparound",
			chans(1, 3, 4), muts(QDiffSwap),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 10, 14, 8, 10)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					for _, c := range r.Perm(64) {
						if r.IntN(8) == 0 {
							a.emit(cRGB(r.Uint32())...)
						}
						if !a.emit(byte(c)) {
							return false
						}
					}
					a.soup(soupAll)
					return true
				})
			}),

		decC("d-luma5", "OP_LUMA5 fields, extremes, and wraparound",
			chans(1, 3, 4), muts(QLumaSwap, QLumaAbs),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 10, 3, 8)
				ext := func() int { return []int{-16, 15, r.IntN(32) - 16}[r.IntN(3)] }
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					for !a.done() {
						if r.IntN(5) == 0 {
							a.emit(cRGB(r.Uint32())...)
						} else {
							a.emit(cLuma5(ext(), ext(), ext())...)
						}
					}
					return true
				})
			}),

		decC("d-luma6", "OP_LUMA6 with every tag, extremes, and payload byte order",
			chans(1, 3, 4), muts(QLuma6BE, QLumaSwap, QLumaAbs),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 10, 3, 8)
				ext := func() int { return []int{-32, 31, r.IntN(64) - 32}[r.IntN(3)] }
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					tags := map[byte]bool{}
					for !a.done() {
						c := cLuma6(ext(), ext(), ext())
						tags[c[0]] = true
						a.emit(c...)
					}
					return len(tags) == 4
				})
			}),

		decC("d-rgb-alpha", "OP_RGB takes alpha from prev, in the interior and first column",
			chans(4), muts(QDRGB255, QDRGBPred),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 2, 8, 2, 6)
				return build(r, w, h, 4, 0, func(a *asm) bool {
					a.soup(soupW{RGB: 4, RGBA: 3, Diff: 2})
					return true
				})
			}),

		decC("d-stray-rgba", "OP_RGBA below 4 channels: the alpha byte is kept in state",
			chans(1, 2, 3), muts(QDNormalize),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 10, 1, 4)
				n := varN(v)
				return build(r, w, h, n, 0, func(a *asm) bool {
					a.soupN(soupClean, r.IntN(4), 3)
					a.emit(cRGBA(norm(r.Uint32(), n)&0xffffff | uint32(r.IntN(255))<<24)...)
					a.emit(cRGB(norm(r.Uint32(), n))...)
					a.probe()
					a.soup(soupClean)
					return true
				})
			}),

		decC("d-stray-rgb", "OP_RGB below 3 channels: unused bytes are kept in state",
			chans(1, 2), muts(QDNormalize),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 10, 1, 4)
				n := varN(v)
				return build(r, w, h, n, 0, func(a *asm) bool {
					a.soupN(soupClean, r.IntN(4), 2)
					a.emit(cRGB(strayValue(r, n))...)
					a.probe()
					a.soup(soupClean)
					return true
				})
			}),

		decC("d-stray-pred", "Stray bytes in prev flow through interior prediction",
			chans(1, 2, 3), muts(QDNormalize),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 5, 10, 1, 4)
				n := varN(v)
				return build(r, w, h, n, 0, func(a *asm) bool {
					if a.rem() < 3 {
						return false
					}
					a.emit(cRGBA(strayValue(r, n))...)
					a.emit(cDiff(r.IntN(4)-2, r.IntN(4)-2, r.IntN(4)-2)...)
					a.probe()
					a.soup(soupClean)
					return true
				})
			}),

		decC("d-stray-above", "Above is read from the image, without stray bytes",
			chans(1, 2, 3), muts(QDAboveRaw),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 2, 8, 2, 5)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					a.soup(soupW{Diff: 3, Luma5: 2, RGB: 3, RGBA: 2, Index: 1, Stray: true})
					return true
				})
			}),

		decC("d-stray-run", "A run reloads prev from the image, dropping stray bytes",
			chans(1, 2, 3), muts(QDNoRenorm),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 5, 12, 1, 4)
				n := varN(v)
				return build(r, w, h, n, 0, func(a *asm) bool {
					if a.rem() < 4 {
						return false
					}
					a.emit(cRGBA(strayValue(r, n))...)
					a.emit(cRun(1 + r.IntN(min(3, a.rem()-2)))...)
					a.emit(cRGB(norm(r.Uint32(), n))...)
					a.probe()
					a.soup(soupClean)
					return true
				})
			}),

		decC("d-index-unset", "OP_INDEX of a never-written slot yields zero and overwrites slot 0",
			chans(1, 3, 4), muts(QDIndexAtK),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 10, 1, 4)
				n := varN(v)
				return build(r, w, h, n, 0, func(a *asm) bool {
					var x uint32
					found := false
					for i := 0; i < 2000; i++ {
						x = norm(r.Uint32(), n) | 0xff000000
						if Hash(x) == 0 && x != a.prev() {
							found = true
							break
						}
					}
					if !found || a.rem() < 3 {
						return false
					}
					a.emit(cRGB(x)...)
					var k int
					for k = 1 + r.IntN(31); a.d.A[k] != 0; k = 1 + r.IntN(31) {
					}
					a.emit(cIndex(k)...)
					a.emit(cIndex(0)...)
					a.soup(soupClean)
					return true
				})
			}),

		decC("d-runs-not-indexed", "Run and copy pixels never enter the array",
			chans(1, 3, 4), muts(QRunInsert),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 12, 2, 6)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					a.soup(soupW{RGB: 2, Run: 2, Copy: 2, Index: 4, Diff: 1})
					return true
				})
			}),

		decC("d-prev-after-copy", "After OP_COPY, prev is the last copied pixel",
			chans(1, 3, 4), muts(QPrevAfterCopy),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 12, 2, 6)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					a.soup(soupW{RGB: 2, Copy: 3, Diff: 3, Run: 2, Luma5: 1})
					return true
				})
			}),

		decC("d-run-lengths", "OP_RUN 1 to 16, OP_LONGRUN 1 and 256",
			chans(1), muts(QRunBias, QLongBias),
			func(r *rand.Rand, v string) []byte {
				return build(r, 256+r.IntN(20), 2, 1, 0, func(a *asm) bool {
					a.emit(cLong(256)...)
					for _, k := range r.Perm(16) {
						if a.rem() < k+3 {
							break
						}
						a.emit(cRGB(r.Uint32())...)
						a.emit(cRun(k + 1)...)
					}
					a.emit(cRGB(r.Uint32())...)
					a.emit(cLong(1)...)
					a.soup(soupClean)
					return true
				})
			}),

		decC("d-copy-lengths", "OP_COPY of 1 and 256",
			chans(1, 3), muts(QLongBias, QPrevAfterCopy),
			func(r *rand.Rand, v string) []byte {
				n := varN(v)
				return build(r, 256+r.IntN(20), 2, n, 0, func(a *asm) bool {
					for a.y() == 0 {
						a.emit(a.chunk(soupClean)...)
					}
					a.emit(cCopy(256)...)
					for a.rem() > 1 && r.IntN(3) > 0 {
						a.emit(cRGB(norm(r.Uint32(), n))...)
						a.emit(cCopy(1)...)
					}
					a.soup(soupClean)
					return true
				})
			}),

		decC("d-run-row-end", "Runs and copies that end exactly at the row end",
			chans(1, 3, 4), muts(QDRunStrict),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 1, 10, 2, 6)
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					for !a.done() {
						if a.rem() <= 16 && r.IntN(2) == 0 {
							switch {
							case a.y() > 0 && r.IntN(2) == 0:
								a.emit(cCopy(a.rem())...)
							case r.IntN(2) == 0:
								a.emit(cLong(a.rem())...)
							default:
								a.emit(cRun(a.rem())...)
							}
							continue
						}
						a.emit(a.chunk(soupClean)...)
					}
					return true
				})
			}),

		decC("d-payload-tags", "Payload bytes that look like tags",
			chans(1, 3, 4), nil,
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 10, 2, 6)
				hi := func() uint32 { return uint32([]int{0xf8 + r.IntN(8), 0xe0 + r.IntN(16), 0xf0 + r.IntN(4)}[r.IntN(3)]) }
				return build(r, w, h, varN(v), 0, func(a *asm) bool {
					for !a.done() {
						switch r.IntN(4) {
						case 0:
							a.emit(cRGB(hi() | hi()<<8 | hi()<<16)...)
						case 1:
							a.emit(cRGBA(hi() | hi()<<8 | hi()<<16 | hi()<<24)...)
						case 2:
							z := byte(hi())
							a.emit(0x40+byte(r.IntN(128)), z)
						default:
							a.emit(0xf4+byte(r.IntN(4)), byte(hi()), byte(hi()))
						}
					}
					return true
				})
			}),

		decC("d-index-hash", "Canonical stream with many OP_INDEX chunks (hash includes implicit alpha)",
			chans(1, 3, 4), mutsN(func(n int) []Quirk {
				if n < 4 {
					return []Quirk{QHashA0, QHash6}
				}
				return []Quirk{QHash6}
			}),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 4, 12, 3, 8)
				p := newPainter(r, w, h, varN(v))
				out, _ := Encode(p.fill(func(x, y int) uint32 { return p.palv() }), 0)
				return out
			}),
	}
}

// ---- Invalid streams, each paired with a valid twin ----

// smallValid returns a random valid stream.
func smallValid(r *rand.Rand, n int) []byte {
	w, h := rdims(r, 1, 8, 1, 6)
	return build(r, w, h, n, 0, func(a *asm) bool {
		a.soup(soupClean)
		return true
	})
}

func setLE32(b []byte, off int, v uint32) {
	b[off], b[off+1], b[off+2], b[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// headerOnly returns a stream with the given header fields and body.
func headerOnly(w, h, n uint32, body ...byte) []byte {
	return append(header(w, h, n), body...)
}

func invalidChallenges() []*Challenge {
	return []*Challenge{
		invC("v-magic", "Bad magic bytes",
			chans(3), nil,
			func(r *rand.Rand, v string) []byte {
				s := smallValid(r, varN(v))
				if s == nil || isTwin(v) {
					return s
				}
				i := r.IntN(4)
				if r.IntN(2) == 0 {
					s[i] ^= 0x20 // case change
				} else {
					s[i] = byte(r.Uint32())
				}
				return s
			}),

		invC("v-zero-width", "Zero width",
			chans(1, 4), muts(QLaxZero),
			func(r *rand.Rand, v string) []byte {
				if isTwin(v) {
					return smallValid(r, varN(v))
				}
				return headerOnly(0, uint32(1+r.IntN(8)), uint32(varN(v)))
			}),

		invC("v-zero-height", "Zero height",
			chans(1, 4), muts(QLaxZero),
			func(r *rand.Rand, v string) []byte {
				if isTwin(v) {
					return smallValid(r, varN(v))
				}
				return headerOnly(uint32(1+r.IntN(8)), 0, uint32(varN(v)))
			}),

		invC("v-width-limit", "Width 1000001, one past the limit, with a complete body",
			chans(1), muts(QLaxWidth),
			func(r *rand.Rand, v string) []byte {
				w := uint32(maxWidth + 1)
				if isTwin(v) {
					w = uint32(257 + r.IntN(400))
				}
				s := header(w, 1, 1)
				s = append(s, cRGB(r.Uint32())...)
				for left := int(w) - 1; left > 0; {
					k := min(left, 256)
					s = append(s, cLong(k)...)
					left -= k
				}
				return s
			}),

		invC("v-width-signed", "Width of 2^31 or more",
			chans(1), muts(QLaxSigned),
			func(r *rand.Rand, v string) []byte {
				if isTwin(v) {
					return smallValid(r, 1)
				}
				return headerOnly(1<<31+uint32(r.IntN(1<<20)), uint32(1+r.IntN(4)), 1)
			}),

		invC("v-channels", "Channel count out of range",
			[]string{"c0", "c5", "c257", "c16777217"}, func(v string) []Quirk {
				if v == "c257" || v == "c16777217" {
					return []Quirk{QLaxLowByte}
				}
				return nil
			},
			func(r *rand.Rand, v string) []byte {
				fields := map[string]uint32{"c0": 0, "c5": 5, "c257": 257, "c16777217": 16777217}
				body := 1
				if v == "c0" || v == "c5" {
					body = 4
				}
				s := smallValid(r, body)
				if s != nil && !isTwin(v) {
					setLE32(s, 12, fields[v])
				}
				return s
			}),

		invC("v-short-header", "Stream shorter than the header",
			chans(3), nil,
			func(r *rand.Rand, v string) []byte {
				s := smallValid(r, 3)
				if s == nil || isTwin(v) {
					return s
				}
				return s[:r.IntN(16)]
			}),

		invC("v-bad-tag", "Tag 0xF8 to 0xFF",
			chans(1, 3, 4), muts(QLaxTag),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 2, 8, 1, 5)
				twin := isTwin(v)
				q := QLaxTag
				if twin {
					q = 0
				}
				return build(r, w, h, varN(v), q, func(a *asm) bool {
					a.soupN(soupClean, r.IntN(2*w*h), 1)
					tag := byte(0xf8 + r.IntN(8))
					if twin {
						tag = byte(0xf4 + r.IntN(4))
					}
					if !a.emit(tag, byte(r.Uint32()), byte(r.Uint32())) {
						return false
					}
					a.soup(soupClean)
					return true
				})
			}),

		truncC("v-trunc-luma", "Stream ends inside an OP_LUMA5 or OP_LUMA6", func(r *rand.Rand) []byte {
			if r.IntN(2) == 0 {
				return cLuma5(r.IntN(32)-16, r.IntN(32)-16, r.IntN(32)-16)
			}
			return cLuma6(r.IntN(64)-32, r.IntN(64)-32, r.IntN(64)-32)
		}),

		truncC("v-trunc-literal", "Stream ends inside an OP_RGB or OP_RGBA", func(r *rand.Rand) []byte {
			if r.IntN(2) == 0 {
				return cRGB(r.Uint32())
			}
			return cRGBA(r.Uint32())
		}),

		truncC("v-trunc-count", "Stream ends before an OP_LONGRUN or OP_COPY count", func(r *rand.Rand) []byte {
			if r.IntN(2) == 0 {
				return cLong(1)
			}
			return cCopy(1)
		}),

		invC("v-early-end", "Stream ends before the last pixel",
			chans(1, 3, 4), muts(QLaxEarly),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 2, 8, 1, 5)
				var cut int
				s := build(r, w, h, varN(v), 0, func(a *asm) bool {
					var ends []int
					for !a.done() {
						ends = append(ends, len(a.bytes()))
						a.emit(a.chunk(soupClean)...)
					}
					cut = ends[r.IntN(len(ends))]
					return true
				})
				if s == nil || isTwin(v) {
					return s
				}
				return s[:cut]
			}),

		invC("v-trailing", "Bytes after the last pixel",
			chans(1, 3, 4), muts(QLaxTrailing),
			func(r *rand.Rand, v string) []byte {
				s := smallValid(r, varN(v))
				if s == nil || isTwin(v) {
					return s
				}
				for i := 1 + r.IntN(3); i > 0; i-- {
					s = append(s, []byte{0xe0, 0x00, byte(r.Uint32())}[r.IntN(3)])
				}
				return s
			}),

		runPastC("v-run-past-row", "OP_RUN past the end of the row", 0xe0),
		runPastC("v-longrun-past-row", "OP_LONGRUN past the end of the row", 0xf2),
		runPastC("v-copy-past-row", "OP_COPY past the end of the row", 0xf3),

		invC("v-copy-row0", "OP_COPY in the first row",
			chans(1, 3, 4), muts(QLaxCopyRow0),
			func(r *rand.Rand, v string) []byte {
				w, h := rdims(r, 2, 10, 2, 5)
				twin := isTwin(v)
				return build(r, w, h, varN(v), QLaxCopyRow0, func(a *asm) bool {
					if twin {
						for a.y() == 0 {
							a.emit(a.chunk(soupClean)...)
						}
					} else {
						a.soupN(soupClean, r.IntN(w), 1)
					}
					if a.done() {
						return false
					}
					if !a.emit(cCopy(1 + r.IntN(a.rem()))...) {
						return false
					}
					a.soup(soupClean)
					return true
				})
			}),

		invC("v-size-overflow", "Width times height overflows 32 bits",
			chans(1, 4), muts(QLaxW32),
			func(r *rand.Rand, v string) []byte {
				if isTwin(v) {
					return smallValid(r, varN(v))
				}
				return headerOnly(65536, 65536, uint32(varN(v)))
			}),
	}
}

// truncC builds a stream whose final chunk is cut short.
func truncC(slug, name string, last func(r *rand.Rand) []byte) *Challenge {
	return invC(slug, name, chans(1, 3, 4), muts(QLaxTrunc),
		func(r *rand.Rand, v string) []byte {
			w, h := rdims(r, 2, 8, 2, 5)
			var cut int
			s := build(r, w, h, varN(v), 0, func(a *asm) bool {
				for a.d.Total-a.d.Pos > 1 {
					a.emit(a.chunk(soupW{Diff: 3, Luma5: 2, RGB: 2, Index: 1})...)
				}
				c := last(r)
				cut = len(a.bytes()) + 1 + r.IntN(len(c)-1)
				return a.emit(c...)
			})
			if s == nil || isTwin(v) {
				return s
			}
			return s[:cut]
		})
}

// runPastC builds a run that overruns its row but fits in the image.
func runPastC(slug, name string, tag byte) *Challenge {
	return invC(slug, name, chans(1, 3, 4), muts(QLaxRunWrap),
		func(r *rand.Rand, v string) []byte {
			w, h := rdims(r, 1, 10, 2, 5)
			twin := isTwin(v)
			return build(r, w, h, varN(v), QLaxRunWrap, func(a *asm) bool {
				a.soupN(soupClean, r.IntN(w*h), 2)
				if a.y() == h-1 || (tag == 0xf3 && a.y() == 0) {
					return false
				}
				k := a.rem()
				if !twin {
					k += 1 + r.IntN(int(a.d.Total-a.d.Pos)-a.rem())
				}
				var c []byte
				switch tag {
				case 0xe0:
					if k > 16 {
						return false
					}
					c = cRun(k)
				case 0xf2:
					c = cLong(min(k, 256))
				default:
					c = cCopy(min(k, 256))
				}
				if !a.emit(c...) {
					return false
				}
				a.soup(soupClean)
				return true
			})
		})
}
