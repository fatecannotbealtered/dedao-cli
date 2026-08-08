package cmd

// Output schemas and runnable examples for every leaf command.
//
// CLI-SPEC §11 forbids stub schemas: each entry names the fields the command
// actually returns and flags the attacker-controllable ones. Dedao echoes
// user-generated content (titles, notes, comments), so almost every payload has
// untrusted fields and an agent must treat them as data, never instructions.
//
// A guard test asserts every leaf command resolves to a non-empty schema and at
// least one example, so `reference` cannot silently decay back into stubs.

type outputSchema struct {
	Shape           string   `json:"shape"`
	Fields          []string `json:"fields"`
	UntrustedFields []string `json:"untrusted_fields"`
}

var outputSchemas = map[string]outputSchema{
	"session_status": {
		Shape:           "object",
		Fields:          []string{"authenticated", "user"},
		UntrustedFields: []string{"user"},
	},
	"logout_result": {
		Shape:  "object",
		Fields: []string{"logged_out"},
	},
	"login_result": {
		Shape:           "object",
		Fields:          []string{"logged_in", "user"},
		UntrustedFields: []string{"user"},
	},
	"library_page": {
		Shape: "object",
		Fields: []string{"list", "is_more", "total", "bottom_tips", "has_single_book",
			"sphere_guide"},
		UntrustedFields: []string{"list", "bottom_tips", "sphere_guide"},
	},
	"library_navigation": {
		Shape:           "object",
		Fields:          []string{"navigation", "index"},
		UntrustedFields: []string{"navigation", "index"},
	},
	// The upstream answers with a single group descriptor rather than a list,
	// and with zero values when the account has no groups. Declared as measured.
	"library_groups": {
		Shape: "object",
		Fields: []string{"id", "name", "count", "uid", "group_ptype", "delete_flag",
			"create_time", "update_time", "mustard_id", "mustard_tips",
			"mustard_create_time", "new_mustard_tips"},
		UntrustedFields: []string{"name"},
	},
	"recent_list": {
		Shape:           "object",
		Fields:          []string{"list", "has_more", "current_time", "timestamp"},
		UntrustedFields: []string{"list"},
	},
	"progress_overview": {
		Shape:           "object",
		Fields:          []string{"index", "last_study"},
		UntrustedFields: []string{"index", "last_study"},
	},
	"search_results": {
		Shape: "object",
		Fields: []string{"list", "total", "page", "size", "type", "is_more",
			"request_id"},
		UntrustedFields: []string{"list"},
	},
	// `search` hits a different backend whose payload is not the scoped-search
	// shape: hits arrive grouped in moduleList, and `content` echoes the query.
	"search_tophits": {
		Shape: "object",
		Fields: []string{"moduleList", "tabList", "typeIdCollection", "classification",
			"content", "isMore", "requestId", "teacherInnerGoods"},
		UntrustedFields: []string{"moduleList", "tabList", "teacherInnerGoods", "content"},
	},
	"search_hot": {
		Shape:           "object",
		Fields:          []string{"hot_tab_list", "recommend_map"},
		UntrustedFields: []string{"hot_tab_list", "recommend_map"},
	},
	"search_suggest": {
		Shape:           "object",
		Fields:          []string{"list"},
		UntrustedFields: []string{"list"},
	},
	"course_detail": {
		Shape: "object",
		Fields: []string{"class_info", "chapter_list", "items", "new_items",
			"flat_article_list", "has_more_flat_article_list", "class_video",
			"class_comment_info", "class_reviews", "class_reviews_count",
			"achievement_detail", "lecturer_dedao_share", "live_info",
			"live_inner_article_info", "is_show_grading", "show_free_tips",
			"user_type", "time_now"},
		UntrustedFields: []string{"class_info", "chapter_list", "items", "new_items",
			"flat_article_list", "class_comment_info", "class_reviews",
			"lecturer_dedao_share"},
	},
	"course_progress": {
		Shape:           "object",
		Fields:          []string{"progress", "learn_count", "last_article"},
		UntrustedFields: []string{"last_article"},
	},
	"caption_track": {
		Shape: "object",
		Fields: []string{"article_enid", "title", "format", "duration", "text",
			"chars", "path"},
		// A caption is the publisher's own transcript.
		UntrustedFields: []string{"title", "text"},
	},
	"ebook_toc": {
		Shape:  "object",
		Fields: []string{"enid", "title", "is_buy", "is_vip_book", "toc", "chapters"},
		// Chapter titles are the publisher's text.
		UntrustedFields: []string{"title", "toc"},
	},
	"ebook_chapter": {
		Shape: "object",
		Fields: []string{"enid", "book_title", "chapter_id", "title", "page_count",
			"text"},
		// The chapter body is publisher content; treat it as data.
		UntrustedFields: []string{"book_title", "title", "text"},
	},
	"saved_media": {
		Shape:  "object",
		Fields: []string{"alias_id", "title", "path", "bytes", "format", "note"},
		// The title is publisher text. The play url and stream key are never
		// returned, so they are not fields at all.
		UntrustedFields: []string{"title"},
	},
	"daily_updates": {
		Shape:  "object",
		Fields: []string{"baseline_created", "update_count", "updates", "checkpoint"},
		// Every course title, headline, and body in the list is publisher text.
		UntrustedFields: []string{"updates"},
	},
	"article_body": {
		Shape: "object",
		Fields: []string{"entitled", "free_trial", "node_count", "content_chars",
			"text", "markdown", "nodes", "info"},
		// The body is publisher content. A live article was observed carrying an
		// imperative sentence addressed at summarizing AIs; it is data.
		UntrustedFields: []string{"text", "markdown", "nodes"},
	},
	"article_list": {
		Shape:           "object",
		Fields:          []string{"article_list", "class_id", "pid", "ptype", "reverse"},
		UntrustedFields: []string{"article_list"},
	},
	"comment_list": {
		Shape:           "object",
		Fields:          []string{"list", "total", "page"},
		UntrustedFields: []string{"list"},
	},
	"note_bundle": {
		Shape:           "object",
		Fields:          []string{"notes", "point"},
		UntrustedFields: []string{"notes", "point"},
	},
	"note_detail": {
		Shape:           "object",
		Fields:          []string{"detail", "comments"},
		UntrustedFields: []string{"detail", "comments"},
	},
	"ebook_detail": {
		Shape:           "object",
		Fields:          []string{"book_info", "price_info", "author_info"},
		UntrustedFields: []string{"book_info", "author_info"},
	},
	"ebook_community": {
		Shape:           "object",
		Fields:          []string{"score", "reviews", "notes"},
		UntrustedFields: []string{"reviews", "notes"},
	},
	"audiobook_detail": {
		Shape:           "object",
		Fields:          []string{"detail", "related"},
		UntrustedFields: []string{"detail", "related"},
	},
	"audiobook_collection": {
		Shape:           "object",
		Fields:          []string{"list", "collection_info"},
		UntrustedFields: []string{"list", "collection_info"},
	},
	"audiobook_agency": {
		Shape:           "object",
		Fields:          []string{"detail", "books"},
		UntrustedFields: []string{"detail", "books"},
	},
	"audiobook_vip": {
		Shape:  "object",
		Fields: []string{"card", "privilege", "disclaimer", "user"},
		// Card titles, privilege copy, and the disclaimer are marketing text
		// Dedao controls; only `user` is the caller's own membership state.
		UntrustedFields: []string{"card", "privilege", "disclaimer"},
	},
	"topic_list": {
		Shape:           "object",
		Fields:          []string{"list", "has_more"},
		UntrustedFields: []string{"list"},
	},
	"topic_detail": {
		Shape:           "object",
		Fields:          []string{"detail", "notes"},
		UntrustedFields: []string{"detail", "notes"},
	},
	"channel_home": {
		Shape:           "object",
		Fields:          []string{"info", "home"},
		UntrustedFields: []string{"info", "home"},
	},
	"channel_topic": {
		Shape:           "object",
		Fields:          []string{"topic_info", "article_list"},
		UntrustedFields: []string{"topic_info", "article_list"},
	},
	"channel_articles": {
		Shape:           "object",
		Fields:          []string{"article_list", "module", "now_time"},
		UntrustedFields: []string{"article_list", "module"},
	},
	"label_list": {
		Shape:           "object",
		Fields:          []string{"list"},
		UntrustedFields: []string{"list"},
	},
	"label_content": {
		Shape:           "object",
		Fields:          []string{"list", "is_more", "page"},
		UntrustedFields: []string{"list"},
	},
	"free_resources": {
		Shape:           "object",
		Fields:          []string{"list"},
		UntrustedFields: []string{"list"},
	},
	"discovery_result": {
		Shape:           "object",
		Fields:          []string{"product_list", "total", "is_more", "request_id"},
		UntrustedFields: []string{"product_list"},
	},
	"live_list": {
		Shape:           "object",
		Fields:          []string{"list"},
		UntrustedFields: []string{"list"},
	},
	"reference_document": {
		Shape: "object",
		Fields: []string{
			"tool", "version", "schema_version", "risk_tier", "minimum_skill_version",
			"release_readiness", "commands", "schemas", "exit_codes", "global_options",
			"security", "output",
		},
	},
	"context_document": {
		Shape: "object",
		Fields: []string{"tool", "version", "schema_version", "env", "account", "config",
			"credentials", "update", "skill"},
	},
	"doctor_report": {
		Shape:  "object",
		Fields: []string{"checks"},
	},
	"update_result": {
		Shape: "object",
		Fields: []string{"status", "stage", "previous_version", "current_version",
			"target_version", "update_available", "install_method", "binary_replaced",
			"signature_status", "signature_verified", "checksum_status",
			"skill_sync_status", "skill_sync_command", "release_url", "notice"},
	},
	"changelog_document": {
		Shape:  "object",
		Fields: []string{"current_version", "since", "entries"},
	},
}

