package moonshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const maxKimiRecursionWalkNodes = 10000

// validateKimiToolEnumTypes rejects an enum node that declares no type, which
// the official K3.1 API answers with
//
//	tools.function.parameters is not a valid moonshot flavored json schema
//
// It must run before normalizeKimiFunctionParameterSchemas, because that
// baseline inference fills such a type in and the official 400 would be lost.
// const, oneOf, allOf and the structural keywords stay exempt: the official API
// accepts those without a sibling type.
func validateKimiToolEnumTypes(tools []dto.ToolCallRequest, path string) error {
	for index := range tools {
		if tools[index].Type != "function" {
			continue
		}
		var schema any
		if err := common.UnmarshalUseNumber(tools[index].Function.Parameters, &schema); err != nil {
			continue // Let normal upstream validation report malformed JSON.
		}
		if found := findKimiEnumWithoutType(schema, "", 0); found != "" {
			return fmt.Errorf("%s[%d].function.parameters is not a valid moonshot flavored json schema: enum without type at %s", path, index, found)
		}
	}
	return nil
}

// findKimiEnumWithoutType returns the JSON path of the first enum node that has
// no usable type, or an empty string when every enum node declares one.
func findKimiEnumWithoutType(value any, path string, depth int) string {
	if depth > maxKimiFunctionSchemaDepth {
		return ""
	}
	switch schema := value.(type) {
	case map[string]any:
		if _, hasEnum := schema["enum"]; hasEnum && isMissingKimiFunctionSchemaType(schema["type"]) {
			return path
		}
		for keyword, child := range schema {
			switch keyword {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
				children, ok := child.(map[string]any)
				if !ok {
					continue
				}
				for name, node := range children {
					if found := findKimiEnumWithoutType(node, path+"/"+keyword+"/"+name, depth+1); found != "" {
						return found
					}
				}
			case "items", "prefixItems", "contains", "additionalProperties", "unevaluatedProperties",
				"propertyNames", "not", "if", "then", "else", "anyOf", "oneOf", "allOf":
				if found := findKimiEnumWithoutType(child, path+"/"+keyword, depth+1); found != "" {
					return found
				}
			}
		}
	case []any:
		for index, child := range schema {
			if found := findKimiEnumWithoutType(child, path+"/"+strconv.Itoa(index), depth+1); found != "" {
				return found
			}
		}
	}
	return ""
}

// validateKimiToolSchemaRecursion rejects a request whose tool schema recurses
// without a finite instance, matching the official API. Each tool is an
// independent schema document, and K3 can also declare tools mid-conversation
// in a system message.
func validateKimiToolSchemaRecursion(body []byte) error {
	if !bytes.Contains(body, []byte(`"$ref"`)) {
		return nil
	}
	var request map[string]any
	if err := common.UnmarshalUseNumber(body, &request); err != nil {
		return nil // Let normal upstream validation report malformed JSON.
	}
	if err := validateKimiToolListRecursion(request["tools"], "tools"); err != nil {
		return err
	}
	messages, _ := request["messages"].([]any)
	for i, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if err := validateKimiToolListRecursion(message["tools"], fmt.Sprintf("messages[%d].tools", i)); err != nil {
			return err
		}
	}
	return nil
}

func validateKimiToolListRecursion(value any, path string) error {
	tools, _ := value.([]any)
	for i, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if nested, ok := tool["tools"].([]any); ok {
			if err := validateKimiToolListRecursion(nested, fmt.Sprintf("%s[%d].tools", path, i)); err != nil {
				return err
			}
			continue
		}
		field := "parameters"
		if function, ok := tool["function"].(map[string]any); ok && tool["type"] == "function" {
			tool = function
		} else if _, ok := tool["input_schema"]; ok {
			field = "input_schema"
		} else if tool["type"] != "function" {
			continue
		}
		if found := findKimiInfiniteRecursion(tool[field]); found != "" {
			return fmt.Errorf("%s[%d].function.%s is not a valid moonshot flavored json schema: infinite recursion without termination condition at %s", path, i, field, found)
		}
	}
	return nil
}

