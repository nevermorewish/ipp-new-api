package openaigpt

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// NormalizeResponsesCompatibility repairs missing namespaces using only tool
// declarations visible at the call's position. Azure compatibility is opt-in:
// it discards encrypted history and stale optional tool-call IDs, but never
// changes tool schemas. Reserved tools require their original schema, including
// encryption annotations; these annotations are not encrypted history.
// UseNumber preserves unrelated large integers when rewriting pass-through JSON.
func NormalizeResponsesCompatibility(body []byte, removeAzureEncryption bool) ([]byte, bool, error) {
	var root map[string]any
	if err := common.UnmarshalUseNumber(body, &root); err != nil {
		return nil, false, err
	}
	n := responsesCompatibility{removeEncryption: removeAzureEncryption, namespaces: make(map[string]map[string]struct{})}
	if err := n.tools(root["tools"], "", 0); err != nil {
		return nil, false, err
	}
	if removeAzureEncryption {
		if include, ok := root["include"].([]any); ok {
			kept := make([]any, 0, len(include))
			for _, value := range include {
				if value == "reasoning.encrypted_content" {
					n.changed = true
					continue
				}
				kept = append(kept, value)
			}
			root["include"] = kept
		}
	}
	if input, ok := root["input"].([]any); ok {
		// References are protocol items, not arbitrary IDs inside tool arguments
		// or schema examples. Do not leave one pointing at removed history/IDs.
		n.references = make(map[string]struct{})
		for _, value := range input {
			if item, ok := value.(map[string]any); ok && item["type"] == "item_reference" {
				if id, ok := item["id"].(string); ok {
					n.references[id] = struct{}{}
				}
			}
		}
		items, err := n.items(input, 0)
		if err != nil {
			return nil, false, err
		}
		root["input"] = items
	}
	if !n.changed {
		return body, false, nil
	}
	result, err := common.Marshal(root)
	return result, true, err
}

type responsesCompatibility struct {
	removeEncryption bool
	changed          bool
	namespaces       map[string]map[string]struct{}
	references       map[string]struct{}
}

// NormalizeAzureGPTChatEncryption covers native Chat requests, including the
// pass-through path, before an Azure upstream converts them to Responses.
// It removes encrypted history while preserving tool schema declarations.
func NormalizeAzureGPTChatEncryption(body []byte, enabled bool) ([]byte, bool, error) {
	if !enabled {
		return body, false, nil
	}
	var root map[string]any
	if err := common.UnmarshalUseNumber(body, &root); err != nil {
		return nil, false, err
	}
	n := responsesCompatibility{removeEncryption: true, namespaces: make(map[string]map[string]struct{})}
	if err := n.tools(root["tools"], "", 0); err != nil {
		return nil, false, err
	}
	if messages, ok := root["messages"].([]any); ok {
		cleaned, err := n.items(messages, 0)
		if err != nil {
			return nil, false, err
		}
		root["messages"] = cleaned
	}
	if !n.changed {
		return body, false, nil
	}
	result, err := common.Marshal(root)
	return result, true, err
}

func (n *responsesCompatibility) tools(value any, namespace string, depth int) error {
	if depth > maxToolNestingDepth {
		return fmt.Errorf("Responses tools exceed maximum nesting depth of %d", maxToolNestingDepth)
	}
	tools, _ := value.([]any)
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tool["name"].(string)
		if nested, ok := tool["tools"].([]any); ok {
			// Both explicit namespaces and Codex's untyped named containers
			// carry tools. Never turn an unnamed container into a namespace.
			childNamespace := namespace
			if name != "" {
				childNamespace = name
			}
			if err := n.tools(nested, childNamespace, depth+1); err != nil {
				return err
			}
			continue
		}
		if tool["type"] != "function" {
			continue
		}
		if name != "" {
			if n.namespaces[name] == nil {
				n.namespaces[name] = make(map[string]struct{})
			}
			n.namespaces[name][namespace] = struct{}{}
		}
	}
	return nil
}

