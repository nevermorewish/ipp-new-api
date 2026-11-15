package moonshot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// kvvNamedToolCall is the function call that answers a named tool_choice the
// gateway sent upstream as structured output.
type kvvNamedToolCall struct {
	name string
	id   string
}

// emulateKVVNamedToolChoice answers a named tool_choice with thinking disabled
// the way the official K3.1 API does. Kimi upstreams reject that combination as
// if thinking were on, but they do enforce a json_schema response format, so
// the request asks for the function's arguments as structured output and the
// response is turned back into a call to that function. A tool_choice naming
// an undeclared function is forwarded unchanged for the upstream to reject.
func emulateKVVNamedToolChoice(request *dto.GeneralOpenAIRequest) *kvvNamedToolCall {
	choice, err := common.Marshal(request.ToolChoice)
	if err != nil || ojson.GetBytes(choice, "type").String() != "function" {
		return nil
	}
	name := ojson.GetBytes(choice, "function.name").String()
	if name == "" {
		return nil
	}
	var function *dto.FunctionRequest
	for i := range request.Tools {
		if request.Tools[i].Type == "function" && request.Tools[i].Function.Name == name {
			function = &request.Tools[i].Function
			break
		}
	}
	// K3 can also declare tools in a system message.
	for i := 0; function == nil && i < len(request.Messages); i++ {
		var tools []dto.ToolCallRequest
		if len(request.Messages[i].Tools) == 0 || common.Unmarshal(request.Messages[i].Tools, &tools) != nil {
			continue
		}
		for j := range tools {
			if tools[j].Type == "function" && tools[j].Function.Name == name {
				function = &tools[j].Function
				break
			}
		}
	}
	if function == nil {
		return nil
	}
	schema := function.Parameters
	if len(schema) == 0 || common.GetJsonType(schema) == "null" {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	format, err := common.Marshal(dto.FormatJsonSchema{Name: name, Description: function.Description, Schema: schema})
	if err != nil {
		return nil
	}
	// The tools stay in the prompt so the model keeps their context; none
	// keeps the upstream from answering with a different tool.
	request.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: format}
	request.ToolChoice = "none"
	// The upstream's own call IDs look like get_weather_0_bbd6df97.
	return &kvvNamedToolCall{name: name, id: fmt.Sprintf("%s_0_%s", name, common.GetUUID()[:8])}
}

// rewriteResponse turns the structured-output answer of a non-streaming
// response into a call to the named function, in the shape the upstream uses
// for a real call: empty content and finish_reason tool_calls.
func (call kvvNamedToolCall) rewriteResponse(body []byte) []byte {
	for i, choice := range ojson.GetBytes(body, "choices").Array() {
		content := choice.Get("message.content")
		if content.Type != ojson.String || content.String() == "" {
			continue
		}
		if existing := choice.Get("message.tool_calls"); existing.IsArray() && len(existing.Array()) > 0 {
			continue
		}
		toolCalls, err := common.Marshal([]map[string]any{{
			"id":       call.id,
			"type":     "function",
			"function": map[string]any{"name": call.name, "arguments": content.String()},
		}})
		if err != nil {
			return body
		}
		prefix := fmt.Sprintf("choices.%d", i)
		rewritten, err := ojson.SetRawBytes(body, prefix+".message.tool_calls", toolCalls)
		if err == nil {
			rewritten, err = ojson.SetBytes(rewritten, prefix+".message.content", "")
		}
		if err == nil && choice.Get("finish_reason").String() == "stop" {
			rewritten, err = ojson.SetBytes(rewritten, prefix+".finish_reason", "tool_calls")
		}
		if err != nil {
			return body
		}
		body = rewritten
	}
	return body
}

// kvvNamedToolCallStream rewrites a streaming structured-output answer into
// argument deltas of a call to the named function. It works line by line, so
// every SSE event is forwarded as soon as the upstream sends it.
type kvvNamedToolCallStream struct {
	call    kvvNamedToolCall
	source  *bufio.Reader
	body    io.Closer
	started map[int64]bool // choice index -> call header already sent
	pending []byte
	err     error
}

func newKVVNamedToolCallStream(body io.ReadCloser, call kvvNamedToolCall) *kvvNamedToolCallStream {
	return &kvvNamedToolCallStream{call: call, source: bufio.NewReader(body), body: body, started: make(map[int64]bool)}
}

func (s *kvvNamedToolCallStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		line, err := s.source.ReadBytes('\n')
		if len(line) > 0 {
			s.pending = s.rewriteEvent(line)
		}
		s.err = err
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *kvvNamedToolCallStream) Close() error {
	return s.body.Close()
}

func (s *kvvNamedToolCallStream) rewriteEvent(line []byte) []byte {
	text := bytes.TrimRight(line, "\r\n")
	payload, ok := bytes.CutPrefix(text, []byte("data:"))
	payload = bytes.TrimSpace(payload)
	if !ok || len(payload) == 0 || payload[0] != '{' {
		return line
	}
	for i, choice := range ojson.GetBytes(payload, "choices").Array() {
		index := choice.Get("index").Int()
		prefix := fmt.Sprintf("choices.%d", i)
		if content := choice.Get("delta.content"); content.Type == ojson.String && content.String() != "" {
			// The first delta carries the call header, like a real streamed
			// call; later ones carry only argument text.
			function := map[string]any{"arguments": content.String()}
			delta := map[string]any{"index": 0, "function": function}
			if !s.started[index] {
				delta["id"], delta["type"], function["name"] = s.call.id, "function", s.call.name
			}
			toolCalls, err := common.Marshal([]any{delta})
			if err != nil {
				return line
			}
			rewritten, err := ojson.SetRawBytes(payload, prefix+".delta.tool_calls", toolCalls)
			if err == nil {
				rewritten, err = ojson.DeleteBytes(rewritten, prefix+".delta.content")
			}
			if err != nil {
				return line
			}
			payload = rewritten
			s.started[index] = true
		}
		if s.started[index] && choice.Get("finish_reason").String() == "stop" {
			rewritten, err := ojson.SetBytes(payload, prefix+".finish_reason", "tool_calls")
			if err != nil {
				return line
			}
			payload = rewritten
		}
	}
	return append(append([]byte("data: "), payload...), line[len(text):]...)
}