// The official API rejects a tool schema whose recursion has no finite
// instance:
//
//	detected infinite recursion without termination condition
//
// It answers that 400 only when every edge on the cycle is mandatory. A cycle
// that can stop at an optional property, or that an array can leave empty,
// stays accepted. The keyword set below mirrors the upstream validator as
// measured against api.moonshot.cn:
//
//   - properties only when the property name is listed in required
//   - items only when minItems requires at least one element
//   - anyOf only when every branch leads back into the cycle
//   - not and if/then/else always
//   - oneOf, allOf, patternProperties, additionalProperties and dependentSchemas
//     are never treated as mandatory; a cycle through them stays accepted
//
// findKimiInfiniteRecursion returns the JSON path where the cycle closes, or an
// empty string when every cycle can terminate.
func findKimiInfiniteRecursion(schema any) string {
	walker := kimiInfiniteRecursionWalker{remaining: maxKimiRecursionWalkNodes}
	if path, cycling := walker.walk(schema, schema, "", nil, 0); cycling {
		return path
	}
	return ""
}

type kimiInfiniteRecursionWalker struct {
	remaining int
}

// walk reports whether the subtree cannot be satisfied finitely. active holds
// the local references currently being expanded through mandatory edges only,
// so meeting one of them again closes an unavoidable cycle.
func (w *kimiInfiniteRecursionWalker) walk(value, root any, path string, active []string, depth int) (string, bool) {
	w.remaining--
	if w.remaining < 0 || depth > maxKimiFunctionSchemaDepth {
		return "", false
	}
	schema, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	// A nested $id starts a new schema resource: references inside it target a
	// different root and cannot cycle back into this document.
	if id, ok := schema["$id"].(string); ok && id != "" {
		root = schema
		active = nil
	}
	if ref, ok := schema["$ref"].(string); ok && isLocalKimiSchemaRef(ref) {
		if slices.Contains(active, ref) {
			return path, true
		}
		target, err := kimiSchemaPointer(root, ref)
		if err != nil {
			return "", false
		}
		return w.walk(target, root, path, append(active, ref), depth+1)
	}

	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, node := range properties {
			if !kimiSchemaNameIsRequired(schema, name) {
				continue
			}
			if found, cycling := w.walk(node, root, path+"/properties/"+name, active, depth+1); cycling {
				return found, true
			}
		}
	}
	if items, exists := schema["items"]; exists && kimiSchemaMinItemsRequiresElement(schema) {
		if found, cycling := w.walk(items, root, path+"/items", active, depth+1); cycling {
			return found, true
		}
	}
	// anyOf succeeds with a single branch, so the node is unsatisfiable only
	// when every branch leads back into the same cycle.
	if branches, ok := schema["anyOf"].([]any); ok && len(branches) > 0 {
		allCycle := true
		var firstPath string
		for index, branch := range branches {
			found, cycling := w.walk(branch, root, path+"/anyOf/"+strconv.Itoa(index), active, depth+1)
			if !cycling {
				allCycle = false
				break
			}
			if firstPath == "" {
				firstPath = found
			}
		}
		if allCycle {
			return firstPath, true
		}
	}
	for _, keyword := range []string{"not", "if", "then", "else"} {
		if child, exists := schema[keyword]; exists {
			if found, cycling := w.walk(child, root, path+"/"+keyword, active, depth+1); cycling {
				return found, true
			}
		}
	}
	return "", false
}

// kimiSchemaMinItemsRequiresElement reports whether the array cannot be empty.
// JSON Schema defaults minItems to 0, so an absent or zero bound leaves the
// cycle escapable.
func kimiSchemaMinItemsRequiresElement(schema map[string]any) bool {
	switch value := schema["minItems"].(type) {
	case json.Number:
		parsed, err := value.Int64()
		return err == nil && parsed > 0
	case float64:
		return value > 0
	case int:
		return value > 0
	case int64:
		return value > 0
	}
	return false
}

// isLocalKimiSchemaRef reports whether a reference targets the current schema
// document. External references belong to another document and cannot be
// resolved into a cycle here.
func isLocalKimiSchemaRef(ref string) bool {
	return ref == "#" || strings.HasPrefix(ref, "#/")
}

// kimiSchemaNameIsRequired reports whether a property name is listed in the
// parent's required array. required holds names, not subschemas.
func kimiSchemaNameIsRequired(parent map[string]any, name string) bool {
	required, _ := parent["required"].([]any)
	for _, entry := range required {
		if text, ok := entry.(string); ok && text == name {
			return true
		}
	}
	return false
}
