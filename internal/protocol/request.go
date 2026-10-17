// Package protocol implements coding-client protocol adapters. Design and edge
// cases are informed by cc-switch (MIT); see THIRD_PARTY_NOTICES.md.
package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"qc-switch/internal/domain"
)

type Object = map[string]any

func Obj(v any) Object {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return Object{}
}
func Arr(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}
func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
func Num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}
func JSON(v any) string { b, _ := json.Marshal(v); return string(b) }

type Part struct {
	Kind, Text, URL, ID, Name, Args string
	Data                            any
}
type Message struct {
	Role  string
	Parts []Part
}
type Tool struct {
	Name, WireName, Namespace, Kind, Description string
	Schema                                       any
}
type Request struct {
	Model      string
	Stream     bool
	System     string
	Messages   []Message
	Tools      []Tool
	MaxTokens  int64
	Effort     string
	Extras     Object
	ToolChoice any
}
type Context struct{ Tools map[string]Tool }

func content(v any) ([]Part, error) {
	if s, ok := v.(string); ok {
		return []Part{{Kind: "text", Text: s}}, nil
	}
	out := []Part{}
	for _, item := range Arr(v) {
		b := Obj(item)
		switch Str(b["type"]) {
		case "text", "input_text", "output_text":
			out = append(out, Part{Kind: "text", Text: Str(b["text"])})
		case "image":
			s := Obj(b["source"])
			u := Str(s["url"])
			if Str(s["type"]) == "base64" {
				u = "data:" + Str(s["media_type"]) + ";base64," + Str(s["data"])
			}
			if u == "" {
				return nil, fmt.Errorf("image has no supported source")
			}
			out = append(out, Part{Kind: "image", URL: u})
		case "image_url", "input_image":
			u := Str(b["image_url"])
			if u == "" {
				u = Str(Obj(b["image_url"])["url"])
			}
			if u == "" {
				return nil, fmt.Errorf("image URL is required")
			}
			out = append(out, Part{Kind: "image", URL: u})
		case "tool_use":
			out = append(out, Part{Kind: "call", ID: Str(b["id"]), Name: Str(b["name"]), Args: JSON(b["input"])})
		case "tool_result":
			p, e := content(b["content"])
			if e != nil {
				return nil, e
			}
			out = append(out, Part{Kind: "result", ID: Str(b["tool_use_id"]), Data: p})
		case "thinking":
			out = append(out, Part{Kind: "reasoning", Text: Str(b["thinking"])})
		case "redacted_thinking": // Opaque provider state cannot be replayed to a different protocol.
		default:
			return nil, fmt.Errorf("unsupported content block %q for protocol conversion", Str(b["type"]))
		}
	}
	return out, nil
}

