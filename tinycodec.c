// TCOM image codec: allocation-free, one packed pixel at a time
// This is free and unencumbered software released into the public domain.
#include "tinycodec.h"

#define TINYINIT 0xff000000u

static uint32_t tinyhash(uint32_t v)
{
    return (v * 0x9e3779b1u) >> 27;
}

// Byte-wise floor average of l and u in bytes 0-2, byte 3 from l.
static uint32_t tinypred(uint32_t l, uint32_t u)
{
    return ((l & u & 0xffffff) + (((l ^ u) & 0xfefefe) >> 1)) |
           (l & 0xff000000);
}

static uint32_t tinyadd(uint32_t b, int dr, int dg, int db)
{
    return (((b>> 0) + (uint32_t)dr) & 0xff)       |
           (((b>> 8) + (uint32_t)dg) & 0xff) <<  8 |
           (((b>>16) + (uint32_t)db) & 0xff) << 16 |
           (b & 0xff000000);
}

// Canonical pixel value for the channel count: unused bytes zeroed, and
// byte 3 forced to 0xff when there is no fourth channel.
static uint32_t tinynorm(uint32_t v, uint32_t mask)
{
    return (v & mask) | (~mask & 0xff000000);
}

static int tinys8(uint32_t x)
{
    return (int)((x & 0xff) ^ 0x80) - 0x80;
}

static uint32_t tinyload32(unsigned char *s)
{
    return (uint32_t)s[0] | (uint32_t)s[1]<<8 |
           (uint32_t)s[2]<<16 | (uint32_t)s[3]<<24;
}

static void tinystore32(unsigned char *s, uint32_t v)
{
    s[0] = (unsigned char)(v >>  0);
    s[1] = (unsigned char)(v >>  8);
    s[2] = (unsigned char)(v >> 16);
    s[3] = (unsigned char)(v >> 24);
}

TinyEncoder tinyencoder(void *buf, int width, int height, int nchannels,
                        uint32_t *row)
{
    unsigned char *s = buf;
    s[0] = 'T'; s[1] = 'C'; s[2] = 'O'; s[3] = 'M';
    tinystore32(s+ 4, (uint32_t)width);
    tinystore32(s+ 8, (uint32_t)height);
    tinystore32(s+12, (uint32_t)nchannels);

    TinyEncoder e = {0};
    e.row   = row;
    e.prev  = TINYINIT;
    e.mask  = nchannels<4 ? (1u << 8*nchannels) - 1 : 0xffffffff;
    e.width = width;
    return e;
}

static unsigned char *tinyflush(TinyEncoder *e, unsigned char *s)
{
    if (e->runop==0xf2 && e->run<=16) {
        *s++ = (unsigned char)(0xdf + e->run);
    } else {
        *s++ = e->runop;
        *s++ = (unsigned char)(e->run - 1);
    }
    e->run = 0;
    return s;
}

int tinyencode(TinyEncoder *e, void *buf, uint32_t pixel)
{
    unsigned char *s = buf;
    uint32_t v = tinynorm(pixel, e->mask);
    uint32_t u = e->up ? e->row[e->x] : TINYINIT;

    // A pending run either absorbs this pixel or is flushed. Run kind is
    // chosen at its first pixel, RUN_PREV taking precedence.
    if (e->run) {
        if (e->runop==0xf2 ? v==e->prev : v==u) {
            e->run++;
            goto advance;
        }
        s = tinyflush(e, s);
    }
    if (v==e->prev || (e->up && v==u)) {
        e->runop = v==e->prev ? 0xf2 : 0xf3;
        e->run   = 1;
        goto advance;
    }

    int h = (int)tinyhash(v);
    if (e->table[h] == v) {
        *s++ = (unsigned char)(0xc0 + h);
        goto advance;
    }
    e->table[h] = v;

    uint32_t b = e->x ? tinypred(e->prev, u) : u;
    int dr = tinys8((v>> 0) - (b>> 0));
    int dg = tinys8((v>> 8) - (b>> 8));
    int db = tinys8((v>>16) - (b>>16));
    if ((v ^ b) > 0xffffff) {
        // byte 3 differs from the predictor: literal only
    } else if (dr+2u<4 && dg+2u<4 && db+2u<4) {
        *s++ = (unsigned char)((dr+2) | (dg+2)<<2 | (db+2)<<4);
        goto advance;
    } else if (dg+16u<32 && dr-dg+16u<32 && db-dg+16u<32) {
        int z = (dg+16) | (dr-dg+16)<<5 | (db-dg+16)<<10;
        *s++ = (unsigned char)(0x40 + (z>>8));
        *s++ = (unsigned char)z;
        goto advance;
    } else if (dg+32u<64 && dr-dg+32u<64 && db-dg+32u<64) {
        int z = (dg+32) | (dr-dg+32)<<6 | (db-dg+32)<<12;
        *s++ = (unsigned char)(0xf4 + (z>>16));
        *s++ = (unsigned char)z;
        *s++ = (unsigned char)(z>>8);
        goto advance;
    }

    if ((v ^ e->prev) <= 0xffffff) {
        *s++ = 0xf0;  // RGB, byte 3 inherited from prev
        *s++ = (unsigned char)(v >>  0);
        *s++ = (unsigned char)(v >>  8);
        *s++ = (unsigned char)(v >> 16);
    } else {
        *s++ = 0xf1;
        tinystore32(s, v);
        s += 4;
    }

    advance:
    e->prev = v;
    e->row[e->x++] = v;
    if (e->run && (e->run==256 || e->x==e->width)) {
        s = tinyflush(e, s);
    }
    if (e->x == e->width) {
        e->x  = 0;
        e->up = true;
    }
    return (int)(s - (unsigned char *)buf);
}

