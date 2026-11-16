package moonshot

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// inlineKimiMediaURLs sends http(s) image and video URLs in message content as
// base64 data. Kimi documents media only as base64 or as ms:// IDs of uploaded
// files, and a Kimi upstream answers a Messages image URL with "invalid base64
// data". A Chat image_url or video_url part gets a data URL in the object form,
// and a Messages image or video block with a url source, also inside a tool
// result, gets a base64 source. Data URLs, file IDs and every other byte of the
// body stay as they are.
func inlineKimiMediaURLs(c *gin.Context, body []byte, format types.RelayFormat) ([]byte, error) {
	messages := ojson.GetBytesNoCopy(body, "messages")
	if !messages.IsArray() {
		return body, nil
	}
	var doc *ojson.Doc
	for messageIndex, message := range messages.Array() {
		content := message.Get("content")
		if !content.IsArray() {
			continue
		}
		var paths []string
		var blocks []ojson.Result
		for partIndex, part := range content.Array() {
			path := fmt.Sprintf("messages.%d.content.%d", messageIndex, partIndex)
			paths, blocks = append(paths, path), append(blocks, part)
			nested := part.Get("content")
			if format == types.RelayFormatClaude && part.Get("type").String() == "tool_result" && nested.IsArray() {
				for nestedIndex, block := range nested.Array() {
					paths, blocks = append(paths, fmt.Sprintf("%s.content.%d", path, nestedIndex)), append(blocks, block)
				}
			}
		}
		for index, block := range blocks {
			kind := block.Get("type").String()
			var mediaURL ojson.Result
			var member string
			switch {
			case format == types.RelayFormatOpenAI && (kind == dto.ContentTypeImageURL || kind == dto.ContentTypeVideoUrl):
				mediaURL, member = block.Get(kind), kind
				if mediaURL.IsObject() {
					mediaURL, member = mediaURL.Get("url"), kind+".url"
				}
			case format == types.RelayFormatClaude && (kind == "image" || kind == "video") && block.Get("source.type").String() == "url":
				mediaURL, member = block.Get("source.url"), "source"
			default:
				continue
			}
			if mediaURL.Type != ojson.String {
				continue
			}
			rawURL := mediaURL.String()
			scheme := strings.ToLower(rawURL[:min(len(rawURL), len("https://"))])
			if !strings.HasPrefix(scheme, "http://") && !strings.HasPrefix(scheme, "https://") {
				continue
			}
			data, mimeType, err := downloadKimiMedia(c, strings.TrimSuffix(kind, "_url"), rawURL)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", paths[index], err)
			}
			var value any = "data:" + mimeType + ";base64," + data
			if member == "source" {
				value = dto.ClaudeMessageSource{Type: "base64", MediaType: mimeType, Data: data}
			} else if member == kind {
				value = map[string]any{"url": value}
			}
			raw, err := common.Marshal(value)
			if err != nil {
				return nil, err
			}
			if doc == nil {
				doc = ojson.NewDoc(body)
			}
			if err := doc.SetRawBytes(paths[index]+"."+member, raw); err != nil {
				return nil, fmt.Errorf("%s: %w", paths[index], err)
			}
		}
	}
	if doc == nil {
		return body, nil
	}
	return doc.Bytes(), nil
}

// downloadKimiMedia returns the base64 data and media type of an image or a
// video. The shared file service applies SSRF protection and
// MAX_FILE_DOWNLOAD_MB, and reuses media that token counting already fetched
// for this request. Storage often serves media as a generic or wrong type, so
// a type of another kind is replaced by the one its first bytes show; a page
// that answered with HTML instead of the media is rejected here, where the
// client sees why.
func downloadKimiMedia(c *gin.Context, kind, mediaURL string) (string, string, error) {
	data, mimeType, err := service.GetBase64Data(c, types.NewURLFileSource(mediaURL), "inline media for Moonshot")
	if err != nil {
		return "", "", fmt.Errorf("cannot download %s URL: %w", kind, err)
	}
	mimeType = strings.ToLower(mimeType)
	if !strings.HasPrefix(mimeType, kind+"/") {
		// 684 base64 characters hold the 512 bytes content sniffing reads.
		head, _ := base64.StdEncoding.DecodeString(data[:min(len(data), 684)])
		mimeType = http.DetectContentType(head)
	}
	if !strings.HasPrefix(mimeType, kind+"/") {
		return "", "", fmt.Errorf("%s URL returned %s instead of %s data", kind, mimeType, kind)
	}
	return data, mimeType, nil
}
