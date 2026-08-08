package cmd

import (
	"github.com/fatecannotbealtered/dedao-cli/internal/dedao"
	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/spf13/cobra"
)

// The five commands here read owned content rather than list it, so each one
// states plainly what it does *not* return: captions without media urls, an
// ebook chapter without a reading token, an audiobook saved locally without its
// play url. Each also checks entitlement against the account rather than
// inferring it from a fetch having succeeded.

// articleCaptionsCommand downloads a course video's caption track.
func (a *application) articleCaptionsCommand() *cobra.Command {
	var format, out string
	var textOnly bool
	cmd := &cobra.Command{
		Use:   "article-captions <article-enid>",
		Short: "Download an owned course video's captions as text",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			// The caption track is exactly the kind of unwrapped payload
			// --format raw exists for.
			"raw_output": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.ArticleCaptions(cmd.Context(), args[0], format, out)
			if err != nil {
				return err
			}
			if textOnly || a.format == output.FormatRaw {
				return a.printer().Raw([]byte(result.Text + "\n"))
			}
			return a.success(result)
		},
	}
	cmd.Flags().StringVar(&format, "format-track", "srt",
		"Caption track to download: srt or vtt")
	cmd.Flags().StringVar(&out, "out", "", "Also save the track to this path")
	cmd.Flags().BoolVar(&textOnly, "text-only", false, "Print the caption text alone")
	return cmd
}

// ebookChaptersCommand lists an owned ebook's table of contents.
func (a *application) ebookChaptersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ebook-chapters <ebook-enid>",
		Short: "List an owned ebook's chapters",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.EbookChapters(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.success(result)
		},
	}
}

// ebookReadCommand reads one chapter of an owned ebook as text.
func (a *application) ebookReadCommand() *cobra.Command {
	var chapter, title string
	var textOnly bool
	cmd := &cobra.Command{
		Use:   "ebook-read <ebook-enid>",
		Short: "Read one chapter of an owned ebook as text",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"raw_output": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if chapter == "" && title == "" {
				return output.NewError("E_VALIDATION",
					"name the chapter with --chapter or --title",
					map[string]any{"ebook_enid": args[0]})
			}
			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.EbookChapterText(cmd.Context(), args[0], chapter, title)
			if err != nil {
				return err
			}
			if textOnly || a.format == output.FormatRaw {
				text, _ := result["text"].(string)
				return a.printer().Raw([]byte(text + "\n"))
			}
			return a.success(result)
		},
	}
	cmd.Flags().StringVar(&chapter, "chapter", "", "Chapter id, for example Chapter_1_1")
	cmd.Flags().StringVar(&title, "title", "", "Match a chapter by title substring")
	cmd.Flags().BoolVar(&textOnly, "text-only", false, "Print the chapter text alone")
	return cmd
}

// audiobookMediaCommand saves an authorized audiobook locally.
func (a *application) audiobookMediaCommand() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "audiobook-media <topic-id-or-alias-id>",
		Short: "Save an authorized audiobook to a local file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.AudiobookMedia(cmd.Context(), args[0], out)
			if err != nil {
				return err
			}
			return a.success(result)
		},
	}
	cmd.Flags().StringVar(&out, "out", "",
		"Where to save the file (default: <state-dir>/media/<title>.ts)")
	return cmd
}

// dailyCommand collects what is new in owned courses since the last run.
func (a *application) dailyCommand() *cobra.Command {
	var checkpoint string
	var includeExisting, withContent bool
	cmd := &cobra.Command{
		Use:   "daily",
		Short: "Collect articles added to owned courses since the last run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.Daily(cmd.Context(), dedaoDailyOptions(
				checkpoint, includeExisting, withContent))
			if err != nil {
				return err
			}
			return a.success(result)
		},
	}
	cmd.Flags().StringVar(&checkpoint, "checkpoint", "",
		"Checkpoint file (default: <state-dir>/checkpoint.json)")
	cmd.Flags().BoolVar(&includeExisting, "include-existing", false,
		"Report a course's existing back catalogue on its first run")
	cmd.Flags().BoolVar(&withContent, "with-content", false,
		"Fetch each new article's text as well as its metadata")
	return cmd
}

// dedaoDailyOptions keeps the flag plumbing out of the command body.
func dedaoDailyOptions(checkpoint string, includeExisting, withContent bool) dedao.DailyOptions {
	return dedao.DailyOptions{
		CheckpointPath:  checkpoint,
		IncludeExisting: includeExisting,
		WithContent:     withContent,
	}
}