TinyDecoder tinydecoder(void *buf, ptrdiff_t len)
{
    TinyDecoder d = {0};
    d.error = true;
    if (len < TINYHDRLEN) {
        return d;
    }

    unsigned char *s = buf;
    uint32_t w = tinyload32(s+ 4);
    uint32_t h = tinyload32(s+ 8);
    uint32_t n = tinyload32(s+12);
    if (s[0]!='T' || s[1]!='C' || s[2]!='O' || s[3]!='M' ||
        w-1 >= TINYMAXWIDTH || h-1 >= 0x7fffffff || n-1 >= 4) {
        return d;
    }

    d.p         = s + TINYHDRLEN;
    d.end       = s + len;
    d.prev      = TINYINIT;
    d.mask      = n<4 ? (1u << 8*n) - 1 : 0xffffffff;
    d.width     = (int)w;
    d.height    = (int)h;
    d.nchannels = (int)n;
    d.error     = false;
    return d;
}

uint32_t tinydecode(TinyDecoder *d)
{
    if (d->error || !d->row || d->y>=d->height) {
        goto fail;
    }

    uint32_t u = d->y ? d->row[d->x] : TINYINIT;
    uint32_t v;

    if (d->run) {
        d->run--;
        v = d->runop==0xf3 ? u : d->prev;
        goto advance;
    }

    if (d->p == d->end) {
        goto fail;
    }
    int t = *d->p++;
    ptrdiff_t avail = d->end - d->p;
    unsigned char *s = d->p;

    if ((t>=0xe0 && t<=0xef) || t==0xf2 || t==0xf3) {
        int k = t - 0xdf;
        if (t >= 0xf0) {
            if (avail < 1) goto fail;
            k = *d->p++ + 1;
        }
        if (k>d->width-d->x || (t==0xf3 && !d->y)) {
            goto fail;
        }
        d->runop = (uint8_t)t;
        d->run   = k - 1;
        // Run pixels come from the output, so they're canonical.
        v = t==0xf3 ? u : tinynorm(d->prev, d->mask);
        goto advance;
    }

    if (t==0xf0 || t==0xf1) {
        if (t == 0xf0) {
            if (avail < 3) goto fail;
            v = (uint32_t)s[0] | (uint32_t)s[1]<<8 | (uint32_t)s[2]<<16 |
                (d->prev & 0xff000000);
            d->p += 3;
        } else {
            if (avail < 4) goto fail;
            v = tinyload32(s);
            d->p += 4;
        }
    } else if (t>=0xc0 && t<=0xdf) {
        v = d->table[t-0xc0];
    } else if (t >= 0xf8) {
        goto fail;
    } else {
        uint32_t b = d->x ? tinypred(d->prev, u) : u;
        int dr, dg, db;
        if (t < 0x40) {
            dr = (t>>0 & 3) - 2;
            dg = (t>>2 & 3) - 2;
            db = (t>>4 & 3) - 2;
        } else if (t < 0xc0) {
            if (avail < 1) goto fail;
            int z = (t-0x40)<<8 | s[0];
            d->p += 1;
            dg = (z     & 31) - 16;
            dr = (z>>5  & 31) - 16 + dg;
            db = (z>>10 & 31) - 16 + dg;
        } else {
            if (avail < 2) goto fail;
            int z = (t-0xf4)<<16 | s[0] | s[1]<<8;
            d->p += 2;
            dg = (z     & 63) - 32;
            dr = (z>>6  & 63) - 32 + dg;
            db = (z>>12 & 63) - 32 + dg;
        }
        v = tinyadd(b, dr, dg, db);
    }

    // A crafted stream may set bytes outside the channel count. These are
    // never output, but persist in prev and the table, as in the reference.
    d->table[tinyhash(v)] = v;
    d->prev = v;
    v = tinynorm(v, d->mask);
    d->row[d->x] = v;
    goto next;

    advance:
    d->prev = v;
    d->row[d->x] = v;

    next:
    if (++d->x == d->width) {
        d->x = 0;
        if (++d->y == d->height) {
            d->error = d->p != d->end;
        }
    }
    return v;

    fail:
    d->error = true;
    return 0;
}
