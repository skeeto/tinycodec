# The TCOM Image Format

A TCOM file is a 16-byte header followed by a stream of byte-aligned
chunks. There is no end marker: the stream ends exactly when the last
pixel has been decoded.

```
tcom_header {
    char     magic[4];   // magic bytes "TCOM"
    uint32_t width;      // image width in pixels (LE), 1..1000000
    uint32_t height;     // image height in pixels (LE), >= 1
    uint32_t channels;   // bytes per pixel (LE), 1..4
};
```

All multi-byte integers are little-endian. The image is `width * height`
pixels, row by row, left to right, top to bottom. Each pixel is `channels`
bytes with no padding. Channels are opaque bytes; the format assigns them
no color meaning.

## Pixel values

The encoder and decoder work with 4-byte pixel values. Channel bytes 0 to
3 are called **r, g, b, a** for convenience. A pixel with fewer than 4
channels has its missing color channels set to 0 and its **a** set to 255.
Only the first `channels` bytes of each value are stored in the image.

Each pixel value also has a 32-bit integer form,
`r | g<<8 | b<<16 | a<<24`, which is used for hashing and comparison.

## State

The encoder and decoder keep the same state:

- **prev**, the previous pixel value, initially `{r: 0, g: 0, b: 0, a: 255}`.
- **array[32]**, previously seen pixel values, initially all zeros,
  including **a**. A 4-channel pixel `{0, 0, 0, 0}` can therefore be coded
  as `OP_INDEX` 0 before it has ever appeared.
- **above**, the pixel in the same column of the previous row, read back
  from the image. In the first row, **above** is `{0, 0, 0, 255}`.

A pixel value is placed in **array** at

```
index = ((r | g<<8 | b<<16 | a<<24) * 0x9E3779B1 mod 2^32) >> 27
```

## Prediction

The difference chunks code a pixel relative to a predicted value **pred**:

```
first column:   pred = above
otherwise:      pred.r = (prev.r + above.r) / 2      (no wrap, rounded down)
                pred.g = (prev.g + above.g) / 2
                pred.b = (prev.b + above.b) / 2
                pred.a = prev.a
```

Outside the first column, **a** is not averaged; it is copied from **prev**.

## Chunks

The first byte of a chunk (the tag) selects the chunk type:

```
0x00..0x3F   OP_DIFF      1 byte
0x40..0xBF   OP_LUMA5     2 bytes
0xC0..0xDF   OP_INDEX     1 byte
0xE0..0xEF   OP_RUN       1 byte
0xF0         OP_RGB       4 bytes
0xF1         OP_RGBA      5 bytes
0xF2         OP_LONGRUN   2 bytes
0xF3         OP_COPY      2 bytes
0xF4..0xF7   OP_LUMA6     3 bytes
0xF8..0xFF   invalid
```

Chunks fall into two groups. A **pixel chunk** (`OP_DIFF`, `OP_LUMA5`,
`OP_LUMA6`, `OP_INDEX`, `OP_RGB`, `OP_RGBA`) produces one pixel value `v`.
It then sets `array[index(v)] = v` and `prev = v`. A **run chunk**
(`OP_RUN`, `OP_LONGRUN`, `OP_COPY`) produces `k` pixels. It sets **prev** to
the last of them as read back from the image, and leaves **array**
unchanged.

Applying a difference to a channel wraps modulo 256: `1 - 2` gives 255,
and `255 + 1` gives 0. Differences and run lengths are stored as unsigned
fields with a bias, given for each chunk below.

```
┌─ OP_DIFF ───────────────┐
│         Byte[0]         │
│  7  6  5  4  3  2  1  0 │
│──────┼─────┼─────┼──────│
│  0 0 │  db │  dg │  dr  │
└──────┴─────┴─────┴──────┘
```

The 2-bit differences `dr`, `dg`, `db` from **pred** are each in -2..1,
stored with a bias of 2. The result is `{pred.r + dr, pred.g + dg,
pred.b + db, pred.a}`.

```
┌─ OP_LUMA5 ──────────────┬─────────┐
│         Byte[0]         │ Byte[1] │
│─────────────────────────┼─────────│
│     0x40 + (v >> 8)     │ v & 255 │
└─────────────────────────┴─────────┘

  v:  14         10   9          5   4          0
     ┌──────────────┬──────────────┬─────────────┐
     │   db - dg    │   dr - dg    │     dg      │
     └──────────────┴──────────────┴─────────────┘
```

`v = (Byte[0] - 0x40) << 8 | Byte[1]` is a 15-bit value with three 5-bit
fields, each stored with a bias of 16:

- `dg`, the difference of **g** from **pred**, in -16..15;
- `dr - dg` and `db - dg`, in -16..15.

The result is `{pred.r + dr, pred.g + dg, pred.b + db, pred.a}`.

```
┌─ OP_LUMA6 ──────────────┬─────────┬─────────┐
│         Byte[0]         │ Byte[1] │ Byte[2] │
│─────────────────────────┼─────────┼─────────│
│    0xF4 + (v >> 16)     │ v & 255 │ v>>8&255│
└─────────────────────────┴─────────┴─────────┘

  v:  17         12  11          6   5          0
     ┌──────────────┬──────────────┬─────────────┐
     │   db - dg    │   dr - dg    │     dg      │
     └──────────────┴──────────────┴─────────────┘
```

`OP_LUMA6` is `OP_LUMA5` widened to an 18-bit value with three 6-bit
fields. Each is stored with a bias of 32, so each difference is in -32..31.
The low 16 bits of `v` are stored little-endian in Byte[1] and Byte[2].

