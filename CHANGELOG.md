# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- Raise the Go toolchain to 1.26.6 to clear four reachable Go standard-library
  vulnerabilities reported by `govulncheck`: GO-2026-6218 (`net/url`),
  GO-2026-6090 (`crypto/tls`), GO-2026-5972 (`encoding/asn1`), and
  GO-2026-5026 (`net/http`).

## [1.0.1] - 2026-08-12

### Added

- `SECURITY.md` documents the pending QR-login window as an accepted risk:
  `login` persists the anonymous OAuth token and QR string as plaintext JSON
  (`login-pending.json`, `0600`/`0700`) outside the sealed store. It is
  pre-authentication state cleared on resume or expiry, but a copy exfiltrated
  mid-login could poll check-login and capture the session at scan time; the
  mitigation is the short QR TTL and clear-on-completion, not encryption.
- The `getnote note get` reference description states the id error split: a
  malformed note id is `E_VALIDATION` (exit 2), a well-formed unknown id is
  `E_NOT_FOUND` (exit 3).
- A guard test pins `release_readiness.live_smoke_total_commands` to the leaf
  command count `reference` enumerates, so a command added or removed without
  re-recording the live smoke fails the build instead of misstating the
  denominator.
- CI runs `govulncheck ./...` in the lint job.
- `scripts/check-clean.sh` fails CI when a tracked root-level file falls
  outside the explicit repo-skeleton allowlist, blocking committed debug
  captures and scraped assets.

### Changed

- The Go toolchain is raised to 1.26.5: the new `govulncheck` CI gate found
  five reachable Go standard-library vulnerabilities in 1.26.2 (`net`,
  `net/http`, `crypto/x509`), all fixed upstream by 1.26.3/1.26.4.
- `update --check` now reports the canonical keys the contract requires:
  `status` (`current` or `available`) and `target_version`. The non-canonical
  `latest_version` top-level key is removed; the `notice` object is unchanged.
- `context.data.credentials.storage` and the `doctor` `credentials` check now
  report the backend recorded in the stored Dedao session, falling back to a
  live probe only when nothing is stored — so a session sealed before a
  keyring-to-file degradation (or the reverse) is reported where it actually
  sits.
- The `changelog` example in `reference` uses the `<previous-version>`
  placeholder instead of a hardcoded version literal.

### Fixed

- `README.md` had drifted from `README_zh.md`: the release-gate enumeration
  was missing `update`, and the Courses capability row did not mention the
  notes-and-comments reading that `article-notes` and `comments` provide.

## [1.0.0] - 2026-08-11

### Added

- Added a dedicated `getnote` namespace backed by GetNote's official OpenAPI:
  encrypted API credential setup, note save/list/detail/update/delete/share,
  asynchronous task status, semantic search, tag management, and knowledge-base
  listing and note organization.
- Added GetNote capability and security guidance to `reference`, `context`,
  `doctor`, the bundled Skill, and both READMEs.
- Added `getnote save --idempotency-key` forwarding for safe create retries.
- `context` now distinguishes GetNote environment, encrypted-store, and mixed
  credential sources; `doctor` uses a bounded read-only request before it marks
  configured GetNote credentials valid.
- `npm run live-smoke` runs every read command it can reach against the real
  service and fails on any payload carrying a field the contract does not
  declare. This is the layer the mock tests cannot reach: mock upstream answers
  with shapes this repo wrote itself, so it can only confirm its own
  assumptions. The script harvests identifiers from listings rather than
  inventing them, never runs a write, and writes a report of command names,
  outcomes, and field names with no identifiers and no account content.
  `--include-writes` additionally runs the GetNote mutation chain -- save,
  update, share, tag add/remove, knowledge-base add/remove, delete -- through
  the two-step confirmation gate against one disposable note that is deleted
  before the run ends, by marker lookup if its id could not be read.
  `getnote kb create` stays out: GetNote has no command to delete a knowledge
  base, so a run could not clean up after it. The ebook and audiobook
  identifiers come from public listings rather than the library, because a
  detail endpoint answers for content the account does not own -- so those
  surfaces are covered, and their entitlement refusals are part of what is
  verified. Candidates are validated before use: an identifier the detail
  endpoint rejects is discarded rather than reported as a tool defect.
  `release_readiness` reports the coverage it actually has: 52 of 66 commands,
  7 of them writes, and it names the 4 it cannot reach.



