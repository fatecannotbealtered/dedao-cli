# Dedao web endpoint contract

Verified: 2026-08-07, against a signed-in account, by observing the official web
front-end's own traffic in a browser.

These are the HTTP endpoints Dedao's current web client uses. They are not a
public, stable, or supported developer API. Everything here was confirmed by
watching real requests, not inferred from documentation or from an earlier
implementation.

## How to read this

- **Auth**: `cookie` means a normal signed-in session is sufficient. Nothing in
  this document requires reproducing a request signature.
- **Entitlement**: what the account must own for the call to return content.
  When the account lacks it, the endpoint answers honestly and the CLI reports
  that answer rather than working around it.
- Response bodies use Dedao's uniform wrapper: `h` carries the business status
  (`h.c == 0` on success) and `c` carries the payload.

## Session

| Purpose | Method and path | Auth |
|---|---|---|
| Current user | `GET /api/pc/user/info` | cookie |
| Anonymous OAuth token (login only) | `POST /loginapi/getAccessToken` | none |
| Create login QR | `GET /oauth/api/embedded/qrcode` | oauth token header |
| Poll QR scan | `POST /oauth/api/embedded/qrcode/check_login` | oauth token header |

`user/info` returns `uid_hazy`, `nickname`, `is_v`, `vip_user`, `is_teacher`,
`today_study_time`, `study_serial_days`. Presence of `uid_hazy` is the reliable
signed-in signal.

## Shelf

**The category list is not a fixed set.** `navbar/get` returns the live
categories with per-filter counts; the client reads them rather than hardcoding.
As of verification the account sees six:

| category | name | filters |
|---|---|---|
| `study` | 学习 | all |
| `plan` | 计划 | all |
| `bauhinia` | 课程 | all, progress, category |
| `ebook` | 电子书书架 | all, progress, category |
| `odob` | 听书书架 | all, progress, category |
| `challenge` | 挑战赛 | all |

| Purpose | Method and path | Auth |
|---|---|---|
| Shelf categories and counts | `GET /api/hades/v1/navbar/get` | cookie |
| Shelf index | `POST /api/hades/v1/index/detail` | cookie |
| Owned items in a category | `POST /api/hades/v2/product/list` | cookie |
| Whether a category has groups | `POST /api/hades/v1/group/has` | cookie |
| Items in a group | `POST /api/hades/v2/product/group/list` | cookie |

`product/list` body: `{category, display_group, filter, filter_complete,
group_id, order, page, page_size, sort_type}`. Items carry `enid`, `id`,
`type`, `class_id`, `title`, `author`, `progress`, `duration`, `course_num`,
`publish_num`, `last_read`.

## Course and article

| Purpose | Method and path | Auth | Entitlement |
|---|---|---|---|
| Course metadata | `POST /pc/bauhinia/pc/class/info` | cookie | — |
| Article list | `POST /api/pc/bauhinia/pc/class/purchase/article_list` | cookie | owns the course |
| Study progress | `POST /api/pc/bauhinia/pc/class/user_data` | cookie | owns the course |
| Course diploma | `POST /api/pc/bauhinia/pc/class/diploma` | cookie | owns the course |
| Article metadata + content token | `POST /pc/bauhinia/pc/article/info` | cookie | — |
| Article body | `GET /pc/ddarticle/v1/article/get/v2` | cookie | `is_buy: 1` |

Three details the shape depends on, each confirmed against live traffic. Getting
any of them wrong yields `h.c == 0` with an empty payload -- a silent failure
that looks like "the account owns nothing":

1. The article list arrives under **`c.article_list`**, not `c.list`.
2. `article/info` takes **`detail_id`**, not `id`. Passing `id` returns business
   code `104000`.
3. The content token field is **`dd_article_token`**, not `token`. It is empty
   in the article-list rows and only populated by `article/info`.

`article/get/v2` query: `token`, `sign=b23a426b357d1b83`,
`appid=1632426125495894021`, `is_new=1`. **`sign` is a fixed client identifier,
not a per-request signature** -- the same constant appears on every call from
the official front-end. It is not an access-control mechanism; entitlement is
carried by the session and reflected in `is_buy`.

`c.content` is a JSON string holding a node array. Observed node types:
`paragraph`, `image`, `audio`, `blockquote`.

Verified: an owned article (`is_buy: 1`) returned 83 nodes / 4911 characters on
cookie auth alone.

## Notes, comments, knowledge city