func Decode(p domain.Protocol, b Object) (Request, error) {
	r := Request{Model: Str(b["model"]), Stream: b["stream"] == true, Extras: Object{}, ToolChoice: b["tool_choice"]}
	for _, k := range []string{"temperature", "top_p", "stop", "parallel_tool_calls"} {
		if v, ok := b[k]; ok {
			r.Extras[k] = v
		}
	}
	r.MaxTokens = Num(b["max_tokens"])
	if r.MaxTokens == 0 {
		r.MaxTokens = Num(b["max_output_tokens"])
	}
	if r.MaxTokens == 0 {
		r.MaxTokens = Num(b["max_completion_tokens"])
	}
	r.Effort = Str(b["reasoning_effort"])
	if r.Effort == "" {
		r.Effort = Str(Obj(b["reasoning"])["effort"])
	}
	if r.Effort == "" && p == domain.Messages {
		r.Effort = Str(Obj(b["output_config"])["effort"])
	}
	if p == domain.Messages {
		parts, e := content(b["system"])
		if e != nil {
			return r, e
		}
		r.System = Text(parts)
	}
	if p == domain.Responses {
		r.System = Str(b["instructions"])
		if Str(b["previous_response_id"]) != "" {
			return r, fmt.Errorf("cross-protocol routing requires full conversation input; restart this session without previous_response_id")
		}
		if s, ok := b["input"].(string); ok {
			r.Messages = append(r.Messages, Message{Role: "user", Parts: []Part{{Kind: "text", Text: s}}})
		} else {
			for _, item := range Arr(b["input"]) {
				m := Obj(item)
				switch Str(m["type"]) {
				case "", "message":
					parts, e := content(m["content"])
					if e != nil {
						return r, e
					}
					role := Str(m["role"])
					if role == "system" || role == "developer" {
						r.System += "\n" + Text(parts)
					} else {
						r.Messages = append(r.Messages, Message{Role: role, Parts: parts})
					}
				case "function_call":
					name := Str(m["name"])
					if ns := Str(m["namespace"]); ns != "" {
						name = ns + "." + name
					}
					r.Messages = append(r.Messages, Message{Role: "assistant", Parts: []Part{{Kind: "call", ID: callID(m), Name: name, Args: Str(m["arguments"])}}})
				case "custom_tool_call":
					r.Messages = append(r.Messages, Message{Role: "assistant", Parts: []Part{{Kind: "call", ID: callID(m), Name: Str(m["name"]), Args: JSON(Object{"input": m["input"]})}}})
				case "function_call_output", "custom_tool_call_output":
					parts, e := outputParts(m["output"])
					if e != nil {
						return r, e
					}
					r.Messages = append(r.Messages, Message{Role: "tool", Parts: []Part{{Kind: "result", ID: callID(m), Data: parts}}})
				case "reasoning":
					for _, v := range Arr(m["summary"]) {
						if text := Str(Obj(v)["text"]); text != "" {
							r.Messages = append(r.Messages, Message{Role: "assistant", Parts: []Part{{Kind: "reasoning", Text: text}}})
						}
					}
				case "compaction":
					return r, fmt.Errorf("remote compaction state cannot be converted; use local compaction")
				default:
					return r, fmt.Errorf("unsupported Responses input item %q", Str(m["type"]))
				}
			}
		}
	} else {
		for _, item := range Arr(b["messages"]) {
			m := Obj(item)
			parts, e := content(m["content"])
			if e != nil {
				return r, e
			}
			role := Str(m["role"])
			if role == "system" || role == "developer" {
				r.System += Text(parts) + "\n"
				continue
			}
			if role == "tool" {
				parts = []Part{{Kind: "result", ID: Str(m["tool_call_id"]), Data: parts}}
			}
			for _, v := range Arr(m["tool_calls"]) {
				t := Obj(v)
				f := Obj(t["function"])
				parts = append(parts, Part{Kind: "call", ID: Str(t["id"]), Name: Str(f["name"]), Args: Str(f["arguments"])})
			}
			if text := Str(m["reasoning_content"]); text != "" {
				parts = append([]Part{{Kind: "reasoning", Text: text}}, parts...)
			}
			r.Messages = append(r.Messages, Message{Role: role, Parts: parts})
		}
	}
	var addTool func(Object, string) error
	addTool = func(t Object, ns string) error {
		kind := Str(t["type"])
		if kind == "namespace" {
			for _, v := range Arr(t["tools"]) {
				if e := addTool(Obj(v), Str(t["name"])); e != nil {
					return e
				}
			}
			return nil
		}
		if kind != "" && kind != "function" && kind != "custom" {
			return fmt.Errorf("server-side tool %q is not supported by this protocol bridge", kind)
		}
		f := t
		if nested, ok := t["function"]; ok {
			f = Obj(nested)
		}
		name := Str(f["name"])
		if name == "" {
			return fmt.Errorf("tool name is empty")
		}
		schema := f["parameters"]
		if p == domain.Messages {
			schema = f["input_schema"]
		}
		if schema == nil {
			schema = Object{"type": "object", "properties": Object{}}
		}
		desc := Str(f["description"])
		if kind == "custom" {
			schema = Object{"type": "object", "properties": Object{"input": Object{"type": "string", "description": "Raw tool input, preserving all formatting."}}, "required": []string{"input"}, "additionalProperties": false}
			desc += "\nOriginal tool format: " + JSON(f["format"])
		}
		full := name
		if ns != "" {
			full = ns + "." + name
		}
		sum := sha256.Sum256([]byte(full))
		wire := name
		if ns != "" || len(wire) > 55 {
			wire = "qc_" + hex.EncodeToString(sum[:12])
		}
		if kind == "" {
			kind = "function"
		}
		r.Tools = append(r.Tools, Tool{Name: name, WireName: wire, Namespace: ns, Kind: kind, Description: desc, Schema: schema})
		return nil
	}
	for _, v := range Arr(b["tools"]) {
		if e := addTool(Obj(v), ""); e != nil {
			return r, e
		}
	}
	return r, nil
}

