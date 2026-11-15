package moonshot

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// The official Kimi API matches its prompt cache on the token prefix, so a
// request whose long text changes only at its end still reads the cached
// beginning. Some Kimi upstreams reuse the cache only at a content block
// boundary instead, the end of a message or of one text part, and write the
// whole text again. Splitting long text into text parts gives such an upstream
// boundaries to read from. Kimi joins the parts without a separator, so the
// model sees the same text and prompt tokens do not change.
//
// Every cut must depend only on the text before it, because the same text cut
// differently does not share a cache: changing these values or the cut rule
// makes every conversation miss once. An upstream also stops looking back for
// a cached boundary after a number of new blocks (an earlier turn was still
// read after 31 new parts, not after 81), so a message never grows past
// maxKimiCacheTextParts parts.
const (
	kimiCacheTextMinRunes = 1024
	maxKimiCacheTextParts = 16
)

// kimiCacheTextPieces cuts text at 1024, 2048, 4096, ... runes and at the start
// of its last two lines, so a question or an identifier on its own last line
// ends up in a part of its own. It returns nil when the text stays whole.
func kimiCacheTextPieces(text string) []string {
	if !utf8.ValidString(text) {
		return nil
	}
	var cuts []int
	runes, checkpoint := 0, kimiCacheTextMinRunes
	for offset := range text {
		if runes == checkpoint {
			cuts = append(cuts, offset)
			checkpoint *= 2
		}
		runes++
	}
	if runes < kimiCacheTextMinRunes {
		return nil
	}

	// A line starts after a run of ASCII whitespace that contains a line break
	// and is followed by text, so CRLF, blank lines and a trailing newline never
	// start a part of their own.
	end := len(text)
	for found := 0; found < 2; {
		lineBreak := strings.LastIndexAny(text[:end], "\r\n")
		if lineBreak < 0 {
			break
		}
		start := lineBreak + 1
		for start < len(text) && strings.IndexByte(" \t\n\v\f\r", text[start]) >= 0 {
			start++
		}
		end = lineBreak
		for end > 0 && strings.IndexByte(" \t\n\v\f\r", text[end-1]) >= 0 {
			end--
		}
		if start < len(text) {
			cuts = append(cuts, start)
			found++
		}
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)

	textEnd := len(strings.TrimRightFunc(text, unicode.IsSpace))
	pieces := make([]string, 0, len(cuts)+1)
	previous := 0
	for _, cut := range cuts {
		if cut >= textEnd || strings.TrimSpace(text[previous:cut]) == "" {
			continue
		}
		pieces = append(pieces, text[previous:cut])
		previous = cut
	}
	pieces = append(pieces, text[previous:])
	if len(pieces) < 2 {
		return nil
	}
	return pieces
}

// splitKimiCacheTextBody sends the long text of user and system messages as
// text parts. Only the content of those messages changes; every other byte of
// the body stays as the client or an earlier step wrote it. A message with
// dynamic tool declarations, a content part with extra members such as
// cache_control, and every other role keep their content unchanged. Malformed
// JSON is returned as it is.
func splitKimiCacheTextBody(body []byte) []byte {
	messages := ojson.GetBytesNoCopy(body, "messages")
	if !messages.IsArray() {
		return body
	}
	var doc *ojson.Doc
	for index, message := range messages.Array() {
		role := message.Get("role").String()
		if (role != "user" && role != "system") || message.Get("tools").Exists() {
			continue
		}
		content := message.Get("content")
		var parts []any
		split := false
		switch {
		case content.Type == ojson.String:
			for _, piece := range kimiCacheTextPieces(content.String()) {
				parts = append(parts, dto.MediaContent{Type: dto.ContentTypeText, Text: piece})
			}
			split = len(parts) > 0
		case content.IsArray():
			for _, part := range content.Array() {
				text := part.Get("text")
				var pieces []string
				if part.Get("type").String() == dto.ContentTypeText && text.Type == ojson.String && len(part.Map()) == 2 {
					pieces = kimiCacheTextPieces(text.String())
				}
				if pieces == nil {
					parts = append(parts, common.RawMessage(part.Raw))
					continue
				}
				for _, piece := range pieces {
					parts = append(parts, dto.MediaContent{Type: dto.ContentTypeText, Text: piece})
				}
				split = true
			}
		}
		if !split || len(parts) > maxKimiCacheTextParts {
			continue
		}
		raw, err := common.Marshal(parts)
		if err != nil {
			return body
		}
		if doc == nil {
			doc = ojson.NewDoc(body)
		}
		if err := doc.SetRawBytes(fmt.Sprintf("messages.%d.content", index), raw); err != nil {
			return body
		}
	}
	if doc == nil {
		return body
	}
	return doc.Bytes()
}
