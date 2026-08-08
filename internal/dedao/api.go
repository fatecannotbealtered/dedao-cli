package dedao

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Fixed client identifiers the article endpoint requires. Confirmed against
// live front-end traffic: these are constants on every request, not a
// per-request signature, and carry no access-control meaning.
const (
	articleSign  = "b23a426b357d1b83"
	articleAppID = "1632426125495894021"
)

// LibraryCategories maps the CLI's friendly names to Dedao's internal category
// keys. Agents pass the friendly name; the wire value never leaks into help.
var LibraryCategories = map[string]string{
	"course":    "bauhinia",
	"ebook":     "ebook",
	"audiobook": "odob",
}

// SearchTabs maps the CLI's --tab values to Dedao's numeric tab types.
var SearchTabs = map[string]int{
	"discover":       0,
	"course-content": 65,
	"purchased":      402,
	"course":         66,
	"ebook":          2,
	"audiobook":      13,
	"live":           735,
	"camp":           312,
	"topic":          6301,
	"note":           63,
}

// SpecialSearchPaths are the per-kind endpoints behind `search-type`.
var SpecialSearchPaths = map[string]string{
	"course":        "/api/search/v2/pc/searchclass",
	"ebook-chapter": "/api/search/v2/pc/searchebookchapter",
	"audio":         "/api/search/v2/pc/searchaudio",
	"topic":         "/api/search/v2/pc/searchtopic",
}

// --- account -------------------------------------------------------------

func (c *Client) UserInfo(ctx context.Context) (any, error) {
	return c.get(ctx, "/api/pc/user/info", nil)
}

// --- library -------------------------------------------------------------

func (c *Client) Library(ctx context.Context, category string, page, pageSize int) (any, error) {
	return c.post(ctx, "/api/hades/v2/product/list", map[string]any{
		"category":        category,
		"display_group":   true,
		"filter":          "all",
		"filter_complete": 0,
		"group_id":        0,
		"order":           "study",
		"page":            page,
		"page_size":       pageSize,
		"sort_type":       "desc",
	})
}

func (c *Client) LibraryNavigation(ctx context.Context) (any, error) {
	return c.get(ctx, "/api/hades/v1/navbar/get", nil)
}

func (c *Client) LibraryIndex(ctx context.Context) (any, error) {
	return c.post(ctx, "/api/hades/v1/index/detail", map[string]any{})
}

func (c *Client) LibraryGroups(ctx context.Context, category string) (any, error) {
	return c.post(ctx, "/api/hades/v1/group/has", map[string]any{"category": category})
}

func (c *Client) LibraryGroup(ctx context.Context, category string, groupID, page, pageSize int, order, filter string) (any, error) {
	return c.post(ctx, "/api/hades/v2/product/group/list", map[string]any{
		"category":        category,
		"display_group":   false,
		"filter":          filter,
		"filter_complete": 0,
		"group_id":        groupID,
		"order":           order,
		"page":            page,
		"page_size":       pageSize,
		"sort_type":       "desc",
	})
}

// --- recent and progress -------------------------------------------------

// Recent needs the account's hashed uid, which only /user/info carries, so this
// is a two-call read rather than a single passthrough.
func (c *Client) Recent(ctx context.Context, pageSize, maxID int) (any, error) {
	user, err := c.UserInfo(ctx)
	if err != nil {
		return nil, err
	}
	uidHazy := ""
	if object, ok := user.(map[string]any); ok {
		if value, ok := object["uid_hazy"].(string); ok {
			uidHazy = value
		}
	}
	return c.post(ctx, "/api/pc/blade/v2/recent", map[string]any{
		"filter_product_type": true,
		"max_id":              maxID,
		"page_size":           pageSize,
		"product_type":        "",
		"uid":                 nil,
		"uid_hazy":            uidHazy,
	})
}

func (c *Client) RecentIndex(ctx context.Context) (any, error) {
	return c.post(ctx, "/api/pc/blade/v2/recent-index", map[string]any{})
}

func (c *Client) LastStudy(ctx context.Context, count int, resultType string) (any, error) {
	return c.post(ctx, "/api/pc/blade/v2/pc/last-study", map[string]any{
		"count":       count,
		"result_type": resultType,
	})
}

