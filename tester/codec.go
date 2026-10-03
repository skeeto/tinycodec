package main

// Reference TCOM codec. Quirks inject plausible implementation bugs
// ("mutants") so challenges can prove each instance detects them. With
// Quirk(0) the encoder and decoder follow the specification exactly.

import "fmt"

type Quirk uint64

const (
	// Shared by encoder and decoder
	QInitPrev      Quirk = 1 << iota // prev starts {0,0,0,0}
	QArrayInit                       // array starts filled with {0,0,0,255}
	QRow0Above                       // first-row above is {0,0,0,0}
	QRow0Prev                        // first-row predictor is prev
	QCol0Avg                         // first column averages like the rest
	QCol0A                           // first column takes alpha from prev
	QPredRound                       // average rounds up
	QPredAvgA                        // alpha is averaged too
	QHashA0                          // hash ignores implicit alpha in channels<4
	QHash6                           // 64-entry table indexed by top 6 bits
	QDiffSwap                        // OP_DIFF fields in reverse order
	QLumaSwap                        // luma dr-dg and db-dg fields swapped
	QLumaAbs                         // luma stores dr and db, not relative to dg
	QLuma6BE                         // OP_LUMA6 payload big-endian
	QRunBias                         // OP_RUN stores k, not k-1
	QLongBias                        // OP_LONGRUN/OP_COPY store k, not k-1
	QPrevAfterCopy                   // prev not updated by OP_COPY
	QRunInsert                       // run pixels enter the array

	// Encoder only
	QGatePrev      // alpha gate compares against prev, not pred
	QRGBvsPred     // RGB/RGBA choice compares against pred, not prev
	QRGBOpaque     // RGB chosen iff alpha is 255
	QNoWrap        // differences are not wrapped modulo 256
	QRangeNarrowLo // lowest value of each range excluded
	QRangeNarrowHi // highest value of each range excluded
	QRangeWide     // ranges extend one past the top
	QIndexLate     // index tried after DIFF/LUMA
	QCopyFirst     // copy takes precedence over a run of prev
	QKindSwitch    // a run continues while equal to prev or above
	QCopyMin2      // OP_COPY only for k >= 2
	QCopyRow0      // copy allowed in the first row
	QCap255        // runs capped at 255
	QRun15         // OP_RUN only for k <= 15

	// Decoder only
	QDNormalize  // pixel chunk results normalized before entering state
	QDNoRenorm   // prev after a run of prev keeps stray bytes
	QDAboveRaw   // above keeps stray bytes
	QDIndexAtK   // OP_INDEX result stored at slot k, not its hash
	QDRGB255     // OP_RGB alpha is 255
	QDRGBPred    // OP_RGB alpha comes from pred
	QDRunStrict  // runs must end before the row end
	QLaxTrailing // trailing bytes accepted
	QLaxRunWrap  // runs may continue into the next row
	QLaxCopyRow0 // OP_COPY accepted in the first row
	QLaxTrunc    // missing payload bytes read as zero
	QLaxEarly    // stream may end early; remaining pixels zero
	QLaxTag      // tags 0xF8..0xFF decoded as OP_LUMA6
	QLaxWidth    // no width limit
	QLaxZero     // zero width or height accepted
	QLaxSigned   // width read as signed 32-bit
	QLaxLowByte  // only the low byte of channels is read
	QLaxW32      // pixel count computed in 32 bits

	qEnd
)

var quirkNames = []string{
	"InitPrev", "ArrayInit", "Row0Above", "Row0Prev", "Col0Avg", "Col0A", "PredRound",
	"PredAvgA", "HashA0", "Hash6", "DiffSwap", "LumaSwap", "LumaAbs",
	"Luma6BE", "RunBias", "LongBias", "PrevAfterCopy", "RunInsert",
	"GatePrev", "RGBvsPred", "RGBOpaque", "NoWrap", "RangeNarrowLo",
	"RangeNarrowHi", "RangeWide", "IndexLate", "CopyFirst", "KindSwitch",
	"CopyMin2", "CopyRow0", "Cap255", "Run15",
	"DNormalize", "DNoRenorm", "DAboveRaw", "DIndexAtK", "DRGB255",
	"DRGBPred", "DRunStrict", "LaxTrailing", "LaxRunWrap", "LaxCopyRow0",
	"LaxTrunc", "LaxEarly", "LaxTag", "LaxWidth", "LaxZero", "LaxSigned",
	"LaxLowByte", "LaxW32",
}

