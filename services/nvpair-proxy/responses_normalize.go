package main

import (
	"bytes"
	"encoding/json"
)

// normalizeResponsesInput rewrites a /v1/responses body so that developer/system
// messages appearing after the leading run of system/developer items become
// user messages prefixed with "[developer]\n". Some backend chat templates
// (e.g. Qwen's) reject a system message anywhere but the start. Everything
// else is left byte-identical; on any parse problem or no-op the original
// bytes are returned.
func normalizeResponsesInput(body []byte) []byte {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return body
	}
	rawInput, ok := top["input"]
	if !ok {
		return body
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawInput, &items); err != nil {
		return body // string input, null, or other shape
	}
	leading := true
	changed := false
	for i, it := range items {
		var m map[string]json.RawMessage
		if json.Unmarshal(it, &m) != nil {
			leading = false
			continue
		}
		var typ, role string
		_ = json.Unmarshal(m["type"], &typ)
		_ = json.Unmarshal(m["role"], &role)
		isMsg := (typ == "message" || (typ == "" && m["role"] != nil)) && m["role"] != nil
		isSys := isMsg && (role == "developer" || role == "system")
		if leading && isSys {
			continue
		}
		leading = false
		if !isSys || typ != "message" {
			continue
		}
		if !rewriteDeveloperItem(m) {
			continue
		}
		out, err := json.Marshal(m)
		if err != nil {
			return body
		}
		items[i] = out
		changed = true
	}
	if !changed {
		return body
	}
	newInput, err := json.Marshal(items)
	if err != nil {
		return body
	}
	// Splice the new input value over the old one so other top-level fields
	// keep their order and bytes.
	idx := bytes.Index(body, rawInput)
	if idx < 0 || bytes.Count(body, rawInput) != 1 {
		top["input"] = newInput
		out, err := json.Marshal(top)
		if err != nil {
			return body
		}
		return out
	}
	out := make([]byte, 0, len(body)+len(newInput))
	out = append(out, body[:idx]...)
	out = append(out, newInput...)
	out = append(out, body[idx+len(rawInput):]...)
	return out
}

const developerPrefix = "[developer]\n"

// rewriteDeveloperItem sets role=user and prefixes the first text part.
func rewriteDeveloperItem(m map[string]json.RawMessage) bool {
	m["role"] = json.RawMessage(`"user"`)
	var s string
	if json.Unmarshal(m["content"], &s) == nil {
		b, _ := json.Marshal(developerPrefix + s)
		m["content"] = b
		return true
	}
	var parts []json.RawMessage
	if json.Unmarshal(m["content"], &parts) != nil {
		return false
	}
	for i, p := range parts {
		var pm map[string]json.RawMessage
		if json.Unmarshal(p, &pm) != nil {
			continue
		}
		var text string
		if json.Unmarshal(pm["text"], &text) != nil {
			continue
		}
		tb, _ := json.Marshal(developerPrefix + text)
		pm["text"] = tb
		pm["type"] = json.RawMessage(`"input_text"`)
		nb, err := json.Marshal(pm)
		if err != nil {
			return false
		}
		parts[i] = nb
		cb, err := json.Marshal(parts)
		if err != nil {
			return false
		}
		m["content"] = cb
		return true
	}
	return false
}
