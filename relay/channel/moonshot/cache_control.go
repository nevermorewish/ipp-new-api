package moonshot

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var unsupportedCacheControl = regexp.MustCompile(`(?i)\bcache_control\b[\x60'" ]+(?:is )?(?:not supported|unsupported)\b|\bdoes not support[^\r\n]{0,80}?\bcache_control\b`)

// Some Messages endpoints wrap Chat Completions and reject Anthropic cache
// breakpoints. Replay the final wire body only after that specific rejection.
func (a *Adaptor) retryUnsupportedMessagesCacheControl(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*http.Response, error) {
	if resp.Request == nil || resp.Request.GetBody == nil || c.Request.Context().Err() != nil {
		return resp, nil
	}
	if !unsupportedCacheControl.MatchString(moonshotBadRequestMessage(resp)) {
		return resp, nil
	}
	body, err := resp.Request.GetBody()
	if err != nil {
		return resp, nil
	}
	defer body.Close()
	payload, err := io.ReadAll(body)
	if err != nil || !gjson.ValidBytes(payload) {
		return resp, nil
	}
	var request map[string]any
	if common.UnmarshalUseNumber(payload, &request) != nil {
		return resp, nil
	}
	var marks kimiCacheBreakpoints
	marks.remove(request, "cache_control")
	marks.removeBlocks(request["tools"], "tools")
	marks.removeBlocks(request["system"], "system")
	messages, _ := request["messages"].([]any)
	for i, raw := range messages {
		msg, _ := raw.(map[string]any)
		path := fmt.Sprintf("messages[%d]", i)
		marks.remove(msg, path+".cache_control")
		marks.removeBlocks(msg["content"], path+".content")
	}
	logger.LogInfo(c, fmt.Sprintf("cache_control_rectifier hit=%t removed=%d paths=%s", marks.count > 0, marks.count, strings.Join(marks.paths, ",")))
	if marks.count == 0 {
		return resp, nil
	}
	cleaned, err := common.Marshal(request)
	if err != nil {
		return resp, nil
	}
	_ = resp.Body.Close()
	// bytes.Reader carries its own length, so net/http sizes the replay itself.
	return channel.DoApiRequest(a, c, info, bytes.NewReader(cleaned))
}

type kimiCacheBreakpoints struct {
	count int
	paths []string
}

func (m *kimiCacheBreakpoints) remove(block map[string]any, path string) {
	if _, ok := block["cache_control"]; ok {
		delete(block, "cache_control")
		m.count++
		if len(m.paths) < 8 {
			m.paths = append(m.paths, path)
		}
	}
}

// Tool schemas, tool inputs and text may legitimately contain cache_control
// as application data. Descend only through protocol content-block arrays.
func (m *kimiCacheBreakpoints) removeBlocks(content any, path string) {
	blocks, _ := content.([]any)
	for i, raw := range blocks {
		block, _ := raw.(map[string]any)
		blockPath := fmt.Sprintf("%s[%d]", path, i)
		m.remove(block, blockPath+".cache_control")
		switch block["type"] {
		case "tool_result", "search_result":
			m.removeBlocks(block["content"], blockPath+".content")
		}
	}
}