func callID(m Object) string {
	if s := Str(m["call_id"]); s != "" {
		return s
	}
	return Str(m["id"])
}
func outputParts(v any) ([]Part, error) {
	if _, ok := v.([]any); ok {
		return content(v)
	}
	if s, ok := v.(string); ok {
		return []Part{{Kind: "text", Text: s}}, nil
	}
	return []Part{{Kind: "text", Text: JSON(v)}}, nil
}
func Text(parts []Part) string {
	var out strings.Builder
	for _, p := range parts {
		if p.Kind == "text" || p.Kind == "reasoning" {
			out.WriteString(p.Text)
		}
	}
	return out.String()
}
func lookupTool(r Request, name string) Tool {
	for _, t := range r.Tools {
		if name == t.Name || name == t.WireName || name == t.Namespace+"."+t.Name {
			return t
		}
	}
	return Tool{Name: name, WireName: name, Kind: "function"}
}
func partsValue(parts []Part) any {
	a := []any{}
	for _, p := range parts {
		switch p.Kind {
		case "text":
			a = append(a, Object{"type": "text", "text": p.Text})
		case "image":
			a = append(a, Object{"type": "image_url", "image_url": Object{"url": p.URL}})
		}
	}
	if len(a) == 1 && Str(Obj(a[0])["type"]) == "text" {
		return Obj(a[0])["text"]
	}
	return a
}
func imageSource(u string) Object {
	if strings.HasPrefix(u, "data:") {
		meta, data, ok := strings.Cut(u[5:], ",")
		if ok && strings.HasSuffix(meta, ";base64") {
			if _, e := base64.StdEncoding.DecodeString(data); e == nil {
				return Object{"type": "base64", "media_type": strings.TrimSuffix(meta, ";base64"), "data": data}
			}
		}
	}
	return Object{"type": "url", "url": u}
}