// --- search --------------------------------------------------------------

func (c *Client) EbookVIPInfo(ctx context.Context) (any, error) {
	return c.post(ctx, "/api/pc/ebook2/v1/vip/info", map[string]any{})
}

func (c *Client) Search(ctx context.Context, query string, tabType, page, pageSize int, requestID string) (any, error) {
	// The VIP flag only tweaks ranking, so a failure here must never fail the
	// search itself.
	isEbookVIP := 0
	if info, err := c.EbookVIPInfo(ctx); err == nil {
		if object, ok := info.(map[string]any); ok {
			if vip, ok := object["is_vip"].(bool); ok && vip {
				isEbookVIP = 1
			}
		}
	}
	return c.post(ctx, "/api/search/pc/tophits", map[string]any{
		"content":      query,
		"is_ebook_vip": isEbookVIP,
		"page":         page,
		"page_size":    pageSize,
		"request_id":   requestID,
		"tab_type":     tabType,
	})
}

func (c *Client) SearchHot(ctx context.Context, requestID string) (any, error) {
	return c.post(ctx, "/api/search/pc/hot", map[string]any{
		"is_login":   1,
		"request_id": requestID,
	})
}

func (c *Client) SearchSuggest(ctx context.Context, query string, searchType int) (any, error) {
	return c.post(ctx, "/api/search/pc/suggest", map[string]any{
		"query":      query,
		"searchType": searchType,
	})
}

func (c *Client) SearchSpecial(ctx context.Context, kind, query string, page, pageSize int, requestID string, highlightCount int, resourceType *int) (any, error) {
	path, ok := SpecialSearchPaths[kind]
	if !ok {
		return nil, fmt.Errorf("unknown specialized search kind: %s", kind)
	}
	body := map[string]any{
		"content":    query,
		"hl_num":     highlightCount,
		"page":       page,
		"request_id": requestID,
		"size":       pageSize,
	}
	if resourceType != nil {
		body["resource_type"] = *resourceType
	}
	return c.post(ctx, path, body)
}

// --- discovery -----------------------------------------------------------

func (c *Client) Labels(ctx context.Context, navType, labelCount int) (any, error) {
	return c.post(ctx, "/pc/sunflower/v1/label/list", map[string]any{
		"label_count": labelCount,
		"nav_type":    navType,
	})
}

func (c *Client) LabelContent(ctx context.Context, labelEnid string, navType int, resultType string, page, pageSize int, requestID string) (any, error) {
	return c.post(ctx, "/pc/sunflower/v1/label/content", map[string]any{
		"enid":        labelEnid,
		"nav_type":    navType,
		"page":        page,
		"page_size":   pageSize,
		"request_id":  requestID,
		"result_type": resultType,
	})
}

func (c *Client) FreeResources(ctx context.Context) (any, error) {
	return c.get(ctx, "/pc/sunflower/v1/resource/list", nil)
}

// DiscoveryOptions are the shared filters behind `discover`.
type DiscoveryOptions struct {
	NavType      int
	Page         int
	PageSize     int
	ClassName    string
	LabelID      string
	NavigationID string
	ProductTypes string
	SortStrategy string
	TagIDs       []int
	RequestID    string
}

func (c *Client) discovery(ctx context.Context, path string, options DiscoveryOptions) (any, error) {
	tagIDs := options.TagIDs
	if tagIDs == nil {
		tagIDs = []int{}
	}
	return c.post(ctx, path, map[string]any{
		"classfc_name":  options.ClassName,
		"label_id":      options.LabelID,
		"nav_type":      options.NavType,
		"navigation_id": options.NavigationID,
		"page":          options.Page,
		"page_size":     options.PageSize,
		"product_types": options.ProductTypes,
		"sort_strategy": options.SortStrategy,
		"tags_ids":      tagIDs,
		"request_id":    options.RequestID,
	})
}

func (c *Client) DiscoveryFilters(ctx context.Context, options DiscoveryOptions) (any, error) {
	return c.discovery(ctx, "/pc/label/v2/algo/pc/filter/list", options)
}

func (c *Client) DiscoveryProducts(ctx context.Context, options DiscoveryOptions) (any, error) {
	return c.discovery(ctx, "/pc/label/v2/algo/pc/product/list", options)
}