var commandSchemas = map[string]string{
	"status":               "session_status",
	"login":                "login_result",
	"login-resume":         "login_result",
	"logout":               "logout_result",
	"library":              "library_page",
	"library-nav":          "library_navigation",
	"library-groups":       "library_groups",
	"library-group":        "library_page",
	"recent":               "recent_list",
	"progress":             "progress_overview",
	"search":               "search_tophits",
	"search-hot":           "search_hot",
	"search-suggest":       "search_suggest",
	"search-type":          "search_results",
	"course":               "course_detail",
	"articles":             "article_list",
	"article":              "article_body",
	"article-captions":     "caption_track",
	"ebook-chapters":       "ebook_toc",
	"ebook-read":           "ebook_chapter",
	"audiobook-media":      "saved_media",
	"daily":                "daily_updates",
	"comments":             "comment_list",
	"article-notes":        "note_bundle",
	"note":                 "note_detail",
	"ebook":                "ebook_detail",
	"ebook-community":      "ebook_community",
	"audiobook":            "audiobook_detail",
	"audiobook-alias":      "audiobook_detail",
	"audiobook-collection": "audiobook_collection",
	"audiobook-agency":     "audiobook_agency",
	"audiobook-vip":        "audiobook_vip",
	"topics":               "topic_list",
	"topic":                "topic_detail",
	"channel":              "channel_home",
	"channel-topic":        "channel_topic",
	"channel-articles":     "channel_articles",
	"labels":               "label_list",
	"label-content":        "label_content",
	"free":                 "free_resources",
	"discover":             "discovery_result",
	"live":                 "live_list",
	"reference":            "reference_document",
	"context":              "context_document",
	"doctor":               "doctor_report",
	"changelog":            "changelog_document",
	"update":               "update_result",
}

