# End-to-end testing

## There is no disposable environment

Dedao has no sandbox and no test account tier. The only environment is the live
service, read with a real signed-in account. That shapes everything below.

The blast radius is nil: every upstream command is read-only — the tool never
purchases, comments, follows, or mutates progress — so an E2E run cannot change
anything in the account. The risk is entirely on the other side: rate limiting,
and exposing the account session.

## Layers

| Layer | What it covers | Network | Runs in CI |
|---|---|---|---|
| Unit | parsing, node rendering, envelope unwrapping, schema guards | none | yes |
| Mock upstream | every command's success, bad-args, auth-failure, and upstream-failure paths against an in-process HTTP server | none | yes |
| Live smoke | every declared command against the real service with a signed-in account | yes | **no** |

Only the last needs a real account, and it is the reason
`release_readiness.level` is `beta`. Note the mock layer returns **synthetic
payload shapes**: it proves the envelope, the error mapping, and the exit codes,
but it cannot prove that a declared `output_schema` matches what Dedao really
sends. Only the live smoke can, which is why it is described in detail here.

## Running the live smoke

```bash
dedao-cli status --compact               # confirm the session still authenticates
dedao-cli reference --compact            # read output_schema.fields per command

# Harvest real identifiers first; opaque ids must come from a listing,
# never be guessed:
dedao-cli library course --page-size 1 --compact     # -> a course enid
dedao-cli articles <course-enid> --count 1 --compact # -> an article enid

# Then run each command and compare its data keys to the declared fields.
dedao-cli course <course-enid> --compact
dedao-cli article <article-enid> --render text --compact
```

A command whose live `data` keys differ from its declared `output_schema.fields`
is a finding in the tool: fix the schema to what was measured, and update the
verified date in [COMPATIBILITY.md](COMPATIBILITY.md).

## Rules for a live run

- **Never in CI.** It would need the account session as a secret and would make
  a third party's uptime this repo's build status.
- **Keep it small.** `--page-size 1` or `--count 1`. This is a compatibility
  check, not a crawl; bulk archival is out of bounds.
- **Never paste output into an issue or transcript.** Payloads carry personal
  notes, comments, and purchase history.
- **Never print the session cookie**, and never copy the state directory off the
  machine.
- **Do not test `login` repeatedly.** It requires a human to scan a QR code, and
  hammering it looks exactly like an attack.

## What a failing live run means

An unexpected error or schema mismatch usually means Dedao changed a payload.
Confirm against the endpoint before touching the code, then update the parser,
the `output_schema`, and the verified date in `COMPATIBILITY.md` together.
