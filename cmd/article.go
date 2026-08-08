package cmd

import (
	"github.com/fatecannotbealtered/dedao-cli/internal/output"
	"github.com/spf13/cobra"
)

// articleCommand reads an owned article's body.
//
// `--render` is command-local and deliberately not called `--format`: the
// global --format json|text|raw is the machine contract and must stay
// unambiguous. --render selects what goes *inside* the payload; --format raw
// then emits it unwrapped for piping.
func (a *application) articleCommand() *cobra.Command {
	var render string
	cmd := &cobra.Command{
		Use:   "article <article-enid>",
		Short: "Read an owned article's body",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			// raw is legal here: the rendered body is exactly the kind of
			// unwrapped payload --format raw exists for.
			"raw_output": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch render {
			case "nodes", "text", "markdown":
			default:
				return output.NewError("E_VALIDATION",
					"--render must be one of: nodes, text, markdown",
					map[string]any{"received": render})
			}
			if a.format == output.FormatRaw && render == "nodes" {
				return output.NewError("E_USAGE",
					"--format raw needs --render text or --render markdown",
					map[string]any{"render": render})
			}

			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.ArticleContent(cmd.Context(), args[0], render)
			if err != nil {
				return err
			}

			if a.format == output.FormatRaw {
				body := result.Text
				if render == "markdown" {
					body = result.Markdown
				}
				return a.printer().Raw([]byte(body + "\n"))
			}
			return a.success(result)
		},
	}
	cmd.Flags().StringVar(&render, "render", "nodes",
		"Body rendering: nodes, text, or markdown")
	return cmd
}
