package cmd

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/fatecannotbealtered/dedao-cli/internal/dedao"
	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/spf13/cobra"
)

// run is the single seam every query command funnels through: build a client,
// call one API method, print the success envelope. Errors bubble up to
// asCLIError so status -> code -> exit stays owned by one place.
func (a *application) run(cmd *cobra.Command, call func(context.Context, *dedao.Client) (any, error)) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	data, err := call(cmd.Context(), client)
	if err != nil {
		return err
	}
	return a.success(data)
}

func libraryCategory(kind string) (string, error) {
	category, ok := dedao.LibraryCategories[kind]
	if !ok {
		return "", output.NewError("E_VALIDATION",
			"kind must be one of: audiobook, course, ebook",
			map[string]any{"received": kind})
	}
	return category, nil
}

func (a *application) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether a Dedao session is loaded",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			// A query that successfully reports "not logged in" is a success,
			// not a failure: ok stays true and the caller reads the field.
			data := map[string]any{"authenticated": client.Authenticated()}
			if client.Authenticated() {
				user, err := client.UserInfo(cmd.Context())
				if err != nil {
					return err
				}
				data["user"] = user
			}
			return a.success(data)
		},
	}
}

func (a *application) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Discard the stored Dedao session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			if err := client.Logout(); err != nil {
				return output.WrapError("E_IO", "could not remove the stored session", err, nil)
			}
			return a.success(map[string]any{"logged_out": true})
		},
	}
}

