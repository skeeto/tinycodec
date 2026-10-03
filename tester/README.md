# TCOM conformance tester

An HTTP service that tests an implementation of `SPECIFICATION.md`.
Its intended user is a model given only the specification, a URL and an
API key. The model fetches challenges, submits answers and is told only
pass or fail. You watch real progress on a separate dashboard.

```
go build && ./tcomtester -admin-key SECRET
```

| Flag | Default | Purpose |
|---|---|---|
| `-addr` | `:8080` | Challenge API, for the model. `GET /` documents it. |
| `-admin` | `127.0.0.1:8081` | Dashboard (HTTP basic auth: any user, password from `-admin-key`) |
| `-admin-key` | random, printed | Dashboard password |
| `-log` | `tester.jsonl` | Append-only submission log, replayed at startup |

Any API key works and is registered on first use. To start a key over, use
a new one. Give the model the specification, the URL and a key, and tell it
to read the URL's root page.

## Challenges

There are 77 challenges, and 250 passes are needed in all. Most challenges
have variants, one per channel count. Invalid-stream challenges also have a
valid "twin", so answering "invalid" every time can't pass. Every fetch
generates a fresh random instance, and each instance accepts one submission.

Every instance is checked against **mutants**: the reference codec with one
plausible bug injected (`Quirk` in `codec.go`). Each challenge names the
mutants it targets, and the generator retries until the correct answer
differs from each of their answers. So every instance actually tests its
feature, and a model can't pass a challenge with a bug it targets.

## Keep it hidden

A model with shell access could read this directory, `tinycodec.c` or
`original/`. Run trials from a directory or machine that can't see this
repository. The dashboard's separate port and password keep the expected
answers out of the model's reach.

## Tests

```
go test .                       # 200 instances per challenge variant
go test . -run Challenges -instances 1000
```

- `codec_test.go` checks the reference against the spec's test vectors and
  against `testdata/corpus.jsonl`, which was produced by the original
  implementation (`testdata/gencorpus.c`).
- `challenges_test.go` checks that every generator finds discriminating
  instances, that every opcode is emitted somewhere, and that every mutant
  is targeted.
- `server_test.go` runs a perfect bot, which must solve everything, and one
  bot per mutant, each of which must fail every challenge that targets it.
  It also checks request errors and log replay.