// --- course and articles -------------------------------------------------

func (c *Client) CourseInfo(ctx context.Context, courseEnid string) (any, error) {
	return c.post(ctx, "/pc/bauhinia/pc/class/info", map[string]any{
		"detail_id": courseEnid,
		"is_login":  1,
	})
}

func (c *Client) CourseProgress(ctx context.Context, courseEnid string, reverse bool) (any, error) {
	return c.post(ctx, "/api/pc/bauhinia/pc/class/user_data", map[string]any{
		"detail_id": courseEnid,
		"reverse":   reverse,
	})
}

func (c *Client) CourseArticles(ctx context.Context, courseEnid string, count, maxID, maxOrderNum int, reverse bool) (any, error) {
	return c.post(ctx, "/api/pc/bauhinia/pc/class/purchase/article_list", map[string]any{
		"chapter_id":      "",
		"count":           count,
		"detail_id":       courseEnid,
		"include_edge":    false,
		"is_unlearn":      false,
		"max_id":          maxID,
		"max_order_num":   maxOrderNum,
		"reverse":         reverse,
		"since_id":        0,
		"since_order_num": 0,
		"unlearn_switch":  false,
	})
}

func (c *Client) ArticleComments(ctx context.Context, articleEnid string, page, pageSize int, sort string) (any, error) {
	return c.post(ctx, "/pc/ledgers/notes/article_comment_list", map[string]any{
		"detail_enid":  articleEnid,
		"note_type":    2,
		"only_replied": false,
		"page":         page,
		"page_count":   pageSize,
		"sort_by":      sort,
		"source_type":  65,
	})
}

func (c *Client) ArticleUserComments(ctx context.Context, articleEnid string) (any, error) {
	return c.post(ctx, "/pc/ledgers/notes/article_user_comment_list", map[string]any{
		"detail_enid": articleEnid,
		"note_type":   2,
		"source_type": 65,
	})
}

func (c *Client) ArticleNotes(ctx context.Context, articleEnid, sourceID string) (any, error) {
	return c.post(ctx, "/api/pc/ledgers/notes/article_noteline", map[string]any{
		"note_type":       2,
		"source_enid_str": articleEnid,
		"source_id_str":   sourceID,
		"source_type":     65,
	})
}

func (c *Client) ArticleNotePoint(ctx context.Context, articleEnid string, productType int) (any, error) {
	return c.post(ctx, "/api/pc/ledgers/notepoint/get/usernote", map[string]any{
		"article_id_hazy":   articleEnid,
		"audio_id":          "",
		"has_article_point": true,
		"product_type":      productType,
	})
}

func (c *Client) NoteDetail(ctx context.Context, noteID string, version int) (any, error) {
	return c.post(ctx, "/pc/ledgers/notes/detail", map[string]any{
		"note_id_hazy": noteID,
		"version":      version,
	})
}

func (c *Client) NoteComments(ctx context.Context, noteID string, count int, cursor string) (any, error) {
	return c.post(ctx, "/pc/ledgers/comment/list_v2", map[string]any{
		"note_id_hazy":         noteID,
		"count":                count,
		"max_id_and_timestamp": cursor,
	})
}

// --- ebook ---------------------------------------------------------------

func (c *Client) EbookInfo(ctx context.Context, ebookEnid string) (any, error) {
	return c.get(ctx, "/pc/ebook2/v1/pc/detail", url.Values{"id": {ebookEnid}})
}

func (c *Client) EbookScore(ctx context.Context, ebookEnid string) (any, error) {
	return c.get(ctx, "/pc/ebook2/v1/pc/score/detail", url.Values{"id": {ebookEnid}})
}

func (c *Client) EbookReviews(ctx context.Context, ebookEnid string, page, pageSize int, sort string) (any, error) {
	return c.post(ctx, "/pc/vipcomment/v1/list", map[string]any{
		"enid":      ebookEnid,
		"page":      page,
		"page_size": pageSize,
		"ptype":     2,
		"sort":      sort,
	})
}

func (c *Client) EbookNotes(ctx context.Context, ebookEnid, bookID string) (any, error) {
	return c.post(ctx, "/api/pc/ledgers/ebook/list", map[string]any{
		"book_enid":      ebookEnid,
		"book_id":        bookID,
		"is_old_version": false,
	})
}