- A package.json-backed runtime version source now keeps `--version`, self-
  description, doctor, changelog, and update metadata aligned.
- Query commands accept the standard `--limit` flag and expose normalized
  `count`/`has_more` pagination metadata; numeric ID fields are strings at the
  output boundary.

- Five content-reading commands complete the owned-content surface:
  `article-captions` (a course video's caption track as text),
  `ebook-chapters` and `ebook-read` (an owned ebook's contents and one chapter
  of its body), `audiobook-media` (an authorized audiobook saved to a local
  file), and `daily` (what has appeared in owned courses since the last run).
  Each checks entitlement against the account before fetching anything, so an
  unowned title is refused rather than half-read.
- `daily` keeps a checkpoint between runs. Its first run over a course records
  what is there and reports nothing, because reporting a subscriber's whole back
  catalogue as today's updates would be wrong; `baseline_created` says when that
  happened, and `--include-existing` asks for the catalogue on purpose. The file
  is written atomically and kept private, since it names what the account owns.
- Two secrets are read and never returned: an audiobook's play url and stream
  key, and an ebook's reading token. `audiobook-media` reports the file it wrote
  and nothing that would let the stream be fetched again elsewhere.

- Standalone-binary self-update verifies the release **in-process** before it
  replaces anything (SEC-SPEC §5): the Sigstore protobuf bundle over
  `checksums.txt` is checked with `sigstore-go` against a TUF-bootstrapped trust
  root, the signer identity is pinned by an anchored regexp to this repo's
  tagged release workflow and GitHub's OIDC issuer, then the archive SHA256 is
  checked against the now-trusted manifest. No external cosign, nothing
  preinstalled, and no skip path — a missing bundle, a bad signature, an
  unlisted asset, or a digest mismatch all fail closed as the non-retryable
  `E_INTEGRITY`. The installed binary is untouched until both links pass, and
  the swap is atomic.
- `dedao-cli update` — the single-command self-update the template treats as core
  equipment (CLI-SPEC §14, REPO-SPEC §4). No confirm token, no leaf subcommands:
  it resolves the release, drives the install method, and syncs the whole Skill
  directory in one call. `--check` and `--dry-run` are read-only probes.
- Version notifications as a structured contract: `update --check` refreshes a
  local notice cache, severity is graded from the embedded CHANGELOG delta
  (`warning` on a security entry or major bump, else `info`), and any command
  can surface the cached notice through `meta.notices` — read from a file,
  never a network call.
- npm-managed installs are upgraded by driving `npm install -g <pkg>@<version>`
  rather than mutating a file the manager owns or printing a command for the
  user to run. The idempotent no-op check runs first, so an already-current
  install never shells out.
- Credentials are encrypted at rest (SEC-SPEC §4). Secrets are sealed with
  AES-256-GCM; the 32-byte data key comes from the OS keyring where one exists
  (Windows Credential Manager / macOS Keychain / Linux Secret Service) and from
  machine-bound PBKDF2-SHA256 derivation where none does. The keyring holds the
  key rather than the payload because a Windows credential blob caps at 2560
  bytes and a cookie jar is larger than that.
- `context.data.credentials.storage` and the `doctor` `credentials` check report
  the live backend (`keyring` / `encrypted-file`), so a degraded install is
  visible rather than silent.
- `DEDAO_SECRET_BACKEND=file` forces the fallback backend; the test suites set it
  so `go test` never writes to a real credential store.

- `_untrusted` is now emitted, and names the externally-controlled fields.
  Listing commands previously carried **no marker at all** despite `reference`
  declaring `untrusted_fields` for 33 schemas, so an agent had no signal that
  course titles, comments, and notes were external content (SEC-SPEC §2).
- `reference` declares positional arguments in `params[]`. Twenty-four commands
  took required positionals (`course <course-enid>`, `label-content <label-enid>
  <nav-type> <result-type>`, …) that appeared nowhere in the structured contract.
- `docs/COMPATIBILITY.md` and `docs/E2E.md`.
- CI runs `scripts/check-spec.js`, so vendored spec drift and a stale
  `contract_gen.go` fail the build rather than only a local run.

### Changed

- The T1 threat model now includes explicitly confirmed GetNote writes while
  preserving the existing read-only boundary for every Dedao endpoint.
- `login` now authorizes GetNote in the same pass as the Dedao QR scan, using
  GetNote's OAuth 2.0 device flow: it returns a verification link, a user code,
  and a scannable QR, and `login-resume` settles both halves in one call. The
  credentials are minted by the authorization and sealed in the encrypted store,
  so nothing is copied out of a developer console. No browser is launched — the
  link is handed back for a human to open, the same way the QR image is. Use
  `login --skip-getnote` for content only, and `--oauth-client-id` to authorize
  through your own registered application. The note half never blocks the
  content half: an authorization that expires or cannot start still leaves
  `login-resume` succeeding with `getnote.authorized: false`.
- `status` now reports the GetNote credential state alongside the Dedao session,
  so one call answers what the tool is authenticated for. `getnote auth status`
  remains for the note-only workflow.
- `logout` now clears the stored GetNote credentials as well as the Dedao
  session: it means "this machine no longer holds my credentials". The dry-run
  preview names both deletions, the confirmation token is bound to both, and
  credentials supplied through `GETNOTE_API_KEY` / `GETNOTE_CLIENT_ID` are
  reported as still active rather than counted as removed. `getnote auth logout`
  remains for clearing only the note credentials, and `logout --keep-getnote`
  signs out of Dedao while leaving note access in place, so switching Dedao
  accounts does not cost a separate authorization. The confirmation token is
  bound to the scope in both directions.



- `reference.error_codes` exposes the canonical E_* bindings while
  `reference.exit_codes` exposes the numeric exit-meaning table from
  `contract.json`; local credential deletion documents the required
  dry-run/confirm flow.

### Fixed

- `ebook`, `audiobook-agency` and `topic` each declared a shape with little or
  nothing in common with the real one: `ebook` promised a nested
  `book_info`/`price_info` record where the service sends 51 flat fields, and
  the other two promised envelopes that never arrive. All three ran against the
  live service for the first time here.
- Business code 4000 -- the ebook page endpoint's "this chapter has no body",
  which front matter such as a copyright page returns -- was a retryable
  `E_SERVER`. It is permanent, so it is now `E_NOT_FOUND`.
- The mock upstream now sets the CSRF cookie, requires `_csrf`, and checks the
  token header name. It did none of these, which is why it could not fail while
  the client got all three wrong.
- Login worked for nobody. Two independent faults on the pre-QR path, each
  found by reproducing the call outside this tool: `/loginapi/getAccessToken`
  wants the site's `csrfToken` cookie echoed back as the form field `_csrf`, not
  as any spelling of an `x-csrf-token` header, and sending none at all answers a
  bare 403 that reads like an IP block; and the QR endpoints take the token in
  `X-Oauth-Access-Token`, while this build sent `xi-oauth-token`, which the
  service ignores before reporting `Invalid access token ''` -- an empty token,
  not a rejected one. The mock layer cannot see either: it has no cookies, no
  CSRF, and does not check header names.
- A permission wall no longer arrives as a retryable service fault. Dedao
  answers business code 90015 with "无权访问" for content the account has no
  subscription to, and 5218 for an audiobook product that does not exist; both
  fell through to `E_SERVER`, which is retryable, so an agent would have retried
  a wall that never opens and an id that will never resolve. They are now
  `E_FORBIDDEN` and `E_NOT_FOUND`. Each was reproduced against two different
  inputs, and classified by code rather than by message text.
- `ebook` declared a nested `book_info`/`price_info` shape. The service answers
  with a flat record of 51 fields, of which only `author_info` overlapped: an
  agent reading `book_info` found nothing, every time. `audiobook` was missing
  `quality`.

- `label-content` declared a shape with no field in common with the real one.
  The service returns `product_list`, `navigation_list`, `current_enid`,
  `page_id`, `page_size`, `request_id`, and `is_more`; the contract declared
  `list`, `has_more`, `is_more`, `count`, and `page`, of which only `is_more`
  ever arrives.
- `article-notes` no longer invites a caller to report Dedao's words as the
  user's. The point endpoint returns Dedao's own summary of an article whether
  or not the account highlighted anything; sitting under the key `point` beside
  the account's `notes`, it read as the person's own writing. It is now
  `article_point`, with the upstream ownership flag surfaced as
  `account_wrote_point`. Measured against the live service, where editorial text
  arrives with the flag set to 0.
- `comments` declares the fields the service actually returns: `is_more`, the
  service's own pagination flag, and `article`, which echoes what was asked
  about. Neither was declared. `page` was declared and never arrives. The mock
  layer answers with synthetic shapes, so only a live read could catch this.
- `course` declares `count` and `intro_article`, and no longer declares
  `now_label`, which the service never sends.
- An unauthorized `getnote` command now points at `dedao-cli login`, which
  authorizes note access and mints the credentials, instead of the manual
  API-key path. That path is still named as the unattended option.


- JSON output now canonicalizes every object key to snake_case, converts
  semantic timestamps to RFC3339 UTC, preserves incomplete display text under
  `*_label`, and rejects normalization collisions instead of silently dropping
  one value.
- Logout confirmation tokens now cover legacy plaintext credential files as
  well as the encrypted session, so adding or replacing any credential target
  after dry-run returns `E_CONFLICT` without deleting it.
- README and security guidance now document the required two-step confirmation
  flow for the destructive local `logout` command.


- Self-update could never have found its own release. `.goreleaser.yml`
  publishes `<tool>-<version>-<os>-<arch>` but the updater looked for
  `<tool>_<version>_<os>_<arch>`; the two are now pinned together by a test that
  reads the release template rather than restating it. The previous test
  asserted the wrong convention, so it had locked the bug in.
- `update --check` reported a repository with no published release as
  `E_NETWORK` (retryable). GitHub answering 404 is a definite answer, so the
  check now succeeds with nothing available, and `update` itself reports
  `E_NOT_FOUND` rather than a retryable failure.
- The npm packaging step could not be rehearsed outside CI. It shelled out to
  `unzip` or to whichever `tar` was first on PATH, and an absolute Windows path
  reads as a remote-host spec to GNU tar. Release zips are now read with Node's
  own zlib, so the step needs no external tool and runs anywhere.


- `package-lock.json` was missing while `SECURITY.md` claimed the lockfile was
  committed. It is committed now, and CI installs with `npm ci` so the audit
  resolves exactly the tree a release ships.
- `SECURITY_zh.md` had never been updated alongside its English counterpart: it
  still described a config file, environment variables, and an interactive
  secret prompt that do not exist. Rewritten to match.
- `reference` declared `update` as a `read` command. It replaces the binary and
  rewrites the Skill directory, so it now declares `self-update` and no longer
  understates its blast radius.
- `context` now carries the cached update notice in `data`, which the
  notification contract requires of active-check commands; it is still read
  from the local cache and never from the network.
- The template treats `update` as a core lifecycle command; this build had
  removed it from the docs instead of implementing it, so the Skill, both
  READMEs and SECURITY.md all told agents it did not exist. It exists now and
  those documents describe it again.
- The session was stored as plaintext JSON in the state directory, and
  `SECURITY.md` claimed it was encrypted. It is now genuinely encrypted, and a
  session left by an earlier build is sealed on first read with the plaintext
  original deleted. Any copy that left the machine before this upgrade should be
  treated as compromised.

- Business code `104000` — Dedao's answer for an unknown identifier — was mapped
  to `E_SERVER` with `retryable: true`, so an agent handed a stale enid would
  retry a permanently missing resource forever. It is now `E_NOT_FOUND`
  (exit 3, non-retryable). Its upstream message reads `服务异常，请稍后重试`,
  which is why classifying by message text would get this exactly backwards.
- `search` returned the upstream's transport envelope — an apm id, timings, a
  duplicate status block — with the actual hits buried under `data`. The search
  backend nests a second envelope inside Dedao's own; the client now unwraps it.
- Twelve `output_schema` declarations did not match the payloads the commands
  actually return. `search-hot` declared `list` and returns `hot_tab_list`;
  `articles` declared `list`/`max_id` and returns `article_list`; `audiobook-vip`
  declared `is_vip`/`card_info` and returns `card`/`privilege`/`user`. Because
  `--fields` validates against the real payload, `--fields <declared-field>`
  failed with `E_VALIDATION`. All twelve were corrected against live responses.
- `release_readiness.reason` claimed article body text was not implemented while
  `article` was declared and working; the Skill and its eval scenarios repeated
  the claim and told agents to refuse the request.
- `SKILL.md`, `test-prompts.json`, `README.md`, `README_zh.md`, and
  `SECURITY.md` documented an `update` command that does not exist.
- `README.md` / `README_zh.md`: the Agent Install block exported `DEDAO_HOST`
  and `DEDAO_TOKEN`, neither of which the tool reads; Configuration pointed at
  `~/.dedao-cli/config.json`, which does not exist; the Capabilities table was
  still the template stub; and both described a write/confirm flow for a tool
  with no write commands. `README_zh.md` additionally had untranslated English
  spliced into Chinese sentences.
- `SECURITY.md` claimed credentials were encrypted at rest with AES-256-GCM.
  They are not: the session is plaintext JSON in the state directory.

### Security

- Every GetNote upstream write now requires a dry-run preview and a five-minute,
  single-use confirmation token bound to the exact command, request payload,
  credential context, and available target version. Changed arguments,
  credentials, target state, expired tokens, and replayed tokens fail before
  any mutation request is sent.
- GetNote API keys and client IDs use the existing AES-256-GCM secret store in
  an isolated state subdirectory; credentials are never written to GetNote's
  legacy plaintext config format or emitted in command output.
- Structured upstream error details are preserved under an explicitly
  `_untrusted` field while the stable top-level error message remains local.


- Logout confirmation tokens are bound to an irreversible fingerprint of the
  exact stored session, so replacing credentials after dry-run invalidates the
  old token before anything is deleted.
- `doctor` gained a `plaintext_credentials` check, and `logout` now removes
  every plaintext credential file it knows about rather than only the one the
  current build writes. A state directory carried over from the reference
  implementation still held the session, the browser storage state, and a
  pending QR login in the clear; nothing read them any more, so they were pure
  exposure and neither command would have cleared them.


- `SECURITY.md` no longer claims encryption at rest that is not implemented, and
  no longer describes a self-update signature-verification flow for a tool that
  ships no `update` command.

<!--
Copy the block below for each release. Newest version first.
Keep the link references at the bottom of the file in sync.

## [1.0.0] - YYYY-MM-DD

### Added

- First public release.

### Changed

### Fixed

### Deprecated

### Removed

### Security

-->

[Unreleased]: https://github.com/fatecannotbealtered/dedao-cli/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/fatecannotbealtered/dedao-cli/releases/tag/v1.0.1
[1.0.0]: https://github.com/fatecannotbealtered/dedao-cli/releases/tag/v1.0.0