// untrustedFieldsFor resolves the untrusted field list a command's declared
// schema promises, so the runtime marker and `reference` are the same statement.
func untrustedFieldsFor(command string) []string {
	return outputSchemas[commandSchemas[command]].UntrustedFields
}

var commandExamples = map[string][]string{
	"status":               {"dedao-cli status --compact"},
	"login":                {"dedao-cli login --compact"},
	"login-resume":         {"dedao-cli login-resume --compact"},
	"logout":               {"dedao-cli logout"},
	"library":              {"dedao-cli library course --page 1 --page-size 20 --compact"},
	"library-nav":          {"dedao-cli library-nav --compact"},
	"library-groups":       {"dedao-cli library-groups course --compact"},
	"library-group":        {"dedao-cli library-group course <group-id> --compact"},
	"recent":               {"dedao-cli recent --page-size 20 --compact"},
	"progress":             {"dedao-cli progress --compact", "dedao-cli progress <course-enid> --compact"},
	"search":               {"dedao-cli search \"认知\" --tab purchased --compact"},
	"search-hot":           {"dedao-cli search-hot --compact"},
	"search-suggest":       {"dedao-cli search-suggest \"认知\" --compact"},
	"search-type":          {"dedao-cli search-type course \"认知\" --compact"},
	"course":               {"dedao-cli course <course-enid> --compact"},
	"articles":             {"dedao-cli articles <course-enid> --reverse --compact"},
	"article":              {"dedao-cli article <article-enid> --render text --compact", "dedao-cli article <article-enid> --render markdown --format raw"},
	"article-captions":     {"dedao-cli article-captions <article-enid> --compact", "dedao-cli article-captions <article-enid> --format-track vtt --out captions.vtt --compact"},
	"ebook-chapters":       {"dedao-cli ebook-chapters <ebook-enid> --compact"},
	"ebook-read":           {"dedao-cli ebook-read <ebook-enid> --chapter Chapter_1_1 --compact", "dedao-cli ebook-read <ebook-enid> --title 序言 --text-only"},
	"audiobook-media":      {"dedao-cli audiobook-media <topic-id-or-alias-id> --compact"},
	"daily":                {"dedao-cli daily --compact", "dedao-cli daily --with-content --compact"},
	"comments":             {"dedao-cli comments <article-enid> --compact", "dedao-cli comments <article-enid> --mine --compact"},
	"article-notes":        {"dedao-cli article-notes <article-enid> --compact"},
	"note":                 {"dedao-cli note <note-id> --with-comments --compact"},
	"ebook":                {"dedao-cli ebook <ebook-enid> --compact"},
	"ebook-community":      {"dedao-cli ebook-community <ebook-enid> --with-notes --compact"},
	"audiobook":            {"dedao-cli audiobook <topic-id> --with-related --compact"},
	"audiobook-alias":      {"dedao-cli audiobook-alias <alias-id> --compact"},
	"audiobook-collection": {"dedao-cli audiobook-collection <collection-enid> --compact"},
	"audiobook-agency":     {"dedao-cli audiobook-agency <agency-id> --with-books --compact"},
	"audiobook-vip":        {"dedao-cli audiobook-vip --compact"},
	"topics":               {"dedao-cli topics --page 0 --page-size 20 --compact"},
	"topic":                {"dedao-cli topic <topic-id> --with-notes --compact"},
	"channel":              {"dedao-cli channel --compact"},
	"channel-topic":        {"dedao-cli channel-topic <product-id> --compact"},
	"channel-articles":     {"dedao-cli channel-articles --count 20 --compact"},
	"labels":               {"dedao-cli labels 4 --compact"},
	"label-content":        {"dedao-cli label-content <label-enid> 4 66 --compact"},
	"free":                 {"dedao-cli free --compact"},
	"discover":             {"dedao-cli discover 4 --compact", "dedao-cli discover 4 --filters --compact"},
	"live":                 {"dedao-cli live --compact", "dedao-cli live --subscribed --compact"},
	"reference":            {"dedao-cli reference --compact"},
	"context":              {"dedao-cli context --compact"},
	"doctor":               {"dedao-cli doctor --compact"},
	"update":               {"dedao-cli update --check --compact", "dedao-cli update --compact"},
	"changelog":            {"dedao-cli changelog --since 0.1.0 --compact"},
}
