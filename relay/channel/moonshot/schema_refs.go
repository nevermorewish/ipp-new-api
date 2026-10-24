package moonshot

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const maxKimiExpandedSchemaNodes = 10000

var errKimiRecursiveSchema = errors.New("recursive tool schema")

// Resolve references at the wire boundary, after DTO conversion and parameter
// overrides. Each tool is an independent JSON Schema document: definitions in
// another tool must never be used to repair a dangling reference.
func normalizeKimiToolSchemaRefsBody(body []byte) ([]byte, bool, error) {
	if !bytes.Contains(body, []byte(`"$ref"`)) {
		return body, false, nil
	}
	var request map[string]any
	if err := common.UnmarshalUseNumber(body, &request); err != nil {
		return body, false, nil // Let normal upstream validation handle malformed JSON.
	}
	changed, err := normalizeKimiToolSchemaRefs(request["tools"], "tools", 0)
	if err != nil {
		return body, false, err
	}
	// K3 can introduce tools mid-conversation in system messages.
	messages, _ := request["messages"].([]any)
	for i, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		updated, err := normalizeKimiToolSchemaRefs(message["tools"], fmt.Sprintf("messages[%d].tools", i), 0)
		if err != nil {
			return body, false, err
		}
		changed = changed || updated
	}
	if !changed {
		return body, false, nil
	}
	normalized, err := common.Marshal(request)
	return normalized, err == nil, err
}

func normalizeKimiToolSchemaRefs(value any, path string, depth int) (bool, error) {
	if depth > maxKimiFunctionSchemaDepth {
		return false, fmt.Errorf("%s: tool nesting exceeds %d", path, maxKimiFunctionSchemaDepth)
	}
	tools, _ := value.([]any)
	changed := false
	for i, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		toolPath := fmt.Sprintf("%s[%d]", path, i)
		if nested, ok := tool["tools"].([]any); ok {
			updated, err := normalizeKimiToolSchemaRefs(nested, toolPath+".tools", depth+1)
			if err != nil {
				return false, err
			}
			changed = changed || updated
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
		schema := tool[field]
		resolver := kimiSchemaRefResolver{remaining: maxKimiExpandedSchemaNodes}
		resolved, err := resolver.expand(schema, schema, nil, 0)
		if errors.Is(err, errKimiRecursiveSchema) {
			// Recursive schemas cannot be finitely inlined. Preserve their original
			// references and definitions rather than silently weakening constraints.
			continue
		}
		if err != nil {
			return false, fmt.Errorf("%s (%v).%s: %w", toolPath, tool["name"], field, err)
		}
		if !reflect.DeepEqual(schema, resolved) {
			// Inlined enum/array/object nodes need the same type compatibility as
			// ordinary Kimi schemas, including anyOf branch type placement.
			normalizeKimiFunctionSchemaNode(resolved, 0)
			tool[field] = resolved
			changed = true
		}
	}
	return changed, nil
}

type kimiSchemaRefResolver struct {
	remaining int
}

func (r *kimiSchemaRefResolver) expand(value, root any, active []string, depth int) (any, error) {
	r.remaining--
	if r.remaining < 0 || depth > maxKimiFunctionSchemaDepth {
		return nil, errors.New("local schema reference expansion exceeds size or depth limit")
	}
	schema, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	// A nested $id starts a new schema resource and hence a new local root.
	if id, ok := schema["$id"].(string); ok && id != "" {
		root = schema
	}
	result := make(map[string]any, len(schema))
	maps.Copy(result, schema)
	if ref, ok := schema["$ref"].(string); ok && (ref == "#" || strings.HasPrefix(ref, "#/")) {
		if slices.Contains(active, ref) {
			return nil, errKimiRecursiveSchema
		}
		target, err := kimiSchemaPointer(root, ref)
		if err != nil {
			return nil, err
		}
		expanded, err := r.expand(target, root, append(active, ref), depth+1)
		if err != nil {
			return nil, err
		}
		delete(result, "$ref")
		if targetMap, ok := expanded.(map[string]any); ok {
			// Siblings constrain the reference too. Merge disjoint/equal keywords;
			// use allOf when overwriting would discard either constraint.
			conflict := false
			for key, child := range targetMap {
				if sibling, exists := result[key]; exists && !reflect.DeepEqual(sibling, child) {
					conflict = true
				}
			}
			if !conflict {
				maps.Copy(result, targetMap)
			} else {
				result = map[string]any{"allOf": []any{expanded, result}}
			}
		} else {
			result = map[string]any{"allOf": []any{expanded, result}}
		}
	}
	// Only traverse schema keywords. A $ref in examples/default/const is data.
	for key, child := range result {
		switch key {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			if children, ok := child.(map[string]any); ok {
				copied := make(map[string]any, len(children))
				for name, node := range children {
					expanded, err := r.expand(node, root, active, depth+1)
					if err != nil {
						return nil, err
					}
					copied[name] = expanded
				}
				result[key] = copied
			}
		case "items", "prefixItems", "contains", "additionalProperties", "unevaluatedProperties",
			"unevaluatedItems", "propertyNames", "not", "if", "then", "else", "anyOf", "oneOf", "allOf":
			if nodes, ok := child.([]any); ok {
				copied := make([]any, len(nodes))
				for i, node := range nodes {
					expanded, err := r.expand(node, root, active, depth+1)
					if err != nil {
						return nil, err
					}
					copied[i] = expanded
				}
				result[key] = copied
			} else {
				expanded, err := r.expand(child, root, active, depth+1)
				if err != nil {
					return nil, err
				}
				result[key] = expanded
			}
		}
	}
	return result, nil
}

func kimiSchemaPointer(root any, ref string) (any, error) {
	pointer, err := url.PathUnescape(strings.TrimPrefix(ref, "#"))
	if err != nil {
		return nil, fmt.Errorf("invalid local schema $ref %q", ref)
	}
	value := root
	if pointer != "" {
		for token := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			switch node := value.(type) {
			case map[string]any:
				value = node[token]
			case []any:
				index, err := strconv.Atoi(token)
				if err != nil || index < 0 || index >= len(node) || strconv.Itoa(index) != token {
					value = nil
				} else {
					value = node[index]
				}
			default:
				value = nil
			}
		}
	}
	switch value.(type) {
	case map[string]any, bool:
		return value, nil
	default:
		// MCP clients sometimes emit a generated union schema whose $defs
		// container is present but empty, while its branches still reference
		// names such as __schema0 and __schema3. There is no information left
		// from which to reconstruct those branches. Treat only this generated
		// shape as an unconstrained object so Moonshot can accept the tool; keep
		// rejecting ordinary dangling references instead of borrowing definitions
		// from another tool or silently accepting malformed user schemas.
		if rootMap, ok := root.(map[string]any); ok {
			if defs, ok := rootMap["$defs"].(map[string]any); ok && len(defs) == 0 {
				if suffix, ok := strings.CutPrefix(ref, "#/$defs/__schema"); ok && suffix != "" && strings.Trim(suffix, "0123456789") == "" {
					return map[string]any{"type": "object"}, nil
				}
			}
		}
		return nil, fmt.Errorf("unresolved local schema $ref %q; include its definition in the same tool schema", ref)
	}
}
