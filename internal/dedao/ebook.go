package dedao

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// 得到's web reader draws each page as an SVG of individually positioned
// glyphs, encrypted with a key the reader itself carries. Reading a chapter
// therefore means decrypting the page and putting the glyphs back into lines.
//
// Entitlement is the account's and is checked before any of that: an unowned
// book is refused here rather than half-fetched. The decryption is the same
// step the reader performs for a subscriber who opens the book; it is not a
// way around a purchase, and it cannot become one, because the upstream simply
// does not issue a reading token without entitlement.

const (
	// ebookAESKey and ebookAESIV are the web reader's own page key.
	ebookAESKey = "3e4r06tjkpjcevlbslr3d96gdb5ahbmo"
	ebookAESIV  = "6fd89a1b3a7f48fb"
)

// decryptEbookPage turns one encrypted page payload back into its SVG.
func decryptEbookPage(payload string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", &APIError{Message: "an ebook page was not valid base64"}
	}
	block, err := aes.NewCipher([]byte(ebookAESKey))
	if err != nil {
		return "", &APIError{Message: "the ebook page cipher could not be built"}
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", &APIError{Message: "an ebook page was not a whole number of blocks"}
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, []byte(ebookAESIV)).CryptBlocks(plaintext, ciphertext)

	// PKCS#7, removed only when the padding is actually well-formed: a page
	// that decrypts to something else should not lose its last bytes as well.
	pad := int(plaintext[len(plaintext)-1])
	if pad >= 1 && pad <= aes.BlockSize && pad <= len(plaintext) {
		valid := true
		for _, value := range plaintext[len(plaintext)-pad:] {
			if int(value) != pad {
				valid = false
				break
			}
		}
		if valid {
			plaintext = plaintext[:len(plaintext)-pad]
		}
	}
	return string(plaintext), nil
}

var (
	svgTextNode = regexp.MustCompile(`(?is)<text\b([^>]*)>(.*?)</text>`)
	svgYValue   = regexp.MustCompile(`\by=["']?([-.\d]+)`)
	svgTag      = regexp.MustCompile(`<[^>]+>`)
)

// svgLineBreak is how far apart two glyphs must be, vertically, to be on
// different lines.
const svgLineBreak = 5.0

