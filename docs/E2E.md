# End-to-end testing

## There is no disposable environment

Dedao has no sandbox and no test account tier. The only environment is the live
service, read with a real signed-in account. That shapes everything below.

Dedao smoke commands have no mutation blast radius: they never purchase,
comment, follow, or change progress. GetNote has no disposable environment
either, so its write commands are tested with `--dry-run` by default. A live
confirmed-write smoke must use a clearly named disposable note created for that
run and remove it before the run ends. Account credentials and personal content
remain the primary exposure risk.

## Layers

| Layer | What it covers | Network | Runs in CI |
|---|---|---|---|
| Unit | parsing, node rendering, envelope unwrapping, schema guards | none | yes |
| Mock upstream | every command's success, bad-args, auth-failure, and upstream-failure paths against an in-process HTTP server | none | yes |
| Live smoke | every declared command against the real service with a signed-in account | yes | **no** |

Only the last needs a real account, and it is what carries
`release_readiness.level` to `stable`. Note the mock layer returns **synthetic
payload shapes**: it proves the envelope, the error mapping, and the exit codes,
but it cannot prove that a declared `output_schema` matches what Dedao really
sends. Only the live smoke can, which is why it is described in detail here.

## Running the live smoke

```bash
npm run live-smoke                       # needs a signed-in account; never runs a write
npm run live-smoke -- --bin ./dedao-cli  # against a locally built binary
```

The script reads the command list and the declared `output_schema` from
`reference`, harvests real identifiers from listings rather than inventing them,
runs every read command it can reach, and fails when a payload carries a field
the contract does not declare. A declared field that is absent is reported but
does not fail: pagination and truncation fields appear only when the page
boundary is known. `E_NOT_FOUND`, `E_FORBIDDEN`, and `E_RATE_LIMITED` are
answers, not faults, and are recorded as such.

It writes `live-smoke-report.json` — command names, outcomes, and field names
only, with no identifiers and no account content, so the report is safe to keep
with a release. It is gitignored: the script is the reproducible part, a report
is one run's evidence.

`--include-writes` additionally runs the GetNote mutation chain -- save, update,
share, tag add/remove, knowledge-base add/remove, delete -- through the two-step
confirmation gate against one disposable note created for that run and deleted
before it ends, by marker lookup if its id could not be read. It is opt-in:
nobody should have their notes written to because they ran a smoke test. A
release candidate needs one run with it. `getnote kb create` stays out because
GetNote has no command to delete a knowledge base, so a run could not clean up
after itself.

Its runs found seven contract defects that every mock test had passed, including
two permanent conditions reported as retryable faults, a payload that invited a
reader to present the publisher's words as the user's, and two commands whose
declared shape had almost nothing in common with the real one.

What it covers that nothing else can: reading a chapter of an owned ebook, which
exercises the AES decryption and the glyph reassembly, and saving an owned
audiobook, which exercises the HLS segment decryption. Both were taken on trust
until an account that owns something ran them.

Not run at all: `channel-topic` and `note`, because no listing this account can
read publishes an identifier for them.

## Running the live smoke by hand

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

# GetNote reads and safe mutation previews:
dedao-cli getnote auth status --compact
dedao-cli getnote notes --limit 1 --compact
dedao-cli getnote save --content "dedao-cli live smoke disposable note" --dry-run --compact
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
- **Do not confirm GetNote writes during a routine smoke.** If confirmed-write
  coverage is explicitly required, use one disposable note, record its ID, and
  delete only that note through a separately previewed and confirmed command.

## What a failing live run means

An unexpected error or schema mismatch usually means Dedao changed a payload.
Confirm against the endpoint before touching the code, then update the parser,
the `output_schema`, and the verified date in `COMPATIBILITY.md` together.
