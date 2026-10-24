package moonshot

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
)

const maxKimiFunctionSchemaDepth = 64

func normalizeKimiFunctionSchemaNode(value any, depth int) bool {
	if depth > maxKimiFunctionSchemaDepth {
		return false
	}

	changed := false
	switch schema := value.(type) {
	case map[string]any:
		if parentItems, exists := schema["items"]; exists {
			if branches, ok := schema["anyOf"].([]any); ok {
				for _, rawBranch := range branches {
					branch, ok := rawBranch.(map[string]any)
					if !ok {
						continue
					}
					branchItems, branchHasItems := branch["items"]
					if branchHasItems && reflect.DeepEqual(parentItems, branchItems) {
						delete(schema, "items")
						changed = true
						break
					}
				}
			}
		}
		for keyword, child := range schema {
			switch keyword {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
				changed = normalizeKimiNamedSchemaChildren(child, depth+1) || changed
			case "items", "prefixItems", "contains", "additionalProperties", "unevaluatedProperties",
				"propertyNames", "not", "if", "then", "else", "anyOf", "oneOf", "allOf":
				changed = normalizeKimiFunctionSchemaNode(child, depth+1) || changed
			}
		}
		changed = normalizeKimiAnyOfTypePlacement(schema) || changed
		changed = inferMissingKimiFunctionSchemaType(schema) || changed
	case []any:
		for _, child := range schema {
			changed = normalizeKimiFunctionSchemaNode(child, depth+1) || changed
		}
	}
	return changed
}

// Moonshot requires type declarations to live inside anyOf branches instead
// of beside anyOf on the parent schema. Distribute an existing parent type to
// untyped branches before removing it so the normalization preserves the
// original constraint while accepting schemas whose branches are already
// fully typed (for example, nullable enum/const schemas).
func normalizeKimiAnyOfTypePlacement(schema map[string]any) bool {
	branches, ok := schema["anyOf"].([]any)
	if !ok || len(branches) == 0 {
		return false
	}

	parentType, parentHasType := schema["type"]
	parentTypeIsUsable := parentHasType && !isMissingKimiFunctionSchemaType(parentType)
	allBranchesTyped := true
	changed := false
	for _, rawBranch := range branches {
		branch, ok := rawBranch.(map[string]any)
		if !ok {
			allBranchesTyped = false
			continue
		}
		if !isMissingKimiFunctionSchemaType(branch["type"]) {
			continue
		}
		if isKimiNullOnlySchema(branch) {
			branch["type"] = "null"
			changed = true
			continue
		}
		if inferred, inferredOK := inferKimiFunctionSchemaType(branch); inferredOK {
			branch["type"] = inferred
			changed = true
			continue
		}
		if parentTypeIsUsable {
			branch["type"] = parentType
			changed = true
			continue
		}
		allBranchesTyped = false
	}

	if parentHasType && allBranchesTyped {
		delete(schema, "type")
		changed = true
	}
	return changed
}

func isKimiNullOnlySchema(schema map[string]any) bool {
	if value, exists := schema["const"]; exists {
		return value == nil
	}
	values, ok := schema["enum"].([]any)
	if !ok || len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value != nil {
			return false
		}
	}
	return true
}

// normalizeKimiNamedSchemaChildren traverses maps whose keys are names rather
// than JSON Schema keywords, such as properties and $defs. Treating the
// container itself as a schema would corrupt legitimate property names like
// "items", "required", or "format".
func normalizeKimiNamedSchemaChildren(value any, depth int) bool {
	if depth > maxKimiFunctionSchemaDepth {
		return false
	}
	children, ok := value.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for _, child := range children {
		changed = normalizeKimiFunctionSchemaNode(child, depth+1) || changed
	}
	return changed
}

// inferMissingKimiFunctionSchemaType supplies the explicit type required by
// Moonshot's function-schema validator. Standard JSON Schema permits schemas
// such as {"enum":[...]} or {"oneOf":[...]} without a sibling type, but
// Moonshot rejects those nodes with "type is not defined". anyOf is excluded:
// Moonshot requires each anyOf item to define its own type and rejects a type
// on the parent node. Only infer a type when the node's structural keywords or
// literal values make it unambiguous.
func inferMissingKimiFunctionSchemaType(schema map[string]any) bool {
	if schema == nil || !isMissingKimiFunctionSchemaType(schema["type"]) {
		return false
	}
	if _, hasAnyOf := schema["anyOf"]; hasAnyOf {
		return false
	}

	inferred, ok := inferKimiFunctionSchemaType(schema)
	if !ok {
		return false
	}
	schema["type"] = inferred
	return true
}