// queryCommands returns every read command. They are assembled in one place so
// `reference` and the FCC guard enumerate exactly what is registered.
func (a *application) queryCommands() []*cobra.Command {
	var commands []*cobra.Command

	// --- library ---------------------------------------------------------
	var page, pageSize int
	library := &cobra.Command{
		Use:   "library <course|ebook|audiobook>",
		Short: "List content the account owns in one category",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			category, err := libraryCategory(args[0])
			if err != nil {
				return err
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.Library(ctx, category, page, pageSize)
			})
		},
	}
	library.Flags().IntVar(&page, "page", 1, "Page number")
	library.Flags().IntVar(&pageSize, "page-size", 6, "Items per page")
	commands = append(commands, library)

	commands = append(commands, &cobra.Command{
		Use:   "library-nav",
		Short: "Show library navigation and index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				navigation, err := c.LibraryNavigation(ctx)
				if err != nil {
					return nil, err
				}
				index, err := c.LibraryIndex(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"navigation": navigation, "index": index}, nil
			})
		},
	})

	commands = append(commands, &cobra.Command{
		Use:   "library-groups <course|ebook|audiobook>",
		Short: "List the account's groups in one library category",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			category, err := libraryCategory(args[0])
			if err != nil {
				return err
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.LibraryGroups(ctx, category)
			})
		},
	})

	var groupPage, groupPageSize int
	var groupOrder, groupFilter string
	libraryGroup := &cobra.Command{
		Use:   "library-group <course|ebook|audiobook> <group-id>",
		Short: "List content inside one library group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			category, err := libraryCategory(args[0])
			if err != nil {
				return err
			}
			groupID, err := strconv.Atoi(args[1])
			if err != nil {
				return output.NewError("E_VALIDATION", "group-id must be an integer",
					map[string]any{"received": args[1]})
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.LibraryGroup(ctx, category, groupID, groupPage, groupPageSize, groupOrder, groupFilter)
			})
		},
	}
	libraryGroup.Flags().IntVar(&groupPage, "page", 1, "Page number")
	libraryGroup.Flags().IntVar(&groupPageSize, "page-size", 20, "Items per page")
	libraryGroup.Flags().StringVar(&groupOrder, "order", "study", "Sort order")
	libraryGroup.Flags().StringVar(&groupFilter, "filter", "all", "Completion filter")
	commands = append(commands, libraryGroup)

	// --- recent and progress ---------------------------------------------
	var recentPageSize, recentMaxID int
	recent := &cobra.Command{
		Use:   "recent",
		Short: "List recently studied content",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.Recent(ctx, recentPageSize, recentMaxID)
			})
		},
	}
	recent.Flags().IntVar(&recentPageSize, "page-size", 20, "Items per page")
	recent.Flags().IntVar(&recentMaxID, "max-id", 0, "Pagination cursor")
	commands = append(commands, recent)

	var progressReverse bool
	var progressCount int
	var progressResultType string
	progress := &cobra.Command{
		Use:   "progress [course-enid]",
		Short: "Show study progress, overall or for one course",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				if len(args) == 1 {
					return c.CourseProgress(ctx, args[0], progressReverse)
				}
				index, err := c.RecentIndex(ctx)
				if err != nil {
					return nil, err
				}
				last, err := c.LastStudy(ctx, progressCount, progressResultType)
				if err != nil {
					return nil, err
				}
				return map[string]any{"index": index, "last_study": last}, nil
			})
		},
	}
	progress.Flags().BoolVar(&progressReverse, "reverse", false, "Reverse ordering")
	progress.Flags().IntVar(&progressCount, "count", 10, "Items to return")
	progress.Flags().StringVar(&progressResultType, "result-type", "2,66,310,316", "Product type filter")
	commands = append(commands, progress)

	// --- search ----------------------------------------------------------
	var searchTab, searchRequestID string
	var searchPage, searchPageSize int
	search := &cobra.Command{
		Use:   "search <query>",
		Short: "Search owned content by default, or another scope with --tab",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tabType, ok := dedao.SearchTabs[searchTab]
			if !ok {
				keys := make([]string, 0, len(dedao.SearchTabs))
				for key := range dedao.SearchTabs {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				return output.NewError("E_VALIDATION",
					"--tab must be one of: "+strings.Join(keys, ", "),
					map[string]any{"received": searchTab})
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.Search(ctx, args[0], tabType, searchPage, searchPageSize, searchRequestID)
			})
		},
	}
	search.Flags().StringVar(&searchTab, "tab", "purchased", "Search scope")
	search.Flags().IntVar(&searchPage, "page", 1, "Page number")
	search.Flags().IntVar(&searchPageSize, "page-size", 10, "Items per page")
	search.Flags().StringVar(&searchRequestID, "request-id", "", "Upstream request correlation id")
	commands = append(commands, search)

	commands = append(commands, &cobra.Command{
		Use:   "search-hot",
		Short: "List trending search terms",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.SearchHot(ctx, "")
			})
		},
	})

	var suggestType int
	suggest := &cobra.Command{
		Use:   "search-suggest <query>",
		Short: "List search suggestions for a partial query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.SearchSuggest(ctx, args[0], suggestType)
			})
		},
	}
	suggest.Flags().IntVar(&suggestType, "search-type", 0, "Suggestion scope")
	commands = append(commands, suggest)

	var typePage, typePageSize, typeHighlight int
	searchType := &cobra.Command{
		Use:   "search-type <course|ebook-chapter|audio|topic> <query>",
		Short: "Search one specialized index",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := dedao.SpecialSearchPaths[args[0]]; !ok {
				return output.NewError("E_VALIDATION",
					"kind must be one of: audio, course, ebook-chapter, topic",
					map[string]any{"received": args[0]})
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.SearchSpecial(ctx, args[0], args[1], typePage, typePageSize, "", typeHighlight, nil)
			})
		},
	}
	searchType.Flags().IntVar(&typePage, "page", 1, "Page number")
	searchType.Flags().IntVar(&typePageSize, "page-size", 10, "Items per page")
	searchType.Flags().IntVar(&typeHighlight, "highlight-count", 3, "Highlighted snippets per hit")
	commands = append(commands, searchType)

	// --- course and articles ---------------------------------------------
	commands = append(commands, &cobra.Command{
		Use:   "course <course-enid>",
		Short: "Show one course's metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.CourseInfo(ctx, args[0])
			})
		},
	})

	var articlesCount, articlesMaxID, articlesMaxOrder int
	var articlesReverse bool
	articles := &cobra.Command{
		Use:   "articles <course-enid>",
		Short: "List the owned articles in a course",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.CourseArticles(ctx, args[0], articlesCount, articlesMaxID, articlesMaxOrder, articlesReverse)
			})
		},
	}
	articles.Flags().IntVar(&articlesCount, "count", 30, "Items to return")
	articles.Flags().IntVar(&articlesMaxID, "max-id", 0, "Pagination cursor")
	articles.Flags().IntVar(&articlesMaxOrder, "max-order-num", 0, "Order-number cursor")
	articles.Flags().BoolVar(&articlesReverse, "reverse", false, "Oldest first")
	commands = append(commands, articles)

	var commentsMine bool
	var commentsPage, commentsPageSize int
	var commentsSort string
	comments := &cobra.Command{
		Use:   "comments <article-enid>",
		Short: "List an article's comments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				if commentsMine {
					return c.ArticleUserComments(ctx, args[0])
				}
				return c.ArticleComments(ctx, args[0], commentsPage, commentsPageSize, commentsSort)
			})
		},
	}
	comments.Flags().BoolVar(&commentsMine, "mine", false, "Only the account's own comments")
	comments.Flags().IntVar(&commentsPage, "page", 1, "Page number")
	comments.Flags().IntVar(&commentsPageSize, "page-size", 20, "Items per page")
	comments.Flags().StringVar(&commentsSort, "sort", "like", "Sort order")
	commands = append(commands, comments)

	var notesSourceID string
	var notesProductType int
	articleNotes := &cobra.Command{
		Use:   "article-notes <article-enid>",
		Short: "List the account's notes on an article",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				notes, err := c.ArticleNotes(ctx, args[0], notesSourceID)
				if err != nil {
					return nil, err
				}
				point, err := c.ArticleNotePoint(ctx, args[0], notesProductType)
				if err != nil {
					return nil, err
				}
				return map[string]any{"notes": notes, "point": point}, nil
			})
		},
	}
	articleNotes.Flags().StringVar(&notesSourceID, "source-id", "", "Upstream source id")
	articleNotes.Flags().IntVar(&notesProductType, "product-type", 66, "Product type")
	commands = append(commands, articleNotes)

	var noteWithComments bool
	var noteCount int
	var noteCursor string
	note := &cobra.Command{
		Use:   "note <note-id>",
		Short: "Show one note, optionally with its replies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				detail, err := c.NoteDetail(ctx, args[0], 2)
				if err != nil {
					return nil, err
				}
				result := map[string]any{"detail": detail}
				if noteWithComments {
					replies, err := c.NoteComments(ctx, args[0], noteCount, noteCursor)
					if err != nil {
						return nil, err
					}
					result["comments"] = replies
				}
				return result, nil
			})
		},
	}
	note.Flags().BoolVar(&noteWithComments, "with-comments", false, "Include replies")
	note.Flags().IntVar(&noteCount, "count", 20, "Replies to return")
	note.Flags().StringVar(&noteCursor, "cursor", "", "Reply pagination cursor")
	commands = append(commands, note)

	// --- ebook -----------------------------------------------------------
	commands = append(commands, &cobra.Command{
		Use:   "ebook <ebook-enid>",
		Short: "Show one ebook's metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.EbookInfo(ctx, args[0])
			})
		},
	})

	var reviewPage, reviewPageSize int
	var reviewSort string
	var withNotes bool
	ebookCommunity := &cobra.Command{
		Use:   "ebook-community <ebook-enid>",
		Short: "Show an ebook's score and reviews",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				score, err := c.EbookScore(ctx, args[0])
				if err != nil {
					return nil, err
				}
				reviews, err := c.EbookReviews(ctx, args[0], reviewPage, reviewPageSize, reviewSort)
				if err != nil {
					return nil, err
				}
				result := map[string]any{"score": score, "reviews": reviews}
				if withNotes {
					notes, err := c.EbookNotes(ctx, args[0], "")
					if err != nil {
						return nil, err
					}
					result["notes"] = notes
				}
				return result, nil
			})
		},
	}
	ebookCommunity.Flags().IntVar(&reviewPage, "page", 1, "Page number")
	ebookCommunity.Flags().IntVar(&reviewPageSize, "page-size", 20, "Items per page")
	ebookCommunity.Flags().StringVar(&reviewSort, "sort", "hot", "Sort order")
	ebookCommunity.Flags().BoolVar(&withNotes, "with-notes", false, "Include the account's notes")
	commands = append(commands, ebookCommunity)

	// --- audiobook -------------------------------------------------------
	var withRelated bool
	audiobook := &cobra.Command{
		Use:   "audiobook <topic-id>",
		Short: "Show one audiobook's metadata (safe-field allowlist)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				info, err := c.AudiobookInfo(ctx, args[0])
				if err != nil {
					return nil, err
				}
				if !withRelated {
					return info, nil
				}
				related, err := c.AudiobookRelated(ctx, args[0])
				if err != nil {
					return nil, err
				}
				return map[string]any{"detail": info, "related": related}, nil
			})
		},
	}
	audiobook.Flags().BoolVar(&withRelated, "with-related", false, "Include related books")
	commands = append(commands, audiobook)

	commands = append(commands, &cobra.Command{
		Use:   "audiobook-alias <alias-id>",
		Short: "Resolve an audiobook by alias id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.AudiobookAlias(ctx, args[0])
			})
		},
	})

	commands = append(commands, &cobra.Command{
		Use:   "audiobook-collection <collection-enid>",
		Short: "List an audiobook collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.AudiobookCollection(ctx, args[0])
			})
		},
	})

	var agencyWithBooks bool
	var agencyCount int
	agency := &cobra.Command{
		Use:   "audiobook-agency <agency-id>",
		Short: "Show an audiobook agency, optionally with its books",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				detail, err := c.AudiobookAgency(ctx, args[0])
				if err != nil {
					return nil, err
				}
				if !agencyWithBooks {
					return detail, nil
				}
				books, err := c.AudiobookAgencyBooks(ctx, args[0], agencyCount, 0, 0)
				if err != nil {
					return nil, err
				}
				return map[string]any{"detail": detail, "books": books}, nil
			})
		},
	}
	agency.Flags().BoolVar(&agencyWithBooks, "with-books", false, "Include the agency's books")
	agency.Flags().IntVar(&agencyCount, "count", 20, "Books to return")
	commands = append(commands, agency)

	commands = append(commands, &cobra.Command{
		Use:   "audiobook-vip",
		Short: "Show the account's audiobook VIP status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.AudiobookVIP(ctx)
			})
		},
	})

	// --- topics and channel ----------------------------------------------
	var topicsPage, topicsPageSize int
	topics := &cobra.Command{
		Use:   "topics",
		Short: "List knowledge-city topics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.Topics(ctx, topicsPage, topicsPageSize)
			})
		},
	}
	topics.Flags().IntVar(&topicsPage, "page", 0, "Page number")
	topics.Flags().IntVar(&topicsPageSize, "page-size", 20, "Items per page")
	commands = append(commands, topics)

	var topicWithNotes, topicElected bool
	var topicPage, topicPageSize int
	topic := &cobra.Command{
		Use:   "topic <topic-id>",
		Short: "Show one topic, optionally with its notes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				detail, err := c.TopicDetail(ctx, args[0])
				if err != nil {
					return nil, err
				}
				if !topicWithNotes {
					return detail, nil
				}
				notes, err := c.TopicNotes(ctx, args[0], topicPage, topicPageSize, topicElected)
				if err != nil {
					return nil, err
				}
				return map[string]any{"detail": detail, "notes": notes}, nil
			})
		},
	}
	topic.Flags().BoolVar(&topicWithNotes, "with-notes", false, "Include topic notes")
	topic.Flags().BoolVar(&topicElected, "elected-only", false, "Only elected notes")
	topic.Flags().IntVar(&topicPage, "page", 0, "Note page number")
	topic.Flags().IntVar(&topicPageSize, "page-size", 20, "Notes per page")
	commands = append(commands, topic)

	var channelID int
	var channelShowAll bool
	channel := &cobra.Command{
		Use:   "channel",
		Short: "Show the AI learning-circle channel home",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				info, err := c.ChannelInfo(ctx, channelID)
				if err != nil {
					return nil, err
				}
				home, err := c.ChannelHome(ctx, channelID, channelShowAll)
				if err != nil {
					return nil, err
				}
				return map[string]any{"info": info, "home": home}, nil
			})
		},
	}
	channel.Flags().IntVar(&channelID, "channel-id", 1000, "Channel id")
	channel.Flags().BoolVar(&channelShowAll, "show-all", false, "Include all sections")
	commands = append(commands, channel)

	var topicProductType int
	channelTopic := &cobra.Command{
		Use:   "channel-topic <product-id>",
		Short: "Show one learning-circle topic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			productID, err := strconv.Atoi(args[0])
			if err != nil {
				return output.NewError("E_VALIDATION", "product-id must be an integer",
					map[string]any{"received": args[0]})
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.ChannelTopic(ctx, productID, topicProductType)
			})
		},
	}
	channelTopic.Flags().IntVar(&topicProductType, "product-type", 4006, "Product type")
	commands = append(commands, channelTopic)

	var articlesChannelID, articlesChannelCount int
	channelArticles := &cobra.Command{
		Use:   "channel-articles",
		Short: "List learning-circle selected articles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.ChannelArticles(ctx, articlesChannelID, articlesChannelCount)
			})
		},
	}
	channelArticles.Flags().IntVar(&articlesChannelID, "channel-id", 1000, "Channel id")
	channelArticles.Flags().IntVar(&articlesChannelCount, "count", 20, "Articles to return")
	commands = append(commands, channelArticles)

	// --- discovery -------------------------------------------------------
	var labelCount int
	labels := &cobra.Command{
		Use:   "labels <nav-type>",
		Short: "List discovery labels for a navigation type",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			navType, err := strconv.Atoi(args[0])
			if err != nil {
				return output.NewError("E_VALIDATION", "nav-type must be an integer",
					map[string]any{"received": args[0]})
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.Labels(ctx, navType, labelCount)
			})
		},
	}
	labels.Flags().IntVar(&labelCount, "label-count", -1, "Labels to return")
	commands = append(commands, labels)

	var labelPage, labelPageSize int
	labelContent := &cobra.Command{
		Use:   "label-content <label-enid> <nav-type> <result-type>",
		Short: "List content under one discovery label",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			navType, err := strconv.Atoi(args[1])
			if err != nil {
				return output.NewError("E_VALIDATION", "nav-type must be an integer",
					map[string]any{"received": args[1]})
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.LabelContent(ctx, args[0], navType, args[2], labelPage, labelPageSize, "")
			})
		},
	}
	labelContent.Flags().IntVar(&labelPage, "page", 1, "Page number")
	labelContent.Flags().IntVar(&labelPageSize, "page-size", 20, "Items per page")
	commands = append(commands, labelContent)

	commands = append(commands, &cobra.Command{
		Use:   "free",
		Short: "List free discovery resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.FreeResources(ctx)
			})
		},
	})

	var discoverFilters bool
	var discoverPage, discoverPageSize int
	var discoverSort, discoverProductTypes string
	discover := &cobra.Command{
		Use:   "discover <nav-type>",
		Short: "List discovery products, or the filters for that navigation type",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			navType, err := strconv.Atoi(args[0])
			if err != nil {
				return output.NewError("E_VALIDATION", "nav-type must be an integer",
					map[string]any{"received": args[0]})
			}
			options := dedao.DiscoveryOptions{
				NavType:      navType,
				Page:         discoverPage,
				PageSize:     discoverPageSize,
				ClassName:    "全部",
				ProductTypes: discoverProductTypes,
				SortStrategy: discoverSort,
			}
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				if discoverFilters {
					return c.DiscoveryFilters(ctx, options)
				}
				return c.DiscoveryProducts(ctx, options)
			})
		},
	}
	discover.Flags().BoolVar(&discoverFilters, "filters", false, "Return filter options instead of products")
	discover.Flags().IntVar(&discoverPage, "page", 0, "Page number")
	discover.Flags().IntVar(&discoverPageSize, "page-size", 18, "Items per page")
	discover.Flags().StringVar(&discoverSort, "sort-strategy", "HOT", "Sort strategy")
	discover.Flags().StringVar(&discoverProductTypes, "product-types", "66", "Product type filter")
	commands = append(commands, discover)

	// --- live ------------------------------------------------------------
	var liveSubscribed bool
	live := &cobra.Command{
		Use:   "live",
		Short: "List current or subscribed live sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd, func(ctx context.Context, c *dedao.Client) (any, error) {
				return c.Live(ctx, liveSubscribed)
			})
		},
	}
	live.Flags().BoolVar(&liveSubscribed, "subscribed", false, "Only sessions the account subscribed to")
	commands = append(commands, live)

	return commands
}
