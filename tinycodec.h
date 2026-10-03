// TCOM image codec: allocation-free, one packed pixel at a time
//
// Pixels are packed little-endian, channel j in bits 8*j..8*j+7 (i.e.
// RGBA is 0xAABBGGRR). With nchannels < 4 the encoder ignores the unused
// high bytes, and the decoder returns 0xff in byte 3 and zeros between.
//
// Both directions predict from the pixel above, so each needs a caller-
// provided row buffer of width uint32_t elements. Its contents need not
// be initialized.
//
// This is free and unencumbered software released into the public domain.
#ifndef TINYCODEC_H
#define TINYCODEC_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#define TINYHDRLEN    16        // bytes written by tinyencoder()
#define TINYENCMAX    7         // max bytes written by one tinyencode()
#define TINYMAXWIDTH  1000000

typedef struct {
    uint32_t *row;              // internal
    uint32_t  prev;             // internal
    uint32_t  mask;             // internal
    uint32_t  table[32];        // internal
    int       width, x, run;    // internal
    bool      up;               // internal
    uint8_t   runop;            // internal
} TinyEncoder;

typedef struct {
    uint32_t      *row;         // caller sets: width elements
    unsigned char *p, *end;     // internal
    uint32_t       prev;        // internal
    uint32_t       mask;        // internal
    uint32_t       table[32];   // internal
    int            x, y, run;   // internal
    uint8_t        runop;       // internal
    int            width;
    int            height;
    int            nchannels;   // 1-4
    bool           error;       // sticky
} TinyDecoder;

// Write the 16-byte (TINYHDRLEN) header to buf and initialize an encoder.
// Requires 1 <= width <= TINYMAXWIDTH, height >= 1, 1 <= nchannels <= 4,
// and row pointing to width elements. Call tinyencode() exactly
// width*height times; there is no finish step. The complete output never
// exceeds TINYHDRLEN + width*height*(nchannels==4 ? 5 : 4) bytes.
TinyEncoder tinyencoder(void *buf, int width, int height, int nchannels,
                        uint32_t *row);

// Encode the next pixel into buf, which must have room for TINYENCMAX
// bytes. Returns the number of bytes written (0..TINYENCMAX).
int tinyencode(TinyEncoder *, void *buf, uint32_t pixel);

// Validate the header and initialize a decoder over the complete stream.
// On an invalid header, the error flag is set and the dimensions are zero.
// Otherwise the caller must set the row field to a buffer of width
// elements before the first tinydecode().
TinyDecoder tinydecoder(void *buf, ptrdiff_t len);

// Decode the next pixel. Call exactly width*height times, then check the
// error flag, which also covers trailing input. Decoding may continue
// after an error, but the results are meaningless.
uint32_t tinydecode(TinyDecoder *);

#endif
