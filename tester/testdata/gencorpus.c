// Generates corpus.jsonl from the original TCOM implementation.
//   $ cc -O2 -I../.. -o gencorpus gencorpus.c ../../tinycodec.c && ./gencorpus >corpus.jsonl
#define main test_main
#include "../../test.c"
#undef main
#include "../../original/tcom.c"

static void hex(unsigned char *p, ptrdiff_t n)
{
    for (ptrdiff_t i = 0; i < n; i++) printf("%02x", p[i]);
}

int main(void)
{
    enum { CAP = 1<<20 };
    unsigned char *src = malloc(CAP), *enc = malloc(5*CAP+32);
    unsigned char *mut = malloc(5*CAP+32), *dec = malloc(CAP);
    rng = 12345;
    for (int iter = 0; iter < 600; iter++) {
        int n = 1 + randn(4), w, h;
        switch (randn(4)) {
        case 0:  w = 1 + randn(8);   h = 1 + randn(8);  break;
        case 1:  w = 1 + randn(300); h = 1 + randn(3);  break;
        case 2:  w = 1 + randn(24);  h = 1 + randn(24); break;
        default: w = 1 + randn(3);   h = 1 + randn(60); break;
        }
        genimage(src, w, h, n, randn(6));
        ptrdiff_t rawlen = (ptrdiff_t)w*h*n;
        unsigned v[3] = {(unsigned)w, (unsigned)h, (unsigned)n};
        size_t len = tcom(src, (size_t)rawlen, enc, 5*CAP+32, v, 0);
        printf("{\"type\":\"enc\",\"w\":%d,\"h\":%d,\"n\":%d,\"raw\":\"", w, h, n);
        hex(src, rawlen);
        printf("\",\"enc\":\"");
        hex(enc, (ptrdiff_t)len);
        printf("\"}\n");

        for (int m = 0; m < 6; m++) {
            memcpy(mut, enc, len);
            ptrdiff_t mlen = (ptrdiff_t)len;
            for (int f = 1 + randn(3); f; f--)
                mut[16 + randn((int)(len-16))] = (unsigned char)rand32();
            if (!randn(4)) mlen -= randn(3);
            if (!randn(4)) mlen += randn(3);
            unsigned dv[3];
            size_t z = tcom(mut, (size_t)mlen, NULL, 0, dv, 1);
            int ok = z && tcom(mut, (size_t)mlen, dec, z, dv, 1) == z;
            printf("{\"type\":\"dec\",\"data\":\"");
            hex(mut, mlen);
            printf("\",\"ok\":%s,\"raw\":\"", ok ? "true" : "false");
            if (ok) hex(dec, (ptrdiff_t)z);
            printf("\"}\n");
        }
    }
    return 0;
}