func Encode(p domain.Protocol, r Request) (Object, Context, error) {
	b := Object{"model": r.Model, "stream": r.Stream}
	ctx := Context{Tools: map[string]Tool{}}
	for _, t := range r.Tools {
		ctx.Tools[t.WireName] = t
	}
	for k, v := range r.Extras {
		if k == "parallel_tool_calls" && p == domain.Messages {
			continue
		}
		b[k] = v
	}
	tools := []any{}
	for _, t := range r.Tools {
		switch p {
		case domain.Messages:
			tools = append(tools, Object{"name": t.WireName, "description": t.Description, "input_schema": t.Schema})
		case domain.Responses:
			tools = append(tools, Object{"type": "function", "name": t.WireName, "description": t.Description, "parameters": t.Schema})
		case domain.Chat:
			tools = append(tools, Object{"type": "function", "function": Object{"name": t.WireName, "description": t.Description, "parameters": t.Schema}})
		}
	}
	if len(tools) > 0 {
		b["tools"] = tools
	}
	if r.ToolChoice != nil {
		choice := Str(r.ToolChoice)
		if choice == "" {
			o := Obj(r.ToolChoice)
			choice = Str(o["type"])
			if choice == "any" {
				choice = "required"
			}
			name := Str(o["name"])
			if name == "" {
				name = Str(Obj(o["function"])["name"])
			}
			if name != "" {
				t := lookupTool(r, name)
				switch p {
				case domain.Messages:
					b["tool_choice"] = Object{"type": "tool", "name": t.WireName}
				case domain.Responses:
					b["tool_choice"] = Object{"type": "function", "name": t.WireName}
				default:
					b["tool_choice"] = Object{"type": "function", "function": Object{"name": t.WireName}}
				}
			}
		}
		if b["tool_choice"] == nil && choice != "" {
			if p == domain.Messages {
				if choice == "required" {
					choice = "any"
				}
				b["tool_choice"] = Object{"type": choice}
			} else {
				b["tool_choice"] = choice
			}
		}
	}
	if r.MaxTokens > 0 {
		switch p {
		case domain.Messages:
			b["max_tokens"] = r.MaxTokens
		case domain.Responses:
			b["max_output_tokens"] = r.MaxTokens
		case domain.Chat:
			b["max_completion_tokens"] = r.MaxTokens
		}
	} else if p == domain.Messages {
		b["max_tokens"] = 8192
	}
	if r.Effort != "" {
		if p == domain.Responses {
			b["reasoning"] = Object{"effort": r.Effort}
		} else if p == domain.Chat {
			b["reasoning_effort"] = r.Effort
		} else {
			b["thinking"] = Object{"type": "adaptive"}
			b["output_config"] = Object{"effort": r.Effort}
		}
	}
	items := []any{}
	if r.System != "" {
		switch p {
		case domain.Messages:
			b["system"] = r.System
		case domain.Responses:
			b["instructions"] = r.System
		default:
			items = append(items, Object{"role": "system", "content": r.System})
		}
	}
	// Responses represents parallel calls as adjacent output items. Chat requires
	// them in one assistant message before the matching tool result messages.
	messages := []Message{}
	for _, m := range r.Messages {
		if p == domain.Chat && m.Role == "assistant" && len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
			messages[len(messages)-1].Parts = append(messages[len(messages)-1].Parts, m.Parts...)
		} else {
			messages = append(messages, m)
		}
	}
	for _, m := range messages {
		switch p {
		case domain.Chat:
			text := []Part{}
			calls := []any{}
			reason := ""
			flush := func() {
				if len(text) > 0 || len(calls) > 0 || reason != "" {
					role := m.Role
					if role == "tool" || role == "" {
						role = "user"
					}
					msg := Object{"role": role, "content": partsValue(text)}
					if len(text) == 0 {
						msg["content"] = nil
					}
					if len(calls) > 0 {
						msg["tool_calls"] = calls
					}
					if reason != "" {
						msg["reasoning_content"] = reason
					}
					items = append(items, msg)
					text = nil
					calls = nil
					reason = ""
				}
			}
			for _, part := range m.Parts {
				switch part.Kind {
				case "text", "image":
					text = append(text, part)
				case "reasoning":
					reason += part.Text
				case "call":
					t := lookupTool(r, part.Name)
					calls = append(calls, Object{"id": part.ID, "type": "function", "function": Object{"name": t.WireName, "arguments": part.Args}})
				case "result":
					flush()
					ps, _ := part.Data.([]Part)
					items = append(items, Object{"role": "tool", "tool_call_id": part.ID, "content": Text(ps)})
					media := []Part{}
					for _, v := range ps {
						if v.Kind == "image" {
							media = append(media, v)
						}
					}
					if len(media) > 0 {
						items = append(items, Object{"role": "user", "content": partsValue(media)})
					}
				}
			}
			flush()
		case domain.Messages:
			parts := []any{}
			role := m.Role
			if role == "tool" {
				role = "user"
			}
			for _, part := range m.Parts {
				switch part.Kind {
				case "text":
					parts = append(parts, Object{"type": "text", "text": part.Text})
				case "image":
					parts = append(parts, Object{"type": "image", "source": imageSource(part.URL)})
				case "call":
					t := lookupTool(r, part.Name)
					var args any
					if e := json.Unmarshal([]byte(part.Args), &args); e != nil {
						return nil, ctx, fmt.Errorf("invalid tool arguments for %s", part.Name)
					}
					parts = append(parts, Object{"type": "tool_use", "id": part.ID, "name": t.WireName, "input": args})
				case "result":
					ps, _ := part.Data.([]Part)
					result := []any{}
					for _, v := range ps {
						if v.Kind == "text" {
							result = append(result, Object{"type": "text", "text": v.Text})
						} else if v.Kind == "image" {
							result = append(result, Object{"type": "image", "source": imageSource(v.URL)})
						}
					}
					parts = append(parts, Object{"type": "tool_result", "tool_use_id": part.ID, "content": result})
				}
			}
			if len(parts) > 0 {
				if len(items) > 0 && Str(Obj(items[len(items)-1])["role"]) == role {
					last := Obj(items[len(items)-1])
					last["content"] = append(Arr(last["content"]), parts...)
				} else {
					items = append(items, Object{"role": role, "content": parts})
				}
			}
		case domain.Responses:
			parts := []any{}
			flush := func() {
				if len(parts) > 0 {
					role := m.Role
					if role == "tool" {
						role = "user"
					}
					items = append(items, Object{"type": "message", "role": role, "content": parts})
					parts = nil
				}
			}
			for _, part := range m.Parts {
				switch part.Kind {
				case "text":
					kind := "input_text"
					if m.Role == "assistant" {
						kind = "output_text"
					}
					parts = append(parts, Object{"type": kind, "text": part.Text})
				case "image":
					parts = append(parts, Object{"type": "input_image", "image_url": part.URL})
				case "call":
					flush()
					t := lookupTool(r, part.Name)
					items = append(items, Object{"type": "function_call", "call_id": part.ID, "name": t.WireName, "arguments": part.Args})
				case "result":
					flush()
					ps, _ := part.Data.([]Part)
					output := []any{}
					for _, v := range ps {
						if v.Kind == "text" {
							output = append(output, Object{"type": "input_text", "text": v.Text})
						} else if v.Kind == "image" {
							output = append(output, Object{"type": "input_image", "image_url": v.URL})
						}
					}
					var value any = output
					if len(output) == 1 && Str(Obj(output[0])["type"]) == "input_text" {
						value = Obj(output[0])["text"]
					}
					items = append(items, Object{"type": "function_call_output", "call_id": part.ID, "output": value})
				}
			}
			flush()
		}
	}
	if p == domain.Responses {
		b["input"] = items
		b["store"] = false
	} else {
		b["messages"] = items
	}
	if p == domain.Chat && r.Stream {
		b["stream_options"] = Object{"include_usage": true}
	}
	return b, ctx, nil
}

func Convert(from, to domain.Protocol, b Object) (Object, Context, error) {
	r, e := Decode(from, b)
	if e != nil {
		return nil, Context{}, e
	}
	return Encode(to, r)
}