| Purpose | Method and path | Auth |
|---|---|---|
| Article comments | `POST /pc/ledgers/notes/article_comment_list` | cookie |
| The account's own comments | `POST /pc/ledgers/notes/article_user_comment_list` | cookie |
| Article note line | `POST /api/pc/ledgers/notes/article_noteline` | cookie |
| Note detail | `POST /pc/ledgers/notes/detail` | cookie |
| Note replies | `POST /pc/ledgers/comment/list_v2` | cookie |
| All topics | `POST /pc/ledgers/topic/all` | cookie |
| Topic detail | `POST /pc/ledgers/topic/detail` | cookie |
| Topic notes | `POST /pc/ledgers/topic/notes/list` | cookie |
| Selected topics | `POST /api/pc/ledgers/topic/select` | cookie |
| Friends timeline | `POST /pc/ledgers/notes/friends_timeline` | cookie |
| Friends timeline unread count | `POST /pc/ledgers/notes/get_friends_timeline_unread_count` | cookie |
| Whether the account may post | `POST /pc/ledgers/notes/is_user_postable` | cookie |
| Friend recommendations | `POST /pc/ledgers/friendship/recommend/bigdata` | cookie |

`topic/detail` must be called with `incr_view_count: false`. Reading must not
register a view on the account's behalf.

## Discovery, search, live

| Purpose | Method and path | Auth |
|---|---|---|
| Top hits search | `POST /api/search/pc/tophits` | cookie |
| Hot searches | `POST /api/search/pc/hot` | cookie |
| Suggestions | `POST /api/search/pc/suggest` | cookie |
| Course search | `POST /api/search/v2/pc/searchclass` | cookie |
| Ebook chapter search | `POST /api/search/v2/pc/searchebookchapter` | cookie |
| Audio search | `POST /api/search/v2/pc/searchaudio` | cookie |
| Topic search | `POST /api/search/v2/pc/searchtopic` | cookie |
| Discovery labels | `POST /pc/sunflower/v1/label/list` | cookie |
| Label content | `POST /pc/sunflower/v1/label/content` | cookie |
| Free resources | `GET /pc/sunflower/v1/resource/list` | cookie |
| Discovery filters | `POST /pc/label/v2/algo/pc/filter/list` | cookie |
| Discovery products | `POST /pc/label/v2/algo/pc/product/list` | cookie |
| Home banners | `GET /pc/sunflower/v1/banner/list?module_id=` | cookie |
| Training camps | `GET /pc/sunflower/v1/trainingcamp/list` | cookie |
| Students | `GET /pc/sunflower/v1/students` | cookie |
| Teachers | `GET /pc/sunflower/v1/teacher/list` | cookie |
| Home images | `GET /pc/sunflower/v1/image/list?module_id=` | cookie |
| Site modules | `GET /pc/sunflower/v1/module/list?module_type=` | cookie |
| Tips | `GET /api/pc/sunflower/v1/tips/info?version=` | cookie |
| Message center sections | `POST /api/pc/messagecenter/internal-message/index/section/info` | cookie |
| Live home | `POST /pc/ddlive/v2/pc/home/live` | cookie |
| Subscribed live | `POST /api/pc/ddlive/v2/pc/user/subscribe/live/list` | cookie |

## Ebook and audiobook

| Purpose | Method and path | Auth | Entitlement |
|---|---|---|---|
| Ebook VIP status | `POST /api/pc/ebook2/v1/vip/info` | cookie | — |
| Ebook detail | `GET /pc/ebook2/v1/pc/detail` | cookie | — |
| Ebook score | `GET /pc/ebook2/v1/pc/score/detail` | cookie | — |
| Ebook reviews | `POST /pc/vipcomment/v1/list` | cookie | — |
| Ebook notes | `POST /api/pc/ledgers/ebook/list` | cookie | — |
| Audiobook VIP card | `POST /pc/odob/v2/vipuser/vip_card_info` | cookie | — |
| Audiobook detail | `GET /pc/odob/pc/audio/detail` | cookie | — |

**Not verified.** The account used for verification owns 0 ebooks and 0
audiobooks, so the reader and media paths could not be exercised. Commands built
on them are declared unverified rather than assumed working -- an account
without the entitlement is expected to be refused, and that refusal is the
correct behaviour to surface.

## Failure handling

Business failures arrive as HTTP 200 with a non-zero `h.c` and a message in
`h.e`. Transport failures use real status codes. Both are mapped onto the
CLI's `E_*` codes by status and type, never by matching message text.

Observed: `104000` for a malformed `article/info` request.
