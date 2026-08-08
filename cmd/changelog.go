package cmd

import (
	"regexp"
	"strconv"
	"strings"

	dedaocli "github.com/fatecannotbealtered/dedao-cli"
	"github.com/spf13/cobra"
)

var (
	versionHeadingRE = regexp.MustCompile(`(?m)^## \[([^\]]+)\](?: - (\S+))?`)
	categoryRE       = regexp.MustCompile(`(?m)^### (\w+)`)
	bulletRE         = regexp.MustCompile(`(?m)^[-*] (.+)$`)
)

type changelogEntry struct {
	Version string              `json:"version"`
	Date    string              `json:"date,omitempty"`
	Changes map[string][]string `json:"changes"`
}

// parseChangelog turns Keep a Changelog markdown into the machine shape
// CLI-SPEC §11 specifies. Unreleased sections are skipped: an agent asking what
// changed wants shipped versions.
func parseChangelog(markdown string) []changelogEntry {
	headings := versionHeadingRE.FindAllStringSubmatchIndex(markdown, -1)
	entries := []changelogEntry{}

	for index, heading := range headings {
		name := markdown[heading[2]:heading[3]]
		if strings.EqualFold(name, "unreleased") {
			continue
		}
		date := ""
		if heading[4] >= 0 {
			date = markdown[heading[4]:heading[5]]
		}
		end := len(markdown)
		if index+1 < len(headings) {
			end = headings[index+1][0]
		}
		entries = append(entries, changelogEntry{
			Version: name,
			Date:    date,
			Changes: parseCategories(markdown[heading[1]:end]),
		})
	}
	return entries
}

func parseCategories(section string) map[string][]string {
	changes := map[string][]string{}
	headings := categoryRE.FindAllStringSubmatchIndex(section, -1)
	for index, heading := range headings {
		category := strings.ToLower(section[heading[2]:heading[3]])
		end := len(section)
		if index+1 < len(headings) {
			end = headings[index+1][0]
		}
		items := []string{}
		for _, bullet := range bulletRE.FindAllStringSubmatch(section[heading[1]:end], -1) {
			items = append(items, strings.TrimSpace(bullet[1]))
		}
		if len(items) > 0 {
			changes[category] = items
		}
	}
	return changes
}

// compareVersions orders two semver-ish strings, ignoring any pre-release tail.
func compareVersions(left, right string) int {
	parse := func(value string) [3]int {
		var out [3]int
		parts := strings.SplitN(strings.SplitN(value, "-", 2)[0], ".", 4)
		for i := 0; i < 3 && i < len(parts); i++ {
			out[i], _ = strconv.Atoi(strings.TrimPrefix(parts[i], "v"))
		}
		return out
	}
	a, b := parse(left), parse(right)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] > b[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func (a *application) changelogCommand() *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Report what changed between versions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			entries := parseChangelog(dedaocli.ChangelogMarkdown)
			if trimmed := strings.TrimSpace(since); trimmed != "" {
				filtered := []changelogEntry{}
				for _, entry := range entries {
					// Strictly newer, so an agent that already saw `since` gets
					// only the delta.
					if compareVersions(entry.Version, trimmed) > 0 {
						filtered = append(filtered, entry)
					}
				}
				entries = filtered
			}
			return a.success(map[string]any{
				"current_version": version,
				"since":           since,
				"entries":         entries,
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "Only report versions newer than this")
	return cmd
}
