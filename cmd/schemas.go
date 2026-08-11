package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

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
		Fields:          []string{"authenticated", "user", "getnote"},
		UntrustedFields: []string{"user"},
	},
	"logout_result": {
		Shape: "object",
		Fields: []string{"preview", "confirm_token", "expires_at", "logged_out", "previously_configured",
			"getnote_stored_credentials_removed", "getnote_credentials_kept",
			"getnote_environment_credentials_active"},
	},
	"login_result": {
		Shape:           "object",
		Fields:          []string{"logged_in", "user", "getnote", "already_signed_in"},
		UntrustedFields: []string{"user"},
	},
	"library_page": {
		Shape: "object",
		Fields: []string{"list", "count", "has_more", "is_more", "total", "bottom_tips", "has_single_book",
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
			"mustard_create_time", "new_mustard_tips", "create_label", "update_label",
			"mustard_create_label"},
		UntrustedFields: []string{"name"},
	},
	"recent_list": {
		Shape:           "object",
		Fields:          []string{"list", "count", "has_more", "current_time", "timestamp", "current_label", "timestamp_label"},
		UntrustedFields: []string{"list"},
	},
	"progress_overview": {
		Shape:           "object",
		Fields:          []string{"index", "last_study"},
		UntrustedFields: []string{"index", "last_study"},
	},
	"search_results": {
		Shape: "object",
		Fields: []string{"list", "count", "has_more", "total", "page", "size", "type", "is_more",
			"request_id"},
		UntrustedFields: []string{"list"},
	},
	// `search` hits a different backend whose payload is not the scoped-search
	// shape: hits arrive grouped in module_list, and `content` echoes the query.
	"search_tophits": {
		Shape: "object",
		Fields: []string{"module_list", "count", "has_more", "tab_list", "type_id_collection", "classification",
			"content", "is_more", "request_id", "teacher_inner_goods"},
		UntrustedFields: []string{"module_list", "tab_list", "teacher_inner_goods", "content"},
	},
	"search_hot": {
		Shape:           "object",
		Fields:          []string{"hot_tab_list", "count", "has_more", "recommend_map"},
		UntrustedFields: []string{"hot_tab_list", "recommend_map"},
	},
	"search_suggest": {
		Shape:           "object",
		Fields:          []string{"list", "count", "has_more"},
		UntrustedFields: []string{"list"},
	},
	"course_detail": {
		Shape: "object",
		Fields: []string{"class_info", "chapter_list", "items", "new_items",
			"flat_article_list", "has_more_flat_article_list", "class_video",
			"class_comment_info", "class_reviews", "class_reviews_count",
			"achievement_detail", "lecturer_dedao_share", "live_info",
			"live_inner_article_info", "is_show_grading", "show_free_tips",
			"user_type", "time_now", "count", "intro_article", "used_coupon_info"},
		UntrustedFields: []string{"class_info", "chapter_list", "items", "new_items",
			"flat_article_list", "class_comment_info", "class_reviews",
			"lecturer_dedao_share", "intro_article"},
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
		Fields:          []string{"article_list", "count", "has_more", "class_id", "pid", "ptype", "reverse"},
		UntrustedFields: []string{"article_list"},
	},
	// Declared as measured against the live service. `is_more` is the service's
	// own flag and `article` echoes what was asked about; neither was declared
	// before. `has_more` stays because `normalizePagination` adds it whenever the
	// page boundary is known, so it is this tool's field rather than the
	// service's. `page` was declared once and never arrives from either.
	"comment_list": {
		Shape:           "object",
		Fields:          []string{"list", "count", "has_more", "is_more", "total", "article"},
		UntrustedFields: []string{"list", "article"},
	},
	// `notes` is the account's own writing; `article_point` is Dedao's summary of
	// the article and arrives whether or not the account highlighted anything.
	// `account_wrote_point` is the upstream flag that tells the two apart.
	"note_bundle": {
		Shape:           "object",
		Fields:          []string{"notes", "article_point", "account_wrote_point"},
		UntrustedFields: []string{"notes", "article_point"},
	},
	"note_detail": {
		Shape:           "object",
		Fields:          []string{"detail", "comments"},
		UntrustedFields: []string{"detail", "comments"},
	},
	// Declared as measured against the live service. The contract described a
	// nested `book_info`/`price_info` shape; the service answers with a flat
	// record of the fields below, of which only `author_info` overlapped. An
	// agent reading `book_info` would have found nothing, every time.
	"ebook_detail": {
		Shape: "object",
		Fields: []string{
			"activity_status", "add_studylist_dd_url", "author_info", "author_list",
			"b_overseas_purchase", "b_special_price", "book_author", "book_intro", "book_type",
			"can_trial_read", "catalog_list", "classify_id", "classify_name", "copywriting", "count",
			"cover", "current_price", "douban_score", "enid", "experiment", "id", "is_buy", "is_buyed",
			"is_on_bookshelf", "is_reserve", "is_trial", "is_tts_switch", "is_vip_book", "isbn",
			"log_id", "log_type", "not_support_web", "not_support_web_menu", "operating_title",
			"original_price", "other_share_summary", "other_share_title", "press", "price",
			"product_score", "publish_time", "rank_name", "rank_num", "read_label", "read_number",
			"status", "style", "title", "trial_read_proportion", "video_detail", "with_video",
		},
		UntrustedFields: []string{
			"author_info", "author_list", "book_author", "book_intro", "catalog_list", "classify_name",
			"copywriting", "experiment", "operating_title", "other_share_summary", "other_share_title",
			"press", "rank_name", "read_label", "title", "video_detail",
		},
	},
	"ebook_community": {
		Shape:           "object",
		Fields:          []string{"score", "reviews", "notes"},
		UntrustedFields: []string{"reviews", "notes"},
	},
	// `quality` arrives alongside the record and was undeclared until a live read
	// found it.
	"audiobook_detail": {
		Shape:           "object",
		Fields:          []string{"detail", "related", "quality"},
		UntrustedFields: []string{"detail", "related"},
	},
	"audiobook_collection": {
		Shape:           "object",
		Fields:          []string{"list", "count", "has_more", "collection_info"},
		UntrustedFields: []string{"list", "collection_info"},
	},
	// Declared as measured. The service answers with the agency record itself,
	// flat -- not the `detail`/`books` envelope declared here before, of which
	// neither field ever arrives.
	"audiobook_agency": {
		Shape: "object",
		Fields: []string{
			"book_count", "created_at", "id", "id_str", "intro", "name", "one_sentence_summary",
			"status", "updated_at",
		},
		UntrustedFields: []string{"intro", "name", "one_sentence_summary"},
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
		Fields:          []string{"list", "count", "has_more"},
		UntrustedFields: []string{"list"},
	},
	// Declared as measured. Like the agency record, the topic arrives flat; the
	// `detail`/`notes` envelope declared here before never appears.
	"topic_detail": {
		Shape: "object",
		Fields: []string{
			"create_time", "ddurl", "has_new_notes", "img", "intro", "last_notes_content",
			"last_notes_id", "last_notes_id_hazy", "last_notes_uid", "last_notes_uid_hazy",
			"last_update_time", "log_id", "log_type", "name", "notes_count", "notes_topic_id",
			"presenters", "resources", "share_url", "state", "tag", "top_area", "topic_id_hazy",
			"topic_id_str", "topmost", "update_time", "user_state", "view_count",
		},
		UntrustedFields: []string{
			"ddurl", "img", "intro", "last_notes_content", "name", "presenters", "resources",
			"share_url", "tag", "top_area",
		},
	},
	"channel_home": {
		Shape:           "object",
		Fields:          []string{"info", "home"},
		UntrustedFields: []string{"info", "home"},
	},
	"channel_topic": {
		Shape:           "object",
		Fields:          []string{"topic_info", "article_list", "count", "has_more"},
		UntrustedFields: []string{"topic_info", "article_list"},
	},
	"channel_articles": {
		Shape:           "object",
		Fields:          []string{"article_list", "count", "has_more", "module", "now_time", "now_label"},
		UntrustedFields: []string{"article_list", "module"},
	},
	"label_list": {
		Shape:           "object",
		Fields:          []string{"list", "count", "has_more"},
		UntrustedFields: []string{"list"},
	},
	// Declared as measured against the live service. The list of content was
	// declared as `list` and arrives as `product_list`; `page` was declared and
	// arrives as `page_id` and `page_size`; and the navigation, the current
	// label, and the request id were not declared at all. `count` and `has_more`
	// come from `normalizePagination` once `product_list` carries items, so they
	// appear only when it does.
	"label_content": {
		Shape: "object",
		Fields: []string{"product_list", "navigation_list", "current_enid", "page_id",
			"page_size", "request_id", "count", "has_more", "is_more"},
		UntrustedFields: []string{"product_list", "navigation_list"},
	},
	"free_resources": {
		Shape:           "object",
		Fields:          []string{"list", "count", "has_more"},
		UntrustedFields: []string{"list"},
	},
	"discovery_result": {
		Shape:           "object",
		Fields:          []string{"product_list", "count", "has_more", "total", "is_more", "request_id"},
		UntrustedFields: []string{"product_list"},
	},
	"live_list": {
		Shape:           "object",
		Fields:          []string{"list", "count", "has_more"},
		UntrustedFields: []string{"list"},
	},
	"reference_document": {
		Shape: "object",
		Fields: []string{
			"tool", "version", "schema_version", "risk_tier", "minimum_skill_version",
			"release_readiness", "commands", "schemas", "exit_codes", "error_codes", "global_options",
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
			"target_version", "update_available", "install_method", "command", "binary_replaced",
			"signature_status", "signature_verified", "checksum_status",
			"skill_sync_status", "skill_sync_command", "release_url", "notice"},
	},
	"changelog_document": {
		Shape:  "object",
		Fields: []string{"current_version", "since", "entries"},
	},
	"getnote_auth_result": {
		Shape: "object",
		Fields: []string{"configured", "stored", "stored_credentials_removed", "environment_credentials_active",
			"api_key_configured", "client_id_configured", "api_key_source", "client_id_source", "state_dir",
			"preview", "confirm_token", "expires_at"},
	},
	"getnote_mutation": {
		Shape:           "object",
		Fields:          []string{"preview", "confirm_token", "expires_at", "result"},
		UntrustedFields: []string{"result"},
	},
	"getnote_task": {
		Shape:           "object",
		Fields:          []string{"task_id", "status", "note_id", "msg", "error_msg", "create_time", "update_time"},
		UntrustedFields: []string{"msg", "error_msg"},
	},
	"getnote_notes": {
		Shape:           "object",
		Fields:          []string{"items", "count", "next_cursor", "has_more", "total", "truncated"},
		UntrustedFields: []string{"items"},
	},
	"getnote_note": {
		Shape:           "object",
		Fields:          []string{"note"},
		UntrustedFields: []string{"note"},
	},
	"getnote_tags": {
		Shape:           "object",
		Fields:          []string{"note_id", "tags"},
		UntrustedFields: []string{"tags"},
	},
	"getnote_search": {
		Shape:           "object",
		Fields:          []string{"items", "count", "has_more", "truncated"},
		UntrustedFields: []string{"items"},
	},
	"getnote_kbs": {
		Shape:           "object",
		Fields:          []string{"items", "count", "has_more", "total", "page", "next_page", "truncated"},
		UntrustedFields: []string{"items"},
	},
	"getnote_kb_notes": {
		Shape:           "object",
		Fields:          []string{"items", "count", "has_more", "total", "page", "next_page", "truncated"},
		UntrustedFields: []string{"items"},
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
	"getnote auth login":   "getnote_auth_result",
	"getnote auth status":  "getnote_auth_result",
	"getnote auth logout":  "getnote_auth_result",
	"getnote save":         "getnote_mutation",
	"getnote task":         "getnote_task",
	"getnote notes":        "getnote_notes",
	"getnote note get":     "getnote_note",
	"getnote note update":  "getnote_mutation",
	"getnote note delete":  "getnote_mutation",
	"getnote note share":   "getnote_mutation",
	"getnote search":       "getnote_search",
	"getnote tag add":      "getnote_mutation",
	"getnote tag remove":   "getnote_mutation",
	"getnote tag list":     "getnote_tags",
	"getnote kbs":          "getnote_kbs",
	"getnote kb notes":     "getnote_kb_notes",
	"getnote kb create":    "getnote_mutation",
	"getnote kb add":       "getnote_mutation",
	"getnote kb remove":    "getnote_mutation",
}

