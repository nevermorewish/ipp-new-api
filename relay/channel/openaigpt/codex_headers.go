package openaigpt

import (
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
)

// These headers carry protocol negotiation and continuation state for Codex
// CLI/Desktop. They are needed even when whole-request passthrough is off.
// Credentials, account selection and transport headers remain gateway-owned.
var codexProtocolHeaders = []string{
	"OpenAI-Beta", "Originator", "Version",
	"X-OpenAI-Internal-Codex-Responses-Lite",
	"X-Codex-Beta-Features", "X-Codex-Turn-State", "X-Codex-Turn-Metadata",
	"X-Client-Request-Id", "X-Oai-Attestation",
	"Session-Id", "Thread-Id", "Conversation-Id", "Session_id", "Conversation_id",
}

func forwardCodexProtocolHeaders(c *gin.Context, target *http.Header, info *relaycommon.RelayInfo) {
	if c == nil || c.Request == nil || target == nil || info == nil {
		return
	}
	if info.RelayMode != relayconstant.RelayModeResponses && info.RelayMode != relayconstant.RelayModeResponsesCompact {
		return
	}
	for _, name := range codexProtocolHeaders {
		if values := c.Request.Header.Values(name); len(values) > 0 {
			(*target)[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
}

// The shared SSE handler creates its own headers. Return the upstream's opaque
// turn state before it flushes so the client can echo it on the next request.
func forwardCodexStreamTurnState(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) {
	if c == nil || c.Writer == nil || resp == nil || resp.Body == nil || info == nil {
		return
	}
	if info.RelayMode != relayconstant.RelayModeResponses || !info.IsStream || resp.StatusCode != http.StatusOK {
		return
	}
	const name = "X-Codex-Turn-State"
	c.Writer.Header().Del(name)
	if values := resp.Header.Values(name); len(values) > 0 {
		c.Writer.Header()[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
}
