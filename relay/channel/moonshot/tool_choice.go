package moonshot

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// normalizeRequiredToolChoiceWithoutTools removes the impossible
// OpenAI string form from a pass-through body when no tools were supplied.
// Non-empty tool arrays and non-string tool_choice values are left untouched.
func normalizeRequiredToolChoiceWithoutTools(body []byte) ([]byte, bool) {
	choice := gjson.GetBytes(body, "tool_choice")
	if !choice.Exists() || choice.Type != gjson.String || choice.String() != "required" {
		return body, false
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.Exists() && tools.Type != gjson.Null && (!tools.IsArray() || len(tools.Array()) > 0) {
		return body, false
	}
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		tools := message.Get("tools")
		if tools.Exists() && tools.Type != gjson.Null && (!tools.IsArray() || len(tools.Array()) > 0) {
			return body, false
		}
	}
	normalized, err := sjson.DeleteBytes(append([]byte(nil), body...), "tool_choice")
	if err != nil {
		return body, false
	}
	return normalized, true
}