// untrustedFieldsFor resolves the untrusted field list a command's declared
// schema promises, so the runtime marker and `reference` are the same statement.
func untrustedFieldsFor(command string) []string {
	return outputSchemas[commandSchemas[command]].UntrustedFields
}

func commandSchemaKey(cmdPath string) string {
	path := strings.TrimPrefix(strings.TrimSpace(cmdPath), "dedao-cli ")
	return commandSchemas[path]
}

func untrustedFieldsForCommand(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	return outputSchemas[commandSchemaKey(cmd.CommandPath())].UntrustedFields
}

func commandExamplesFor(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	path := strings.TrimPrefix(cmd.CommandPath(), "dedao-cli ")
	if examples, ok := commandExamples[path]; ok {
		return examples
	}
	return commandExamples[cmd.Name()]
}

var commandExamples = map[string][]string{
	"status":               {"dedao-cli status --compact"},
	"login":                {"dedao-cli login --compact"},
	"login-resume":         {"dedao-cli login-resume --compact"},
	"logout":               {"dedao-cli logout --dry-run --compact", "dedao-cli logout --confirm <confirm_token> --compact"},
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
	"changelog":            {"dedao-cli changelog --since 1.0.0 --compact"},
	"getnote auth login":   {"Get-Content api-key.txt | dedao-cli getnote auth login --api-key-stdin --client-id <client-id> --compact"},
	"getnote auth status":  {"dedao-cli getnote auth status --compact"},
	"getnote auth logout":  {"dedao-cli getnote auth logout --dry-run --compact", "dedao-cli getnote auth logout --confirm <confirm-token> --compact"},
	"getnote save":         {"dedao-cli getnote save --content \"读书笔记\" --dry-run --compact", "dedao-cli getnote save --content \"读书笔记\" --confirm <confirm-token> --compact"},
	"getnote task":         {"dedao-cli getnote task <task-id> --compact"},
	"getnote notes":        {"dedao-cli getnote notes --limit 20 --compact"},
	"getnote note get":     {"dedao-cli getnote note get <note-id> --compact"},
	"getnote note update":  {"dedao-cli getnote note update <note-id> --title \"新标题\" --dry-run --compact", "dedao-cli getnote note update <note-id> --title \"新标题\" --confirm <confirm-token> --compact"},
	"getnote note delete":  {"dedao-cli getnote note delete <note-id> --dry-run --compact", "dedao-cli getnote note delete <note-id> --confirm <confirm-token> --compact"},
	"getnote note share":   {"dedao-cli getnote note share <note-id> --dry-run --compact", "dedao-cli getnote note share <note-id> --confirm <confirm-token> --compact"},
	"getnote search":       {"dedao-cli getnote search \"认知\" --top-k 10 --compact"},
	"getnote tag add":      {"dedao-cli getnote tag add <note-id> --tag 阅读 --dry-run --compact", "dedao-cli getnote tag add <note-id> --tag 阅读 --confirm <confirm-token> --compact"},
	"getnote tag remove":   {"dedao-cli getnote tag remove <note-id> <tag-id> --dry-run --compact", "dedao-cli getnote tag remove <note-id> <tag-id> --confirm <confirm-token> --compact"},
	"getnote tag list":     {"dedao-cli getnote tag list <note-id> --compact"},
	"getnote kbs":          {"dedao-cli getnote kbs --page 1 --compact"},
	"getnote kb notes":     {"dedao-cli getnote kb notes <topic-id> --page 1 --compact"},
	"getnote kb create":    {"dedao-cli getnote kb create --name \"阅读\" --dry-run --compact", "dedao-cli getnote kb create --name \"阅读\" --confirm <confirm-token> --compact"},
	"getnote kb add":       {"dedao-cli getnote kb add --topic-id <topic-id> --note-id <note-id> --dry-run --compact", "dedao-cli getnote kb add --topic-id <topic-id> --note-id <note-id> --confirm <confirm-token> --compact"},
	"getnote kb remove":    {"dedao-cli getnote kb remove --topic-id <topic-id> --note-id <note-id> --dry-run --compact", "dedao-cli getnote kb remove --topic-id <topic-id> --note-id <note-id> --confirm <confirm-token> --compact"},
}
