package openaigpt

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// NormalizeNonStrictChatResponseSchema makes a non-strict Chat structured
// output schema acceptable to upstreams that require closed object schemas
// even when strict was not requested. It only closes object schemas; required
// fields and the rest of the strict contract remain untouched.
func NormalizeNonStrictChatResponseSchema(body []byte) ([]byte, bool, error) {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return body, false, nil
	}

	rawFormat, exists := request["response_format"]
	if !exists {
		return body, false, nil
	}
	var responseFormat map[string]json.RawMessage
	if err := common.Unmarshal(rawFormat, &responseFormat); err != nil {
		return body, false, nil
	}
	if !rawStringEquals(responseFormat["type"], "json_schema") {
		return body, false, nil
	}

	rawDeclared, exists := responseFormat["json_schema"]
	if !exists {
		return body, false, nil
	}
	var declared map[string]json.RawMessage
	if err := common.Unmarshal(rawDeclared, &declared); err != nil {
		return body, false, nil
	}
	if isStrictTrue(declared["strict"]) {
		return body, false, nil
	}

	normalizedSchema, changed, err := normalizeNonStrictSchemaRaw(declared["schema"])
	if err != nil || !changed {
		return body, false, err
	}
	declared["schema"] = normalizedSchema
	encodedDeclared, err := common.Marshal(declared)
	if err != nil {
		return nil, false, err
	}
	responseFormat["json_schema"] = encodedDeclared
	encodedFormat, err := common.Marshal(responseFormat)
	if err != nil {
		return nil, false, err
	}
	request["response_format"] = encodedFormat
	encodedRequest, err := common.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	return encodedRequest, true, nil
}

// NormalizeNonStrictResponsesTextSchema is the Responses counterpart of
// NormalizeNonStrictChatResponseSchema. Responses carries the schema under
// text.format.schema and the strict flag beside it.
func NormalizeNonStrictResponsesTextSchema(body []byte) ([]byte, bool, error) {
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return body, false, nil
	}

	rawText, exists := request["text"]
	if !exists {
		return body, false, nil
	}
	var text map[string]json.RawMessage
	if err := common.Unmarshal(rawText, &text); err != nil {
		return body, false, nil
	}
	rawFormat, exists := text["format"]
	if !exists {
		return body, false, nil
	}
	var format map[string]json.RawMessage
	if err := common.Unmarshal(rawFormat, &format); err != nil {
		return body, false, nil
	}
	if !rawStringEquals(format["type"], "json_schema") || isStrictTrue(format["strict"]) {
		return body, false, nil
	}

	normalizedSchema, changed, err := normalizeNonStrictSchemaRaw(format["schema"])
	if err != nil || !changed {
		return body, false, err
	}
	format["schema"] = normalizedSchema
	encodedFormat, err := common.Marshal(format)
	if err != nil {
		return nil, false, err
	}
	text["format"] = encodedFormat
	encodedText, err := common.Marshal(text)
	if err != nil {
		return nil, false, err
	}
	request["text"] = encodedText
	encodedRequest, err := common.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	return encodedRequest, true, nil
}

func rawStringEquals(raw json.RawMessage, expected string) bool {
	var value string
	return common.Unmarshal(raw, &value) == nil && strings.EqualFold(value, expected)
}

func normalizeNonStrictSchemaRaw(raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return raw, false, nil
	}
	var schema any
	if err := common.Unmarshal(raw, &schema); err != nil {
		return nil, false, fmt.Errorf("schema must be a valid JSON Schema: %w", err)
	}
	changed, err := closeNonStrictObjectSchemas(schema, 0)
	if err != nil || !changed {
		return raw, false, err
	}
	encoded, err := common.Marshal(schema)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

func closeNonStrictObjectSchemas(node any, depth int) (bool, error) {
	if depth > maxStrictSchemaDepth {
		return false, fmt.Errorf("schema exceeds the maximum nesting depth of %d", maxStrictSchemaDepth)
	}
	switch typed := node.(type) {
	case []any:
		changed := false
		for _, child := range typed {
			childChanged, err := closeNonStrictObjectSchemas(child, depth+1)
			if err != nil {
				return false, err
			}
			changed = changed || childChanged
		}
		return changed, nil
	case map[string]any:
		changed := false
		if isStrictObjectSchema(typed) {
			if additional, exists := typed["additionalProperties"]; !exists || additional != false {
				typed["additionalProperties"] = false
				changed = true
			}
		}

		for _, keyword := range []string{
			"items", "prefixItems", "additionalItems", "anyOf", "oneOf", "allOf",
			"not", "contains", "if", "then", "else", "propertyNames", "unevaluatedItems",
		} {
			child, exists := typed[keyword]
			if !exists {
				continue
			}
			childChanged, err := closeNonStrictObjectSchemas(child, depth+1)
			if err != nil {
				return false, err
			}
			changed = changed || childChanged
		}
		for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
			children, ok := typed[keyword].(map[string]any)
			if !ok {
				continue
			}
			for _, child := range children {
				childChanged, err := closeNonStrictObjectSchemas(child, depth+1)
				if err != nil {
					return false, err
				}
				changed = changed || childChanged
			}
		}
		return changed, nil
	default:
		return false, nil
	}
}
