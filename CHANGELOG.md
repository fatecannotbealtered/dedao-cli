# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- `doctor` gained a `plaintext_credentials` check, and `logout` now removes
  every plaintext credential file it knows about rather than only the one the
  current build writes. A state directory carried over from the reference
  implementation still held the session, the browser storage state, and a
  pending QR login in the clear; nothing read them any more, so they were pure
  exposure and neither command would have cleared them.

### Added

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


### Fixed

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

### Deprecated


### Removed


### Security

- `SECURITY.md` no longer claims encryption at rest that is not implemented, and
  no longer describes a self-update signature-verification flow for a tool that
  ships no `update` command.

<!--
Copy the block below for each release. Newest version first.
Keep the link references at the bottom of the file in sync.

## [0.1.0] - YYYY-MM-DD

### Added

- First public release.

### Changed

### Fixed

### Deprecated

### Removed

### Security

[Unreleased]: https://github.com/fatecannotbealtered/dedao-cli/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/fatecannotbealtered/dedao-cli/releases/tag/v0.1.0
-->