func (q Quirk) String() string {
	s := ""
	for i, name := range quirkNames {
		if q&(1<<i) != 0 {
			if s != "" {
				s += "|"
			}
			s += name
		}
	}
	if s == "" {
		return "none"
	}
	return s
}

const (
	maxWidth = 1000000
	initPix  = 0xff000000
)

type Op uint8

const (
	OpDiff Op = iota
	OpLuma5
	OpLuma6
	OpIndex
	OpRun
	OpRGB
	OpRGBA
	OpLongRun
	OpCopy
)

var opNames = [...]string{
	"OP_DIFF", "OP_LUMA5", "OP_LUMA6", "OP_INDEX", "OP_RUN", "OP_RGB",
	"OP_RGBA", "OP_LONGRUN", "OP_COPY",
}

func (o Op) String() string { return opNames[o] }

// Chunk is one entry of an encoder trace.
type Chunk struct {
	Op                   Op
	X, Y, K              int
	V, Prev, Pred, Above uint32
	D                    [3]int // dr, dg, db from pred (signed, wrapped)
	Bytes                []byte
}

type Image struct {
	W, H, N int
	Pix     []byte
}

func NewImage(w, h, n int) *Image {
	return &Image{W: w, H: h, N: n, Pix: make([]byte, w*h*n)}
}

func mask(n int) uint32 {
	if n >= 4 {
		return 0xffffffff
	}
	return 1<<(8*n) - 1
}

// norm converts a value to the canonical form for n channels.
func norm(v uint32, n int) uint32 {
	m := mask(n)
	return v&m | ^m&0xff000000
}

func (m *Image) At(x, y int) uint32 {
	i := (y*m.W + x) * m.N
	v := uint32(0)
	for j := 0; j < m.N; j++ {
		v |= uint32(m.Pix[i+j]) << (8 * j)
	}
	return norm(v, m.N)
}

func (m *Image) Set(x, y int, v uint32) {
	i := (y*m.W + x) * m.N
	for j := 0; j < m.N; j++ {
		m.Pix[i+j] = byte(v >> (8 * j))
	}
}

func Hash(v uint32) int { return int(v * 0x9e3779b1 >> 27) }

func hashq(v uint32, n int, q Quirk) int {
	if q&QHashA0 != 0 && n < 4 {
		v &^= 0xff000000
	}
	if q&QHash6 != 0 {
		return int(v * 0x9e3779b1 >> 26)
	}
	return int(v * 0x9e3779b1 >> 27)
}

func predq(p, u uint32, x int, q Quirk) uint32 {
	if x == 0 && q&QCol0Avg == 0 {
		if q&QCol0A != 0 {
			return u&0xffffff | p&0xff000000
		}
		return u
	}
	r := uint32(0)
	for j := 0; j < 3; j++ {
		s := p>>(8*j)&255 + u>>(8*j)&255
		if q&QPredRound != 0 {
			s++
		}
		r |= s / 2 << (8 * j)
	}
	if q&QPredAvgA != 0 {
		r |= (p>>24 + u>>24) / 2 << 24
	} else {
		r |= p & 0xff000000
	}
	return r
}

func Pred(p, u uint32, x int) uint32 { return predq(p, u, x, 0) }

// addD applies per-channel deltas to b, keeping its byte 3.
func addD(b uint32, dr, dg, db int) uint32 {
	return (b+uint32(dr))&0xff |
		((b>>8)+uint32(dg))&0xff<<8 |
		((b>>16)+uint32(db))&0xff<<16 |
		b&0xff000000
}

func s8(x uint32) int { return int(int8(byte(x))) }

