package protocol

import (
	"fmt"
	"qc-switch/internal/domain"
)

type Result struct {
	ID, Model, Stop string
	Parts           []Part
	Input, Output   int64
	UsageKnown      bool
}

func ParseResult(p domain.Protocol, b Object) (Result, error) {
	r := Result{ID: Str(b["id"]), Model: Str(b["model"]), Stop: "stop"}
	if b["error"] != nil {
		return r, fmt.Errorf("upstream returned an error")
	}
	u := Obj(b["usage"])
	r.UsageKnown = b["usage"] != nil
	r.Input = Num(u["input_tokens"])
	r.Output = Num(u["output_tokens"])
	switch p {
	case domain.Chat:
		r.Input = Num(u["prompt_tokens"])
		r.Output = Num(u["completion_tokens"])
		choices := Arr(b["choices"])
		if len(choices) == 0 {
			return r, fmt.Errorf("upstream returned no choices")
		}
		c := Obj(choices[0])
		m := Obj(c["message"])
		r.Stop = Str(c["finish_reason"])
		ps, e := content(m["content"])
		if e != nil {
			return r, e
		}
		r.Parts = ps
		if s := Str(m["reasoning_content"]); s != "" {
			r.Parts = append([]Part{{Kind: "reasoning", Text: s}}, r.Parts...)
		}
		for _, v := range Arr(m["tool_calls"]) {
			t := Obj(v)
			f := Obj(t["function"])
			r.Parts = append(r.Parts, Part{Kind: "call", ID: Str(t["id"]), Name: Str(f["name"]), Args: Str(f["arguments"])})
		}
	case domain.Messages:
		r.Stop = Str(b["stop_reason"])
		ps, e := content(b["content"])
		if e != nil {
			return r, e
		}
		r.Parts = ps
	case domain.Responses:
		if Str(b["status"]) == "failed" {
			return r, fmt.Errorf("upstream response failed")
		}
		if Str(b["status"]) == "incomplete" {
			r.Stop = "length"
		}
		for _, v := range Arr(b["output"]) {
			item := Obj(v)
			switch Str(item["type"]) {
			case "message":
				ps, e := content(item["content"])
				if e != nil {
					return r, e
				}
				r.Parts = append(r.Parts, ps...)
			case "function_call":
				r.Parts = append(r.Parts, Part{Kind: "call", ID: callID(item), Name: Str(item["name"]), Args: Str(item["arguments"])})
			case "reasoning":
				for _, summary := range Arr(item["summary"]) {
					r.Parts = append(r.Parts, Part{Kind: "reasoning", Text: Str(Obj(summary)["text"])})
				}
			default:
				return r, fmt.Errorf("unsupported upstream output item %q", Str(item["type"]))
			}
		}
	}
	return r, nil
}

func toolFor(ctx Context, name string) Tool {
	if t, ok := ctx.Tools[name]; ok {
		return t
	}
	return Tool{Name: name, WireName: name, Kind: "function"}
}
func responseCall(p Part, ctx Context) Object {
	t := toolFor(ctx, p.Name)
	item := Object{"type": "function_call", "id": "fc_" + p.ID, "call_id": p.ID, "name": t.Name, "arguments": p.Args, "status": "completed"}
	if t.Namespace != "" {
		item["namespace"] = t.Namespace
	}
	if t.Kind == "custom" {
		item["type"] = "custom_tool_call"
		delete(item, "arguments")
		var args Object
		_ = unmarshalObject(p.Args, &args)
		item["input"] = Str(args["input"])
	}
	return item
}
func RenderResult(p domain.Protocol, r Result, ctx Context) Object {
	if r.ID == "" {
		r.ID = "qc_response"
	}
	switch p {
	case domain.Messages:
		parts := []any{}
		hasTools := false
		for _, part := range r.Parts {
			switch part.Kind {
			case "text":
				parts = append(parts, Object{"type": "text", "text": part.Text})
			case "reasoning": // Omit unsigned cross-provider reasoning; do not forge signed thinking state.
			case "call":
				hasTools = true
				var args any
				_ = unmarshalAny(part.Args, &args)
				t := toolFor(ctx, part.Name)
				parts = append(parts, Object{"type": "tool_use", "id": part.ID, "name": t.Name, "input": args})
			}
		}
		stop := "end_turn"
		if hasTools {
			stop = "tool_use"
		}
		if r.Stop == "length" || r.Stop == "max_tokens" {
			stop = "max_tokens"
		}
		return Object{"id": r.ID, "type": "message", "role": "assistant", "model": r.Model, "content": parts, "stop_reason": stop, "stop_sequence": nil, "usage": Object{"input_tokens": r.Input, "output_tokens": r.Output}}
	case domain.Responses:
		output := []any{}
		for i, part := range r.Parts {
			switch part.Kind {
			case "text":
				output = append(output, Object{"id": fmt.Sprintf("msg_%s_%d", r.ID, i), "type": "message", "role": "assistant", "status": "completed", "content": []any{Object{"type": "output_text", "text": part.Text, "annotations": []any{}}}})
			case "reasoning":
				output = append(output, Object{"id": fmt.Sprintf("rs_%s_%d", r.ID, i), "type": "reasoning", "summary": []any{Object{"type": "summary_text", "text": part.Text}}})
			case "call":
				output = append(output, responseCall(part, ctx))
			}
		}
		status := "completed"
		var details any
		if r.Stop == "length" || r.Stop == "max_tokens" {
			status = "incomplete"
			details = Object{"reason": "max_output_tokens"}
		}
		return Object{"id": r.ID, "object": "response", "model": r.Model, "status": status, "output": output, "error": nil, "incomplete_details": details, "usage": Object{"input_tokens": r.Input, "output_tokens": r.Output, "total_tokens": r.Input + r.Output}}
	}
	return nil
}
