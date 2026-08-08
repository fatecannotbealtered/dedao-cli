# Compatibility

`dedao-cli` talks to Dedao's web app endpoints. Dedao publishes no API contract
and no versioning, so "the verified backend version" is a **date**: the day each
endpoint was last observed answering in the shape this tool parses.

## Verified surface

Every declared command was exercised against the live upstream on **2026-08-07**
with a signed-in account, and twelve `output_schema` entries in `reference` were
corrected to the payload actually measured that day.

| Area | Endpoint prefix | Last verified | Notes |
|---|---|---|---|
| Library | `/api/hades/*` | 2026-08-07 | `library`, `library-nav`, `library-groups`, `library-group` |
| Progress | `/api/pc/blade/*` | 2026-08-07 | `recent`, `progress` |
| Courses | `/pc/bauhinia/*` | 2026-08-07 | `course`, `articles`, `article` |
| Notes and comments | `/pc/ledgers/*` | 2026-08-07 | `comments`, `article-notes`, `note`, `topics`, `topic` |
| Ebooks | `/pc/ebook2/*`, `/pc/vipcomment/*` | 2026-08-07 | `ebook`, `ebook-community` |
| Audiobooks | `/pc/odob/*`, `/pc/sunflower/*` | 2026-08-07 | `audiobook*` |
| Search | `/api/search/*` | 2026-08-07 | `search`, `search-type`, `search-suggest`, `search-hot` |
| Discovery | `/pc/sunflower/*`, `/pc/label/*`, `/pc/sphere/*`, `/pc/ddlive/*` | 2026-08-07 | `discover`, `labels`, `label-content`, `free`, `live`, `channel*` |

Two shapes worth recording, because they cost time to find:

- Most endpoints answer in Dedao's `{h: {c, e}, c: {...}}` envelope, and the
  client unwraps `c`. **`/api/search/pc/tophits` nests a second envelope inside
  it** (`c: {status, data}`); the client unwraps that too, so `search` returns
  the hits rather than transport plumbing.
- Business code **`104000` means "unknown identifier"**, even though the message
  reads `服务异常，请稍后重试`. It is mapped to `E_NOT_FOUND` (exit 3,
  non-retryable), because taking the message at face value would have an agent
  retrying a permanently missing resource forever.

## What "compatible" means here

This tool is a **compatibility client**, not an integration. Dedao can change any
of these endpoints without notice and owes this project nothing.

- A shape change surfaces as an error or a parse failure, not as silently wrong
  data.
- `reference` is generated from this build, so it always describes what the
  binary does — never what the upstream does today. When a command starts
  failing, assume the upstream moved before assuming the tool is broken.

## Re-verifying

There is no automated live gate — that is why `release_readiness.level` is
`beta`. To re-verify by hand, run each command against the live upstream and
compare the top-level keys of `data` against that command's
`output_schema.fields` from `reference`. A mismatch in either direction is a
finding. Note that the mock-upstream tests return synthetic shapes and therefore
cannot catch this class of drift on their own.

## Platform support

| Platform | Status |
|---|---|
| Linux x64 / arm64 | supported |
| macOS x64 / arm64 | supported |
| Windows x64 | supported |

Every command is plain HTTP; nothing needs to be installed. `login` renders a QR
code that a human scans with the Dedao app.