// --- audiobook -----------------------------------------------------------
//
// Audiobook payloads are filtered through SafeAudiobookMetadata, an allowlist
// rather than a denylist, because these responses carry playback material.

func (c *Client) AudiobookInfo(ctx context.Context, topicID string) (any, error) {
	value, err := c.get(ctx, "/pc/odob/pc/audio/detail", url.Values{"topic_id_str": {topicID}})
	if err != nil {
		return nil, err
	}
	return SafeAudiobookMetadata(value), nil
}

func (c *Client) AudiobookAlias(ctx context.Context, aliasID string) (any, error) {
	value, err := c.post(ctx, "/pc/odob/pc/audio/detail/alias", map[string]any{"alias_id": aliasID})
	if err != nil {
		return nil, err
	}
	return SafeAudiobookMetadata(value), nil
}

func (c *Client) AudiobookRelated(ctx context.Context, topicID string) (any, error) {
	value, err := c.post(ctx, "/pc/odob/pc/detail/relation-book", map[string]any{"topic_id_str": topicID})
	if err != nil {
		return nil, err
	}
	return SafeAudiobookMetadata(value), nil
}

func (c *Client) AudiobookCollection(ctx context.Context, collectionEnid string) (any, error) {
	value, err := c.post(ctx, "/pc/sunflower/v1/depot/vip-user/topic-pkg/odob/details", map[string]any{"enid": collectionEnid})
	if err != nil {
		return nil, err
	}
	return SafeAudiobookMetadata(value), nil
}

func (c *Client) AudiobookAgency(ctx context.Context, agencyID string) (any, error) {
	value, err := c.post(ctx, "/pc/odob/pc/agency/detail", map[string]any{"agency_id_str": agencyID})
	if err != nil {
		return nil, err
	}
	return SafeAudiobookMetadata(value), nil
}

func (c *Client) AudiobookAgencyBooks(ctx context.Context, agencyID string, count, maxID, sinceID int) (any, error) {
	value, err := c.post(ctx, "/pc/odob/pc/agency/book-list", map[string]any{
		"agency_id_str": agencyID,
		"count":         count,
		"max_id":        maxID,
		"since_id":      sinceID,
	})
	if err != nil {
		return nil, err
	}
	return SafeAudiobookMetadata(value), nil
}

func (c *Client) AudiobookVIP(ctx context.Context) (any, error) {
	return c.post(ctx, "/pc/odob/v2/vipuser/vip_card_info", map[string]any{})
}

// --- topics and channel --------------------------------------------------

func (c *Client) Topics(ctx context.Context, page, pageSize int) (any, error) {
	return c.post(ctx, "/pc/ledgers/topic/all", map[string]any{
		"page_id": page,
		"limit":   pageSize,
	})
}

// TopicDetail always sends incr_view_count:false. Reading a topic must not
// register a view on the user's behalf -- this client never mutates state.
func (c *Client) TopicDetail(ctx context.Context, topicID string) (any, error) {
	return c.post(ctx, "/pc/ledgers/topic/detail", map[string]any{
		"topic_id_hazy":   topicID,
		"incr_view_count": false,
	})
}

func (c *Client) TopicNotes(ctx context.Context, topicID string, page, pageSize int, electedOnly bool) (any, error) {
	return c.post(ctx, "/pc/ledgers/topic/notes/list", map[string]any{
		"topic_id_hazy": topicID,
		"count":         pageSize,
		"page_id":       page,
		"is_elected":    electedOnly,
		"version":       2,
	})
}

func (c *Client) ChannelInfo(ctx context.Context, channelID int) (any, error) {
	return c.post(ctx, "/sphere/v1/app/channel/info", map[string]any{"channel_id": channelID})
}

func (c *Client) ChannelHome(ctx context.Context, channelID int, showAll bool) (any, error) {
	return c.post(ctx, "/pc/sphere/v1/app/topic/homepage/v2", map[string]any{
		"channel_id":  channelID,
		"is_show_all": showAll,
	})
}

func (c *Client) ChannelTopic(ctx context.Context, productID, productType int) (any, error) {
	return c.post(ctx, "/pc/sphere/v1/app/topic/detail", map[string]any{
		"filter":       "all",
		"product_type": productType,
		"product_id":   productID,
	})
}

