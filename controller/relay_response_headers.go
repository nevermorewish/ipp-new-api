package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// Resolve the channel at the first write. Retries can change the selected channel,
// and an SSE keepalive can commit headers before the upstream response arrives.
type relayHeaderWriter struct {
	gin.ResponseWriter
	ctx *gin.Context
}

func (w *relayHeaderWriter) applyChannelHeaders() {
	if w.Written() || common.GetContextKeyInt(w.ctx, constant.ContextKeyChannelType) != constant.ChannelTypeMoonshot {
		return
	}
	h := w.Header()
	h.Set("Via", "2.0 Caddy")
	h.Set("X-New-Api-Version", "v2.0.39")
	h.Set("X-Node", "losangeles-cn2")
}

func (w *relayHeaderWriter) Write(p []byte) (int, error) {
	w.applyChannelHeaders()
	return w.ResponseWriter.Write(p)
}

func (w *relayHeaderWriter) WriteString(s string) (int, error) {
	w.applyChannelHeaders()
	return w.ResponseWriter.WriteString(s)
}

func (w *relayHeaderWriter) WriteHeaderNow() {
	w.applyChannelHeaders()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *relayHeaderWriter) Flush() {
	w.applyChannelHeaders()
	w.ResponseWriter.Flush()
}

func (w *relayHeaderWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
