package openaigpt

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// PreserveResponsesStreamOptions restores the original Codex options after
// conversion through the shared DTO. Only the OpenAI-GPT handler calls this;
// generic OpenAI and Chat conversion retain their existing behavior.
// Channel filters and parameter overrides must run after this restoration.
func PreserveResponsesStreamOptions(converted, inbound []byte) ([]byte, error) {
	options := gjson.GetBytes(inbound, "stream_options")
	if !options.Exists() {
		return converted, nil
	}
	return sjson.SetRawBytes(converted, "stream_options", []byte(options.Raw))
}