```
┌─ OP_INDEX ──────────────┐
│         Byte[0]         │
│  7  6  5  4  3  2  1  0 │
│─────────┼───────────────│
│  1 1 0  │     index     │
└─────────┴───────────────┘
```

The result is `array[index]`, with `index` in 0..31. The value need not
hash to `index`. Like any pixel chunk's result, it is then stored at its
own hash position.

```
┌─ OP_RGB ────────────────┬─────────┬─────────┬─────────┐
│         Byte[0]         │ Byte[1] │ Byte[2] │ Byte[3] │
│  1  1  1  1  0  0  0  0 │    r    │    g    │    b    │
└─────────────────────────┴─────────┴─────────┴─────────┘
```

**a** is taken from **prev**.

```
┌─ OP_RGBA ───────────────┬─────────┬─────────┬─────────┬─────────┐
│         Byte[0]         │ Byte[1] │ Byte[2] │ Byte[3] │ Byte[4] │
│  1  1  1  1  0  0  0  1 │    r    │    g    │    b    │    a    │
└─────────────────────────┴─────────┴─────────┴─────────┴─────────┘
```

```
┌─ OP_RUN ────────────────┐
│         Byte[0]         │
│  7  6  5  4  3  2  1  0 │
│────────────┼────────────│
│  1 1 1 0   │    run     │
└────────────┴────────────┘
```

Repeats **prev** `k` times. `k` is in 1..16, stored with a bias of -1.

```
┌─ OP_LONGRUN ────────────┬─────────┐
│         Byte[0]         │ Byte[1] │
│  1  1  1  1  0  0  1  0 │   run   │
└─────────────────────────┴─────────┘
```

Repeats **prev** `k` times. `k` is in 1..256, stored with a bias of -1.

```
┌─ OP_COPY ───────────────┬─────────┐
│         Byte[0]         │ Byte[1] │
│  1  1  1  1  0  0  1  1 │   run   │
└─────────────────────────┴─────────┘
```

Copies the next `k` pixels from the row above: each pixel equals its own
**above**. `k` is in 1..256, stored with a bias of -1.

## Validity

A decoder must reject a stream that:

- has a bad magic or a header field out of range;
- uses a tag in 0xF8..0xFF;
- ends partway through a chunk, or before the last pixel;
- contains bytes after the chunk that completes the last pixel;
- has a run chunk that extends past the end of the current row;
- has `OP_COPY` in the first row.

Values read back from the image (**above**, and **prev** after a run
chunk) have only `channels` stored bytes. Their missing channels are
restored as described under Pixel values. A pixel chunk may produce bytes
beyond `channels`, for example `OP_RGB` in a 1-channel image. Those bytes
are not stored in the image, but they stay in **prev** and **array** like
any other value.

## Encoding

Any sequence of valid chunks that reproduces the image is a valid
encoding. The canonical encoding codes each pixel `v` at column `x` as
follows, taking the first rule that applies:

1. **Run.** If `v = prev`, or `v = above` and this is not the first row,
   start a run. If `v = prev`, it is a run of **prev**, which continues
   while each pixel equals **prev**. Otherwise it is a copy, which
   continues while each pixel equals its own **above**. Extend
   the run to at most 256 pixels and never past the end of the row. Emit
   `OP_RUN` for a run of **prev** of at most 16 pixels, otherwise
   `OP_LONGRUN` or `OP_COPY`.
2. **Index.** If `array[index(v)] = v`, emit `OP_INDEX`.
3. If `v.a ≠ pred.a`, go to rule 7. (Rule 7 compares against **prev**,
   not **pred**, so this can still yield `OP_RGB`.)
4. **Diff.** Let `dr`, `dg`, `db` be the differences of `v` from **pred**,
   each taken modulo 256 and read as signed in -128..127. If all three are
   in -2..1, emit `OP_DIFF`. Below, `dr - dg` and `db - dg` are ordinary
   integer subtractions of these signed values. (Wrapping them would make
   no difference within the ranges tested.)
5. If `dg`, `dr - dg` and `db - dg` are all in -16..15, emit `OP_LUMA5`.
6. If they are all in -32..31, emit `OP_LUMA6`.
7. **Literal.** Emit `OP_RGB` if `v.a = prev.a`, otherwise `OP_RGBA`.

An image with fewer than 4 channels never needs `OP_RGBA`. The encoded size
never exceeds `16 + width * height * (channels = 4 ? 5 : 4)` bytes.

## Test vectors

Canonical encodings, in hexadecimal. Raw pixels are listed row by row.

```
1x1, 1 channel:   00
  54434f4d 01000000 01000000 01000000  e0

2x1, 3 channels:  000000 ffffff
  54434f4d 02000000 01000000 03000000  e0 15

1x1, 4 channels:  00000000
  54434f4d 01000000 01000000 04000000  c0

3x2, 1 channel:   000000 000000
  54434f4d 03000000 02000000 01000000  e2 e2

4x4, 4 channels:
  0a141eff 0a141eff 0b151fff 0a141eff
  0a141eff 0a141eff 0b151fff 0a141eff
  c8c8c8ff c8c8c800 c8c8c800 c8c8c800
  c8c8c800 c9c7c601 05060708 090a0b0c
  54434f4d 04000000 04000000 04000000
  f6b4a5 e0 957b c2 e1 f301 f0c8c8c8 f1c8c8c800 e1 e0
  f1c9c7c601 f105060708 f1090a0b0c
```