func header(w, h, n uint32) []byte {
	b := []byte("TCOM")
	for _, v := range []uint32{w, h, n} {
		b = append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	return b
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

type ranges struct{ lo, hi int }

func rangeq(lo, hi int, q Quirk) ranges {
	if q&QRangeNarrowLo != 0 {
		lo++
	}
	if q&QRangeNarrowHi != 0 {
		hi--
	}
	if q&QRangeWide != 0 {
		hi++
	}
	return ranges{lo, hi}
}

func (r ranges) in(vs ...int) bool {
	for _, v := range vs {
		if v < r.lo || v > r.hi {
			return false
		}
	}
	return true
}

// Encode produces the canonical encoding and its chunk trace.
func Encode(m *Image, q Quirk) ([]byte, []Chunk) {
	w, h, n := m.W, m.H, m.N
	out := header(uint32(w), uint32(h), uint32(n))
	var tr []Chunk
	p := uint32(initPix)
	if q&QInitPrev != 0 {
		p = 0
	}
	var a [64]uint32
	if q&QArrayInit != 0 {
		for i := range a {
			a[i] = initPix
		}
	}
	hash := func(v uint32) int { return hashq(v, n, q) }
	above := func(x, y int) uint32 {
		if y > 0 {
			return m.At(x, y-1)
		}
		if q&QRow0Above != 0 {
			return 0
		}
		return initPix
	}
	upOK := func(y int) bool { return y > 0 || q&QCopyRow0 != 0 }
	runMax := 256
	if q&(QCap255|QLongBias) != 0 {
		runMax = 255
	}
	shortMax := 16
	if q&(QRun15|QRunBias) != 0 {
		shortMax = 15
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; {
			v, u := m.At(x, y), above(x, y)
			b := predq(p, u, x, q)
			if y == 0 && q&QRow0Prev != 0 {
				b = p
			}
			c := Chunk{X: x, Y: y, K: 1, V: v, Prev: p, Pred: b, Above: u}
			start := len(out)

			isPrev := v == p
			isUp := upOK(y) && v == u
			if isPrev || isUp {
				isCopy := !isPrev || (q&QCopyFirst != 0 && isUp)
				k := 1
				for k < runMax && x+k < w {
					nv, nu := m.At(x+k, y), above(x+k, y)
					var cont bool
					switch {
					case q&QKindSwitch != 0:
						cont = nv == p || (upOK(y) && nv == nu)
					case isCopy:
						cont = nv == nu
					default:
						cont = nv == p
					}
					if !cont {
						break
					}
					k++
				}
				if !(isCopy && k == 1 && q&QCopyMin2 != 0) {
					count := k - 1
					if q&QLongBias != 0 {
						count = k
					}
					switch {
					case !isCopy && k <= shortMax:
						c.Op = OpRun
						if q&QRunBias != 0 {
							out = append(out, byte(0xe0+k))
						} else {
							out = append(out, byte(0xdf+k))
						}
					case !isCopy:
						c.Op = OpLongRun
						out = append(out, 0xf2, byte(count))
					default:
						c.Op = OpCopy
						out = append(out, 0xf3, byte(count))
					}
					if q&QRunInsert != 0 {
						for j := 0; j < k; j++ {
							rv := m.At(x+j, y)
							a[hash(rv)] = rv
						}
					}
					if !(isCopy && q&QPrevAfterCopy != 0) {
						p = m.At(x+k-1, y)
					}
					c.K = k
					c.Bytes = out[start:]
					tr = append(tr, c)
					x += k
					continue
				}
			}

			j := hash(v)
			if q&QIndexLate == 0 && a[j] == v {
				c.Op = OpIndex
				out = append(out, byte(0xc0+j))
				goto done
			}
			{
				var dr, dg, db int
				if q&QNoWrap != 0 {
					dr = int(v&255) - int(b&255)
					dg = int(v>>8&255) - int(b>>8&255)
					db = int(v>>16&255) - int(b>>16&255)
				} else {
					dr, dg, db = s8(v-b), s8(v>>8-b>>8), s8(v>>16-b>>16)
				}
				c.D = [3]int{dr, dg, db}
				gate := b >> 24
				if q&QGatePrev != 0 {
					gate = p >> 24
				}
				if v>>24 == gate {
					f1, f2 := dr-dg, db-dg
					if q&QLumaAbs != 0 {
						f1, f2 = dr, db
					}
					if q&QLumaSwap != 0 {
						f1, f2 = f2, f1
					}
					switch {
					case rangeq(-2, 1, q).in(dr, dg, db):
						c.Op = OpDiff
						if q&QDiffSwap != 0 {
							out = append(out, byte((db+2)|(dg+2)<<2|(dr+2)<<4))
						} else {
							out = append(out, byte((dr+2)|(dg+2)<<2|(db+2)<<4))
						}
						goto done
					case rangeq(-16, 15, q).in(dg, f1, f2):
						c.Op = OpLuma5
						z := (dg + 16) | (f1+16)<<5 | (f2+16)<<10
						out = append(out, byte(0x40+(z>>8)), byte(z))
						goto done
					case rangeq(-32, 31, q).in(dg, f1, f2):
						c.Op = OpLuma6
						z := (dg + 32) | (f1+32)<<6 | (f2+32)<<12
						if q&QLuma6BE != 0 {
							out = append(out, byte(0xf4+(z>>16)), byte(z>>8), byte(z))
						} else {
							out = append(out, byte(0xf4+(z>>16)), byte(z), byte(z>>8))
						}
						goto done
					}
				}
				if q&QIndexLate != 0 && a[j] == v {
					c.Op = OpIndex
					out = append(out, byte(0xc0+j))
					goto done
				}
				ref := p >> 24
				if q&QRGBvsPred != 0 {
					ref = b >> 24
				}
				rgb := v>>24 == ref
				if q&QRGBOpaque != 0 {
					rgb = v>>24 == 255
				}
				if rgb {
					c.Op = OpRGB
					out = append(out, 0xf0, byte(v), byte(v>>8), byte(v>>16))
				} else {
					c.Op = OpRGBA
					out = append(out, 0xf1, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
				}
			}
		done:
			a[j] = v
			p = v
			c.Bytes = out[start:]
			tr = append(tr, c)
			x++
		}
	}
	return out, tr
}

// Decoder decodes one chunk per Step, so a stream can be assembled and
// checked incrementally.
type Decoder struct {
	S          []byte
	I          int
	W          int64 // may be <= 0 under lax quirks
	H          int64
	N          int
	Total, Pos int64
	P          uint32
	A          [64]uint32
	Img        []byte   // canonical pixels, N bytes each
	Vals       []uint32 // values as a naive decoder would store them
	Q          Quirk
}

func NewDecoder(s []byte, q Quirk) (*Decoder, bool) {
	if len(s) < 16 || string(s[:4]) != "TCOM" {
		return nil, false
	}
	w, h, n := le32(s[4:]), le32(s[8:]), le32(s[12:])
	if q&QLaxLowByte != 0 {
		n &= 255
	}
	if n < 1 || n > 4 {
		return nil, false
	}
	if q&QLaxZero == 0 && (w == 0 || h == 0) {
		return nil, false
	}
	W := int64(w)
	if q&QLaxSigned != 0 {
		W = int64(int32(w))
	}
	if q&QLaxWidth == 0 && W > maxWidth {
		return nil, false
	}
	total := W * int64(h)
	if total < 0 {
		total = 0
	}
	if q&QLaxW32 != 0 {
		total = int64(w * h)
	}
	d := &Decoder{S: s, I: 16, W: W, H: int64(h), N: int(n), Total: total, Q: q}
	d.P = initPix
	if q&QInitPrev != 0 {
		d.P = 0
	}
	if q&QArrayInit != 0 {
		for i := range d.A {
			d.A[i] = initPix
		}
	}
	return d, true
}

func (d *Decoder) Done() bool { return d.Pos >= d.Total }

func (d *Decoder) X() int { return int(d.Pos % d.W) }
func (d *Decoder) Y() int { return int(d.Pos / d.W) }

func (d *Decoder) pixel(pos int64) uint32 {
	v := uint32(0)
	for j := 0; j < d.N; j++ {
		v |= uint32(d.Img[int(pos)*d.N+j]) << (8 * j)
	}
	return norm(v, d.N)
}

func (d *Decoder) above(pos int64) uint32 {
	if pos >= d.W {
		if d.Q&QDAboveRaw != 0 {
			return d.Vals[pos-d.W]
		}
		return d.pixel(pos - d.W)
	}
	if d.Q&QRow0Above != 0 {
		return 0
	}
	return initPix
}

func (d *Decoder) emit(v uint32) {
	nv := norm(v, d.N)
	for j := 0; j < d.N; j++ {
		d.Img = append(d.Img, byte(nv>>(8*j)))
	}
	d.Vals = append(d.Vals, v)
	d.Pos++
}

func (d *Decoder) predict(x int, u uint32) uint32 {
	if d.Pos < d.W && d.Q&QRow0Prev != 0 {
		return d.P
	}
	return predq(d.P, u, x, d.Q)
}

// Step decodes one chunk. It returns false if the stream is invalid.
func (d *Decoder) Step() bool {
	if d.I >= len(d.S) {
		return false
	}
	q := d.Q
	trunc := false
	get := func() uint32 {
		if d.I >= len(d.S) {
			trunc = true
			d.I++
			return 0
		}
		d.I++
		return uint32(d.S[d.I-1])
	}
	x := int64(d.X())
	u := d.above(d.Pos)
	t := get()
	hash := func(v uint32) int { return hashq(v, d.N, q) }

	if (t >= 0xe0 && t <= 0xef) || t == 0xf2 || t == 0xf3 {
		k := int64(t) - 0xdf
		if q&QRunBias != 0 {
			k--
		}
		if t >= 0xf0 {
			k = int64(get()) + 1
			if q&QLongBias != 0 {
				k--
			}
		}
		if trunc && q&QLaxTrunc == 0 {
			return false
		}
		limit := d.W - x
		if q&QLaxRunWrap != 0 {
			limit = d.Total - d.Pos
		}
		if q&QDRunStrict != 0 {
			limit--
		}
		if k > limit || (t == 0xf3 && d.Pos < d.W && q&QLaxCopyRow0 == 0) {
			return false
		}
		if k > d.Total-d.Pos {
			return false // only reachable under quirks
		}
		var last uint32
		for j := int64(0); j < k; j++ {
			if t == 0xf3 {
				last = d.above(d.Pos)
			} else {
				last = d.P
			}
			if q&QRunInsert != 0 {
				d.A[hash(last)] = last
			}
			d.emit(last)
		}
		if k > 0 && !(t == 0xf3 && q&QPrevAfterCopy != 0) {
			if q&QDNoRenorm != 0 {
				d.P = last
			} else {
				d.P = norm(last, d.N)
			}
		}
		return true
	}

	var v uint32
	isIndex := false
	switch {
	case t == 0xf0:
		r, g, b := get(), get(), get()
		alpha := d.P & 0xff000000
		if q&QDRGB255 != 0 {
			alpha = 0xff000000
		}
		if q&QDRGBPred != 0 {
			alpha = d.predict(int(x), u) & 0xff000000
		}
		v = r | g<<8 | b<<16 | alpha
	case t == 0xf1:
		r, g, b, a := get(), get(), get(), get()
		v = r | g<<8 | b<<16 | a<<24
	case t >= 0xc0 && t <= 0xdf:
		v = d.A[t-0xc0]
		isIndex = true
	case t >= 0xf8 && q&QLaxTag == 0:
		return false
	default:
		b := d.predict(int(x), u)
		var dr, dg, db int
		if t < 0x40 {
			dr, dg, db = int(t&3)-2, int(t>>2&3)-2, int(t>>4&3)-2
			if q&QDiffSwap != 0 {
				dr, db = db, dr
			}
		} else {
			var z, bits, bias int
			if t < 0xc0 {
				z = int(t-0x40)<<8 | int(get())
				bits, bias = 5, 16
			} else {
				c0, c1 := int(get()), int(get())
				if q&QLuma6BE != 0 {
					c0, c1 = c1, c0
				}
				z = int(t-0xf4)<<16 | c0 | c1<<8
				bits, bias = 6, 32
			}
			m := 1<<bits - 1
			dg = z&m - bias
			f1, f2 := z>>bits&m-bias, z>>(2*bits)&m-bias
			if q&QLumaSwap != 0 {
				f1, f2 = f2, f1
			}
			if q&QLumaAbs != 0 {
				dr, db = f1, f2
			} else {
				dr, db = dg+f1, dg+f2
			}
		}
		v = addD(b, dr, dg, db)
	}
	if trunc && q&QLaxTrunc == 0 {
		return false
	}
	if q&QDNormalize != 0 {
		v = norm(v, d.N)
	}
	slot := hash(v)
	if isIndex && q&QDIndexAtK != 0 {
		slot = int(t - 0xc0)
	}
	d.A[slot] = v
	d.P = v
	d.emit(v)
	return true
}

func (d *Decoder) Image() *Image {
	return &Image{W: int(d.W), H: int(d.H), N: d.N, Pix: d.Img}
}

// Decode decodes a complete stream, returning false if it is invalid.
func Decode(s []byte, q Quirk) (*Image, bool) {
	d, ok := NewDecoder(s, q)
	if !ok {
		return nil, false
	}
	for !d.Done() {
		if d.I >= len(d.S) && q&QLaxEarly != 0 {
			for !d.Done() {
				d.emit(0)
			}
			break
		}
		if !d.Step() {
			return nil, false
		}
	}
	if d.I > len(d.S) {
		d.I = len(d.S) // lax truncation
	}
	if d.I != len(d.S) && q&QLaxTrailing == 0 {
		return nil, false
	}
	return d.Image(), true
}

// Safe wrappers: a mutant may misbehave arbitrarily, including panicking.
func safeEncode(m *Image, q Quirk) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	out, _ = Encode(m, q)
	return out, nil
}

func safeDecode(s []byte, q Quirk) (m *Image, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	m, ok = Decode(s, q)
	return m, ok, nil
}