// svgToText reassembles a page's glyphs into lines.
//
// The glyphs are taken in document order rather than sorted by position:
// sorting interleaves footnotes and columns, while document order is what the
// reader itself renders.
func svgToText(svg string) string {
	type glyph struct {
		y    float64
		text string
	}
	var glyphs []glyph
	for _, match := range svgTextNode.FindAllStringSubmatch(svg, -1) {
		text := strings.TrimSpace(strings.ReplaceAll(
			html.UnescapeString(svgTag.ReplaceAllString(match[2], "")), " ", " "))
		if text == "" {
			continue
		}
		y := 0.0
		if position := svgYValue.FindStringSubmatch(match[1]); position != nil {
			if parsed, err := strconv.ParseFloat(position[1], 64); err == nil {
				y = parsed
			}
		}
		glyphs = append(glyphs, glyph{y: y, text: text})
	}
	if len(glyphs) == 0 {
		return ""
	}
	var lines []string
	var buffer []string
	lastY := glyphs[0].y
	for index, item := range glyphs {
		if index == 0 {
			buffer = []string{item.text}
			continue
		}
		if item.y-lastY > svgLineBreak || lastY-item.y > svgLineBreak {
			lines = append(lines, strings.Join(buffer, ""))
			buffer = []string{item.text}
			lastY = item.y
			continue
		}
		buffer = append(buffer, item.text)
	}
	lines = append(lines, strings.Join(buffer, ""))
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// ebookDetail reads one ebook's detail record with the reader referer the
// endpoint expects, and as a map rather than as an opaque value.
func (c *Client) ebookDetail(ctx context.Context, ebookEnid string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	value, err := c.getWithReferer(ctx, "/pc/ebook2/v1/pc/detail",
		url.Values{"id": {ebookEnid}}, c.baseURL+"/ebook/reader?id="+ebookEnid)
	if err != nil {
		return nil, err
	}
	info, _ := value.(map[string]any)
	if info == nil {
		info = map[string]any{}
	}
	return info, nil
}

// ebookEntitled answers whether this account may read the whole book.
//
// A VIP title is readable on an active ebook membership; anything else needs a
// purchase. The answer comes from the account, never from a fetch happening to
// succeed.
func (c *Client) ebookEntitled(ctx context.Context, info map[string]any) (bool, error) {
	if truthyValue(info["is_buy"]) {
		return true, nil
	}
	if !truthyValue(info["is_vip_book"]) {
		return false, nil
	}
	value, err := c.EbookVIPInfo(ctx)
	if err != nil {
		return false, err
	}
	vip, _ := value.(map[string]any)
	return truthyValue(vip["is_vip"]) && !truthyValue(vip["is_expire"]), nil
}

// notEntitled is the one refusal both ebook readers give.
func notEntitled() error {
	return &APIError{
		StatusCode: 403,
		Message: "this account is not entitled to read this ebook in full " +
			"(it needs a purchase, or an active ebook membership for a VIP title)",
	}
}

// ebookReadToken asks for the reader token that unlocks the book's pages.
func (c *Client) ebookReadToken(ctx context.Context, ebookEnid string) (string, error) {
	// Unsanitized on purpose: the reading token lives in a field the output
	// redactor strips, and it is needed to fetch the pages. It never leaves
	// this package.
	value, err := c.requestJSONUnsanitized(ctx, http.MethodPost,
		"/api/pc/ebook2/v1/pc/read/token", nil,
		map[string]any{"id": ebookEnid}, c.baseURL+"/ebook/reader?id="+ebookEnid)
	if err != nil {
		return "", err
	}
	result, _ := value.(map[string]any)
	token := asString(result["token"])
	if token == "" {
		return "", &APIError{StatusCode: 403, Message: "no ebook reading token was issued"}
	}
	return token, nil
}

// ebookBookInfo reads the book's structure: its table of contents and the
// chapter order the pages follow.
func (c *Client) ebookBookInfo(ctx context.Context, token, ebookEnid string) (map[string]any, error) {
	value, err := c.getWithReferer(ctx, "/ebk_web/v1/get_book_info",
		url.Values{"token": {token}}, c.baseURL+"/ebook/reader?id="+ebookEnid)
	if err != nil {
		return nil, err
	}
	book, _ := value.(map[string]any)
	if book == nil {
		book = map[string]any{}
	}
	return book, nil
}

// ebookPages fetches one window of a chapter's rendered pages.
//
// The layout numbers are the web reader's own: they decide how the text is
// paginated, and changing them would produce pages the reader never showed.
func (c *Client) ebookPages(
	ctx context.Context,
	chapterID, token, ebookEnid string,
	index, count int,
) (map[string]any, error) {
	value, err := c.postWithReferer(ctx, "/ebk_web_go/v2/get_pages", map[string]any{
		"chapter_id":  chapterID,
		"count":       count,
		"index":       index,
		"offset":      0,
		"orientation": 0,
		"config": map[string]any{
			"density":         1,
			"direction":       0,
			"font_name":       "pingfang",
			"font_scale":      1,
			"font_size":       16,
			"height":          200000,
			"line_height":     "2em",
			"margin_bottom":   20,
			"margin_left":     20,
			"margin_right":    20,
			"margin_top":      0,
			"paragraph_space": "1em",
			"platform":        1,
			"width":           60000,
		},
		"token": token,
	}, c.baseURL+"/ebook/reader?id="+ebookEnid)
	if err != nil {
		return nil, err
	}
	pages, _ := value.(map[string]any)
	if pages == nil {
		pages = map[string]any{}
	}
	return pages, nil
}

// chapterIDFromHref turns a table-of-contents href into the chapter id the
// page endpoint takes.
func chapterIDFromHref(href string) string {
	path := href
	if hash := strings.Index(path, "#"); hash >= 0 {
		path = path[:hash]
	}
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		path = path[slash+1:]
	}
	return strings.TrimSuffix(path, ".xhtml")
}

