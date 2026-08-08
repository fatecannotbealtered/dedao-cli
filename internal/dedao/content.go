package dedao

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Article content arrives as a JSON string holding a node array. Node shapes
// were read off live responses (see docs/API-CONTRACT.md):
//
//	paragraph / blockquote  contents[] of inline runs, plus a flattened `text`
//	image                   url, width, height, legend, jump
//	audio                   title, aid, aliasId, duration, size
//
// Rendering keeps the flattened `text` when present rather than re-joining the
// inline runs: it is what the upstream itself uses for previews, so text output
// matches what a reader sees.

// ContentNode is one block in an article body.
type ContentNode struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Contents []InlineContent `json:"contents,omitempty"`
	Justify  string          `json:"justify,omitempty"`

	// image
	URL    string `json:"url,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Legend string `json:"legend,omitempty"`

	// audio
	Title    string `json:"title,omitempty"`
	Desc     string `json:"desc,omitempty"`
	AID      string `json:"aid,omitempty"`
	AliasID  string `json:"aliasId,omitempty"`
	Duration int    `json:"duration,omitempty"`
	SizeStr  string `json:"sizeStr,omitempty"`
}

// InlineContent is one run inside a paragraph.
type InlineContent struct {
	Type string `json:"type"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
}

// ParseContentNodes decodes the `content` string into typed nodes.
func ParseContentNodes(raw string) ([]ContentNode, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var nodes []ContentNode
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return nil, fmt.Errorf("article content was not a node array: %w", err)
	}
	return nodes, nil
}

// nodeText returns a node's plain text, preferring the flattened field and
// falling back to joining the inline runs.
func nodeText(node ContentNode) string {
	if strings.TrimSpace(node.Text) != "" {
		return node.Text
	}
	var parts []string
	for _, run := range node.Contents {
		if run.Text.Content != "" {
			parts = append(parts, run.Text.Content)
		}
	}
	return strings.Join(parts, "")
}

// RenderText flattens an article to plain text. Images and audio become short
// bracketed markers so an agent knows they were there without being handed a
// media URL it has no right to redistribute.
func RenderText(nodes []ContentNode) string {
	var out []string
	for _, node := range nodes {
		switch node.Type {
		case "paragraph", "blockquote":
			if text := strings.TrimSpace(nodeText(node)); text != "" {
				out = append(out, text)
			}
		case "image":
			if legend := strings.TrimSpace(node.Legend); legend != "" {
				out = append(out, "[图片："+legend+"]")
			} else {
				out = append(out, "[图片]")
			}
		case "audio":
			label := strings.TrimSpace(node.Title)
			if label == "" {
				label = "音频"
			}
			out = append(out, "[音频："+label+"]")
		}
	}
	return strings.Join(out, "\n\n")
}

// RenderMarkdown keeps structure: blockquotes are quoted, images become image
// links, audio becomes a labelled line.
func RenderMarkdown(nodes []ContentNode) string {
	var out []string
	for _, node := range nodes {
		switch node.Type {
		case "paragraph":
			if text := strings.TrimSpace(nodeText(node)); text != "" {
				out = append(out, text)
			}
		case "blockquote":
			text := strings.TrimSpace(nodeText(node))
			if text == "" {
				continue
			}
			lines := strings.Split(text, "\n")
			for i, line := range lines {
				lines[i] = "> " + strings.TrimSpace(line)
			}
			out = append(out, strings.Join(lines, "\n"))
		case "image":
			legend := strings.TrimSpace(node.Legend)
			out = append(out, fmt.Sprintf("![%s](%s)", legend, SanitizeURL(node.URL)))
		case "audio":
			label := strings.TrimSpace(node.Title)
			if label == "" {
				label = "音频"
			}
			if node.Duration > 0 {
				out = append(out, fmt.Sprintf("**[音频] %s** (%ds)", label, node.Duration))
			} else {
				out = append(out, fmt.Sprintf("**[音频] %s**", label))
			}
		}
	}
	return strings.Join(out, "\n\n")
}

// CountCharacters reports the plain-text length, used for the honest
// `content_chars` field rather than counting markup.
func CountCharacters(nodes []ContentNode) int {
	total := 0
	for _, node := range nodes {
		total += len([]rune(nodeText(node)))
	}
	return total
}
