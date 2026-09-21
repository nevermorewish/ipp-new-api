package openaigpt

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// This file holds the contract checks that must read the client's original
// request bytes rather than the parsed DTO.
//
// Everything else in this package validates the outbound body, which is the
// right place for rules about what the gateway is about to send. Unknown fields
// are the exception: on the non-pass-through path the outbound body is
// re-marshaled from the DTO, so a field the DTO does not model has already been
// dropped by the time the outbound body exists. Checking there would report
// success for exactly the requests this is meant to catch.

// responsesFieldAllowList and chatFieldAllowList are the top-level request
// members the gateway understands, derived from the DTO json tags so the lists
// cannot drift as fields are added.
//
// Deriving them from the DTO rather than transcribing the official spec is
// deliberate. The DTO is a superset of the official contract -- it also carries
// vendor extensions for other providers -- so this rejects strictly less than
// OpenAI does. That asymmetry is the safe direction: a field the gateway does
// not model is one it provably cannot act on, so accepting it with HTTP 200
// tells the caller their parameter took effect when it was discarded. A field
// the gateway does model is forwarded and judged upstream.
var (
	responsesFieldAllowList = jsonFieldNames(reflect.TypeOf(dto.OpenAIResponsesRequest{}))
	chatFieldAllowList      = jsonFieldNames(reflect.TypeOf(dto.GeneralOpenAIRequest{}))
	reasoningFieldAllowList = jsonFieldNames(reflect.TypeOf(dto.Reasoning{}))
)

func jsonFieldNames(structType reflect.Type) map[string]struct{} {
	names := make(map[string]struct{}, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		tag := structType.Field(index).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		names[name] = struct{}{}
	}
	return names
}

// ValidateInboundResponsesBody checks the client's Responses request for
// members the gateway cannot act on: unknown top-level fields, unknown
// reasoning members, and Chat-only stream_options members.
//
// A body that is not a JSON object is passed over. Reporting it here would
// duplicate the request-parsing error the caller already gets, in a worse form.
func ValidateInboundResponsesBody(body []byte) error {
	request, ok := decodeTopLevelObject(body)
	if !ok {
		return nil
	}
	if err := rejectUnknownFields(request, responsesFieldAllowList, ""); err != nil {
		return err
	}
	if reasoning, isObject := request["reasoning"].(map[string]any); isObject {
		if err := rejectUnknownFields(reasoning, reasoningFieldAllowList, "reasoning"); err != nil {
			return err
		}
	}
	return validateResponsesStreamOptions(request["stream_options"])
}

// ValidateInboundChatBody checks the client's Chat Completions request for
// unknown top-level fields.
func ValidateInboundChatBody(body []byte) error {
	request, ok := decodeTopLevelObject(body)
	if !ok {
		return nil
	}
	return rejectUnknownFields(request, chatFieldAllowList, "")
}

// validateResponsesStreamOptions rejects Chat-only stream_options members.
//
// Codex clients add members such as reasoning_summary_delivery independently
// of the public API. This channel restores the raw object after DTO conversion,
// so unknown members can reach upstream without a stale local allow-list.
// include_usage remains Chat-only; it does not control Responses usage events.
func validateResponsesStreamOptions(value any) error {
	if value == nil {
		return nil
	}
	options, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("stream_options must be an object")
	}
	if _, exists := options["include_usage"]; exists {
		return fmt.Errorf("stream_options.include_usage is a Chat Completions parameter and is not supported on Responses; Responses reports usage on the response.completed event")
	}
	if value, exists := options["include_obfuscation"]; exists && value != nil {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("stream_options.include_obfuscation must be a boolean")
		}
	}
	return nil
}

func decodeTopLevelObject(body []byte) (map[string]any, bool) {
	if len(body) == 0 {
		return nil, false
	}
	var request map[string]any
	if err := common.Unmarshal(body, &request); err != nil || request == nil {
		return nil, false
	}
	return request, true
}

func rejectUnknownFields(object map[string]any, allowed map[string]struct{}, prefix string) error {
	unknown := make([]string, 0)
	for name := range object {
		if _, ok := allowed[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	paths := make([]string, 0, len(unknown))
	for _, name := range unknown {
		if prefix == "" {
			paths = append(paths, name)
		} else {
			paths = append(paths, prefix+"."+name)
		}
	}
	return fmt.Errorf("unrecognized request argument supplied: %s", strings.Join(paths, ", "))
}