// EbookChapters lists an entitled ebook's table of contents.
//
// No reading token and no page content leave this call: it answers "what is in
// this book", which is what a caller needs before asking for a chapter.
func (c *Client) EbookChapters(ctx context.Context, ebookEnid string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	info, err := c.ebookDetail(ctx, ebookEnid)
	if err != nil {
		return nil, err
	}
	entitled, err := c.ebookEntitled(ctx, info)
	if err != nil {
		return nil, err
	}
	if !entitled {
		return nil, notEntitled()
	}
	token, err := c.ebookReadToken(ctx, ebookEnid)
	if err != nil {
		return nil, err
	}
	book, err := c.ebookBookInfo(ctx, token, ebookEnid)
	if err != nil {
		return nil, err
	}
	bookInfo, _ := book["bookInfo"].(map[string]any)

	toc := []any{}
	for _, raw := range asList(bookInfo["toc"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		href := asString(item["href"])
		toc = append(toc, map[string]any{
			"title":      item["text"],
			"level":      item["level"],
			"play_order": item["playOrder"],
			"chapter_id": emptyToNil(chapterIDFromHref(href)),
			"href":       emptyToNil(href),
		})
	}
	chapters := []any{}
	for _, raw := range asList(bookInfo["orders"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		chapters = append(chapters, map[string]any{
			"chapter_id": item["chapterId"],
			"path":       item["pathInEpub"],
		})
	}

	title := info["operating_title"]
	if asString(title) == "" {
		title = info["title"]
	}
	return map[string]any{
		"enid":        ebookEnid,
		"title":       title,
		"is_buy":      info["is_buy"],
		"is_vip_book": info["is_vip_book"],
		"toc":         toc,
		"chapters":    chapters,
	}, nil
}

// EbookChapterText reads one entitled chapter as plain text.
//
// The chapter is named either by id or by a title substring; a title is matched
// against the book's own contents so a caller can say "the chapter about X"
// without first fetching the whole table.
func (c *Client) EbookChapterText(
	ctx context.Context,
	ebookEnid, chapterID, title string,
) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	info, err := c.ebookDetail(ctx, ebookEnid)
	if err != nil {
		return nil, err
	}
	entitled, err := c.ebookEntitled(ctx, info)
	if err != nil {
		return nil, err
	}
	if !entitled {
		return nil, notEntitled()
	}
	token, err := c.ebookReadToken(ctx, ebookEnid)
	if err != nil {
		return nil, err
	}
	book, err := c.ebookBookInfo(ctx, token, ebookEnid)
	if err != nil {
		return nil, err
	}
	bookInfo, _ := book["bookInfo"].(map[string]any)
	toc := asList(bookInfo["toc"])

	resolvedID, resolvedTitle := strings.TrimSpace(chapterID), strings.TrimSpace(title)
	if resolvedID == "" && resolvedTitle != "" {
		for _, raw := range toc {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			text := asString(item["text"])
			if strings.Contains(text, resolvedTitle) || strings.Contains(resolvedTitle, text) {
				resolvedID = chapterIDFromHref(asString(item["href"]))
				resolvedTitle = text
				break
			}
		}
	}
	if resolvedID == "" {
		return nil, &APIError{
			StatusCode: 400,
			Message:    "name the chapter with --chapter, or a title that matches one",
		}
	}
	if resolvedTitle == "" {
		for _, raw := range toc {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if chapterIDFromHref(asString(item["href"])) == resolvedID {
				resolvedTitle = asString(item["text"])
				break
			}
		}
	}

	var parts []string
	pageCount, index := 0, 0
	for {
		chunk, err := c.ebookPages(ctx, resolvedID, token, ebookEnid, index, 20)
		if err != nil {
			return nil, err
		}
		pages := asList(chunk["pages"])
		for _, raw := range pages {
			page, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			payload := asString(page["svg"])
			if payload == "" {
				continue
			}
			svg, err := decryptEbookPage(payload)
			if err != nil {
				return nil, err
			}
			parts = append(parts, svgToText(svg))
			pageCount++
		}
		if truthyValue(chunk["is_end"]) || len(pages) == 0 {
			break
		}
		index += len(pages)
	}

	kept := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	bookTitle := info["operating_title"]
	if asString(bookTitle) == "" {
		bookTitle = info["title"]
	}
	return map[string]any{
		"enid":       ebookEnid,
		"book_title": bookTitle,
		"chapter_id": resolvedID,
		"title":      emptyToNil(resolvedTitle),
		"page_count": pageCount,
		"text":       strings.Join(kept, "\n"),
	}, nil
}

// getWithReferer issues a GET that upstream only answers with a reader referer.
func (c *Client) getWithReferer(
	ctx context.Context,
	path string,
	params url.Values,
	referer string,
) (any, error) {
	return c.requestJSONWithReferer(ctx, http.MethodGet, path, params, nil, referer)
}

// postWithReferer issues a POST that upstream only answers with a reader
// referer.
func (c *Client) postWithReferer(
	ctx context.Context,
	path string,
	body any,
	referer string,
) (any, error) {
	return c.requestJSONWithReferer(ctx, http.MethodPost, path, nil, body, referer)
}

// emptyToNil reports an absent string as nothing rather than as "".
func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