func (c *Client) ChannelArticles(ctx context.Context, channelID, count int) (any, error) {
	return c.post(ctx, "/pc/sphere/v1/app/special/article_list", map[string]any{
		"channel_id": channelID,
		"count":      count,
	})
}

// --- live ----------------------------------------------------------------

func (c *Client) Live(ctx context.Context, subscribed bool) (any, error) {
	path := "/pc/ddlive/v2/pc/home/live"
	if subscribed {
		path = "/api/pc/ddlive/v2/pc/user/subscribe/live/list"
	}
	return c.post(ctx, path, map[string]any{})
}

// --- article body ---------------------------------------------------------

// ArticleInfo returns an article's metadata plus the token needed to fetch its
// body. The parameter is `detail_id`; passing `id` yields business code 104000.
func (c *Client) ArticleInfo(ctx context.Context, articleEnid string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	// Unsanitized on purpose: the content token lives in a field whose name
	// contains "token", which the output redactor strips.
	value, err := c.requestJSONUnsanitized(ctx, http.MethodPost,
		"/pc/bauhinia/pc/article/info", nil,
		map[string]any{"detail_id": articleEnid},
		c.baseURL+"/course/article?id="+articleEnid)
	if err != nil {
		return nil, err
	}
	info, _ := value.(map[string]any)
	if info == nil {
		info = map[string]any{}
	}
	return info, nil
}

// ArticleContentResult is a rendered article body plus the entitlement facts
// that explain why it is (or is not) complete.
type ArticleContentResult struct {
	Entitled     bool           `json:"entitled"`
	FreeTrial    bool           `json:"free_trial"`
	NodeCount    int            `json:"node_count"`
	ContentChars int            `json:"content_chars"`
	Text         string         `json:"text,omitempty"`
	Markdown     string         `json:"markdown,omitempty"`
	Nodes        []ContentNode  `json:"nodes,omitempty"`
	Info         map[string]any `json:"info"`
}

// ArticleContent fetches an owned article's body.
//
// Entitlement is the session's, not ours to widen: when `is_buy` is false the
// upstream simply does not hand back a usable token, and that refusal is
// reported rather than worked around.
func (c *Client) ArticleContent(ctx context.Context, articleEnid, render string) (*ArticleContentResult, error) {
	info, err := c.ArticleInfo(ctx, articleEnid)
	if err != nil {
		return nil, err
	}
	token, _ := info["dd_article_token"].(string)
	entitled := truthyValue(info["is_buy"])
	freeTrial := truthyValue(info["is_free_try"])

	if strings.TrimSpace(token) == "" {
		return nil, &APIError{
			StatusCode: 403,
			Method:     "POST",
			Path:       "/pc/bauhinia/pc/article/info",
			Message: "the account is not entitled to this article's body " +
				"(no content token was issued)",
		}
	}

	params := url.Values{
		"token":  {token},
		"sign":   {articleSign},
		"appid":  {articleAppID},
		"is_new": {"1"},
	}
	raw, err := c.requestJSONUnsanitized(ctx, http.MethodGet,
		"/pc/ddarticle/v1/article/get/v2", params, nil,
		c.baseURL+"/course/article?id="+articleEnid)
	if err != nil {
		return nil, err
	}
	payload, _ := raw.(map[string]any)
	body, _ := payload["content"].(string)

	nodes, err := ParseContentNodes(body)
	if err != nil {
		return nil, &APIError{Message: err.Error()}
	}

	result := &ArticleContentResult{
		Entitled: entitled, FreeTrial: freeTrial,
		NodeCount: len(nodes), ContentChars: CountCharacters(nodes),
		Info: map[string]any{
			"is_buy": info["is_buy"], "is_free_try": info["is_free_try"],
			"like_num": info["like_num"], "class_enid": info["class_enid"],
		},
	}
	switch render {
	case "text":
		result.Text = RenderText(nodes)
	case "markdown":
		result.Markdown = RenderMarkdown(nodes)
	default:
		result.Nodes = nodes
	}
	return result, nil
}

func truthyValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return typed != "" && typed != "0"
	default:
		return false
	}
}