func (n *responsesCompatibility) items(items []any, depth int) ([]any, error) {
	if depth > maxToolNestingDepth {
		return nil, fmt.Errorf("Responses input exceeds maximum nesting depth of %d", maxToolNestingDepth)
	}
	kept := make([]any, 0, len(items))
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			kept = append(kept, value)
			continue
		}
		typeName, _ := item["type"].(string)
		if n.removeEncryption {
			_, encrypted := item["encrypted_content"]
			if typeName == "encrypted_content" || (typeName == "reasoning" && encrypted) {
				if err := n.checkRemovedID(item); err != nil {
					return nil, err
				}
				n.changed = true
				continue
			}
			// Compaction may be the only remaining conversation history. It
			// cannot safely be replaced with an empty/fictional transcript.
			if typeName == "compaction" && encrypted {
				return nil, fmt.Errorf("Azure GPT compatibility cannot replay encrypted compaction; resend uncompressed plaintext history")
			}
			// A replayed tool call is associated with its output by call_id, not
			// the optional item id. Omit deprecated item_* IDs instead of forging
			// fc_* IDs. Reasoning IDs are not optional tool-call IDs: encrypted
			// reasoning is removed above; other reasoning is left intact.
			if typeName == "function_call" || typeName == "custom_tool_call" {
				if id, ok := item["id"].(string); ok && strings.HasPrefix(id, "item_") {
					if err := n.checkRemovedID(item); err != nil {
						return nil, err
					}
					delete(item, "id")
					n.changed = true
				}
			}
		}
		switch typeName {
		case "additional_tools", "tool_search_output":
			if err := n.tools(item["tools"], "", 0); err != nil {
				return nil, err
			}
		case "function_call":
			name, _ := item["name"].(string)
			namespace, exists := item["namespace"]
			if !exists || namespace == nil || namespace == "" {
				candidates := n.namespaces[name]
				// A declared default-namespace function is already a valid
				// target. Do not reinterpret it as a namespaced function.
				if _, isDefault := candidates[""]; !isDefault && len(candidates) > 1 {
					return nil, fmt.Errorf("function_call %q is missing namespace and matches multiple namespaces; resend the original namespace", name)
				} else if !isDefault && len(candidates) == 1 {
					for candidate := range candidates {
						item["namespace"] = candidate
						n.changed = true
					}
				}
			}
		}
		// Visit protocol content only. Never interpret arguments, schema
		// examples, metadata, or string tool results as protocol objects.
		if typeName == "message" || typeName == "agent_message" || (typeName == "" && item["role"] != nil) {
			if content, ok := item["content"].([]any); ok {
				cleaned, err := n.items(content, depth+1)
				if err != nil {
					return nil, err
				}
				if len(content) > 0 && len(cleaned) == 0 {
					continue
				}
				item["content"] = cleaned
			}
		}
		if n.removeEncryption && (typeName == "function_call_output" || typeName == "custom_tool_call_output") {
			if _, exists := item["encrypted_content"]; exists {
				delete(item, "encrypted_content")
				n.changed = true
				if _, hasOutput := item["output"]; !hasOutput {
					item["output"] = ""
				}
			}
			if output, ok := item["output"].([]any); ok {
				cleaned := make([]any, 0, len(output))
				for _, content := range output {
					if block, ok := content.(map[string]any); ok && block["type"] == "encrypted_content" {
						n.changed = true
						continue
					}
					// Tool results are data, not replayed input items. Do not
					// infer namespaces or discover tools inside their payloads.
					cleaned = append(cleaned, content)
				}
				item["output"] = cleaned
				if len(output) > 0 && len(cleaned) == 0 {
					// Preserve call_id and its matching result even when the
					// user opted to remove its entire encrypted payload.
					item["output"] = ""
				}
			}
		}
		kept = append(kept, item)
	}
	return kept, nil
}

func (n *responsesCompatibility) checkRemovedID(item map[string]any) error {
	if id, ok := item["id"].(string); ok {
		if _, referenced := n.references[id]; referenced {
			return fmt.Errorf("Azure GPT compatibility cannot remove a referenced history item; resend full plaintext history without stale item_reference entries")
		}
	}
	return nil
}
