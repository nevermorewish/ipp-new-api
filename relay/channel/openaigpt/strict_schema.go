package openaigpt

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// maxStrictSchemaDepth bounds recursion through a JSON Schema so a deeply
// nested or self-referential-looking payload cannot exhaust the stack. It is
// deliberately generous: OpenAI's own structured-output limit is five levels.
const maxStrictSchemaDepth = 64

// ValidateStrictSchema enforces the two requirements the official API places on
// a schema submitted with strict:true, plus the one implied by them:
//
//   - additionalProperties must be set to false for every object,
//   - every key in properties must be listed in required,
//   - required must not name a property that properties does not declare.
//
// Quoting docs/openai/13-function-calling.html: "If you send strict: true and
// your schema does not meet the requirements above, the request will be
// rejected with details about the missing constraints." That rejection is what
// this reproduces locally, so a schema no provider can honour fails before it
// costs a generation instead of returning HTTP 200 with unconstrained text.
//
// Only strict schemas are checked here. The final-body compatibility path may
// close object schemas in non-strict response formats, but it deliberately
// does not apply these required-field validation rules.
func ValidateStrictSchema(schema any, path string) error {
	return validateStrictSchemaNode(schema, path, 0)
}

func validateStrictSchemaNode(node any, path string, depth int) error {
	if depth > maxStrictSchemaDepth {
		return fmt.Errorf("%s exceeds the maximum schema nesting depth of %d", path, maxStrictSchemaDepth)
	}
	switch typed := node.(type) {
	case []any:
		// anyOf/oneOf/allOf branches and prefixItems tuples are arrays of
		// schemas; each branch has to satisfy strict mode on its own.
		for index, branch := range typed {
			if err := validateStrictSchemaNode(branch, fmt.Sprintf("%s[%d]", path, index), depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		return validateStrictSchemaObject(typed, path, depth)
	default:
		// Booleans and scalars appear as leaf schemas ("additionalProperties":
		// false) or as keyword values. Nothing to check.
		return nil
	}
}

func validateStrictSchemaObject(schema map[string]any, path string, depth int) error {
	if isStrictObjectSchema(schema) {
		if err := validateStrictObjectConstraints(schema, path); err != nil {
			return err
		}
	}

	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, name := range sortedKeys(properties) {
			child := fmt.Sprintf("%s.properties.%s", path, name)
			if err := validateStrictSchemaNode(properties[name], child, depth+1); err != nil {
				return err
			}
		}
	}

	// Every keyword that can carry a nested schema. additionalProperties is
	// excluded on purpose: under strict mode it is always the literal false
	// checked above, never a schema to recurse into.
	for _, keyword := range []string{"items", "prefixItems", "anyOf", "oneOf", "allOf", "not", "contains"} {
		nested, exists := schema[keyword]
		if !exists {
			continue
		}
		if err := validateStrictSchemaNode(nested, path+"."+keyword, depth+1); err != nil {
			return err
		}
	}
	if defs, ok := schema["$defs"].(map[string]any); ok {
		for _, name := range sortedKeys(defs) {
			if err := validateStrictSchemaNode(defs[name], fmt.Sprintf("%s.$defs.%s", path, name), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// isStrictObjectSchema reports whether a node is an object schema subject to the
// strict constraints. A node counts as one when it says so via type, or when it
// declares properties, which only an object schema does.
//
// A bare {"$ref": "#/$defs/x"} is not an object schema: the constraints belong
// to the definition it points at, which is validated where it is declared.
func isStrictObjectSchema(schema map[string]any) bool {
	if _, hasProperties := schema["properties"]; hasProperties {
		return true
	}
	switch typeValue := schema["type"].(type) {
	case string:
		return typeValue == "object"
	case []any:
		// A nullable object is declared as "type": ["object", "null"].
		for _, entry := range typeValue {
			if name, _ := entry.(string); name == "object" {
				return true
			}
		}
	}
	return false
}

func validateStrictObjectConstraints(schema map[string]any, path string) error {
	additional, exists := schema["additionalProperties"]
	if !exists {
		return fmt.Errorf("%s must set additionalProperties to false when strict is true", path)
	}
	allowsAdditional, isBool := additional.(bool)
	if !isBool || allowsAdditional {
		return fmt.Errorf("%s.additionalProperties must be false when strict is true", path)
	}

	properties, _ := schema["properties"].(map[string]any)
	required, err := strictRequiredNames(schema, path)
	if err != nil {
		return err
	}

	for _, name := range sortedKeys(properties) {
		if _, ok := required[name]; !ok {
			return fmt.Errorf("%s.required must list every declared property when strict is true; %q is missing", path, name)
		}
	}
	for _, name := range sortedNames(required) {
		if _, ok := properties[name]; !ok {
			return fmt.Errorf("%s.required names %q, which is not declared in properties", path, name)
		}
	}
	return nil
}

func strictRequiredNames(schema map[string]any, path string) (map[string]struct{}, error) {
	names := make(map[string]struct{})
	raw, exists := schema["required"]
	if !exists {
		return names, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s.required must be an array of property names", path)
	}
	for index, entry := range entries {
		name, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf("%s.required[%d] must be a string", path, index)
		}
		names[name] = struct{}{}
	}
	return names, nil
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNames(values map[string]struct{}) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// isStrictTrue reports whether a raw JSON value is the boolean true. A missing
// or non-boolean strict flag is not strict mode, so the schema is left alone.
func isStrictTrue(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var strict bool
	if err := common.Unmarshal(raw, &strict); err != nil {
		return false
	}
	return strict
}

// isStrictTrueValue is the decoded-map equivalent of isStrictTrue.
func isStrictTrueValue(value any) bool {
	strict, ok := value.(bool)
	return ok && strict
}

// validateStrictTextFormat checks the Responses text.format object. It is
// called with the already-decoded format so validateText does not decode twice.
func validateStrictTextFormat(format map[string]json.RawMessage) error {
	if !isStrictTrue(format["strict"]) {
		return nil
	}
	rawSchema, exists := format["schema"]
	if !exists {
		return nil // absence is already reported by the json_schema check.
	}
	var schema any
	if err := common.Unmarshal(rawSchema, &schema); err != nil {
		return fmt.Errorf("text.format.schema must be a valid JSON Schema: %w", err)
	}
	return ValidateStrictSchema(schema, "text.format.schema")
}

// validateStrictFunctionParameters checks a function tool declared with
// strict:true. It is shared by the Responses tool list and the Chat tool list,
// which nest the same object one level apart.
func validateStrictFunctionParameters(tool map[string]any, path string) error {
	if !isStrictTrueValue(tool["strict"]) {
		return nil
	}
	parameters, exists := tool["parameters"]
	if !exists {
		return nil
	}
	return ValidateStrictSchema(parameters, path+".parameters")
}

// validateChatResponseFormat mirrors validateStrictTextFormat for the Chat
// response_format.json_schema shape.
func validateChatResponseFormat(formatType string, jsonSchema json.RawMessage) error {
	if !strings.EqualFold(formatType, "json_schema") {
		return nil
	}
	if len(jsonSchema) == 0 || common.GetJsonType(jsonSchema) == "null" {
		return fmt.Errorf("response_format.json_schema is required when response_format.type is json_schema")
	}
	var declared struct {
		Schema any             `json:"schema"`
		Strict json.RawMessage `json:"strict"`
	}
	if err := common.Unmarshal(jsonSchema, &declared); err != nil {
		return fmt.Errorf("response_format.json_schema must be an object: %w", err)
	}
	if !isStrictTrue(declared.Strict) || declared.Schema == nil {
		return nil
	}
	return ValidateStrictSchema(declared.Schema, "response_format.json_schema.schema")
}