func isMissingKimiFunctionSchemaType(value any) bool {
	if value == nil {
		return true
	}
	typeName, ok := value.(string)
	if !ok {
		return false
	}
	typeName = strings.TrimSpace(typeName)
	return typeName == "" || strings.EqualFold(typeName, "none")
}

func inferKimiFunctionSchemaType(schema map[string]any) (string, bool) {
	if hasAnyKimiSchemaKeyword(schema,
		"properties", "additionalProperties", "patternProperties", "required",
		"dependentRequired", "propertyNames", "minProperties", "maxProperties",
	) {
		return "object", true
	}
	if hasAnyKimiSchemaKeyword(schema,
		"items", "prefixItems", "contains", "minItems", "maxItems", "uniqueItems",
	) {
		return "array", true
	}
	if hasAnyKimiSchemaKeyword(schema,
		"minLength", "maxLength", "pattern", "format", "contentEncoding", "contentMediaType",
	) {
		return "string", true
	}
	if hasAnyKimiSchemaKeyword(schema,
		"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	) {
		return "number", true
	}

	if enumValues, ok := schema["enum"].([]any); ok {
		if typeName, inferred := inferKimiSchemaTypeFromLiterals(enumValues); inferred {
			return typeName, true
		}
	}
	if constValue, exists := schema["const"]; exists {
		if typeName, inferred := kimiSchemaTypeForLiteral(constValue); inferred && typeName != "null" {
			return typeName, true
		}
	}

	for _, keyword := range []string{"oneOf", "allOf"} {
		if typeName, inferred := inferKimiSchemaTypeFromAlternatives(schema[keyword]); inferred {
			return typeName, true
		}
	}
	return "", false
}

func hasAnyKimiSchemaKeyword(schema map[string]any, keywords ...string) bool {
	for _, keyword := range keywords {
		if _, exists := schema[keyword]; exists {
			return true
		}
	}
	return false
}

func inferKimiSchemaTypeFromLiterals(values []any) (string, bool) {
	candidate := ""
	for _, value := range values {
		typeName, ok := kimiSchemaTypeForLiteral(value)
		if !ok {
			return "", false
		}
		if typeName == "null" {
			continue
		}
		merged, compatible := mergeKimiSchemaTypes(candidate, typeName)
		if !compatible {
			return "", false
		}
		candidate = merged
	}
	return candidate, candidate != ""
}

func inferKimiSchemaTypeFromAlternatives(value any) (string, bool) {
	branches, ok := value.([]any)
	if !ok || len(branches) == 0 {
		return "", false
	}

	candidate := ""
	for _, rawBranch := range branches {
		branch, ok := rawBranch.(map[string]any)
		if !ok {
			return "", false
		}
		typeName, ok := kimiDeclaredSchemaType(branch["type"])
		if !ok {
			return "", false
		}
		if typeName == "null" {
			continue
		}
		merged, compatible := mergeKimiSchemaTypes(candidate, typeName)
		if !compatible {
			return "", false
		}
		candidate = merged
	}
	return candidate, candidate != ""
}

func kimiDeclaredSchemaType(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		typeName := strings.ToLower(strings.TrimSpace(typed))
		if isSupportedKimiSchemaType(typeName) {
			return typeName, true
		}
	case []any:
		candidate := ""
		for _, item := range typed {
			typeName, ok := item.(string)
			if !ok {
				return "", false
			}
			typeName = strings.ToLower(strings.TrimSpace(typeName))
			if !isSupportedKimiSchemaType(typeName) {
				return "", false
			}
			if typeName == "null" {
				continue
			}
			merged, compatible := mergeKimiSchemaTypes(candidate, typeName)
			if !compatible {
				return "", false
			}
			candidate = merged
		}
		if candidate != "" {
			return candidate, true
		}
	}
	return "", false
}

func kimiSchemaTypeForLiteral(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "null", true
	case string:
		return "string", true
	case bool:
		return "boolean", true
	case json.Number:
		if _, err := typed.Int64(); err == nil {
			return "integer", true
		}
		return "number", true
	case float64:
		if math.Trunc(typed) == typed {
			return "integer", true
		}
		return "number", true
	case float32:
		if math.Trunc(float64(typed)) == float64(typed) {
			return "integer", true
		}
		return "number", true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer", true
	case map[string]any:
		return "object", true
	case []any:
		return "array", true
	default:
		return "", false
	}
}

func mergeKimiSchemaTypes(current, next string) (string, bool) {
	if current == "" || current == next {
		return next, true
	}
	if (current == "integer" && next == "number") || (current == "number" && next == "integer") {
		return "number", true
	}
	return "", false
}

func isSupportedKimiSchemaType(typeName string) bool {
	switch typeName {
	case "object", "array", "string", "integer", "number", "boolean", "null":
		return true
	default:
		return false
	}
}
