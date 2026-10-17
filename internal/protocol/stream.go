package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"qc-switch/internal/domain"
)

func unmarshalObject(s string, v *Object) error { return json.Unmarshal([]byte(s), v) }
func unmarshalAny(s string, v *any) error       { return json.Unmarshal([]byte(s), v) }

// ReadSSE handles arbitrary byte fragmentation, UTF-8 splits, CRLF, comments,
// and multi-line data. done stops reading immediately at a terminal boundary.
func ReadSSE(r io.Reader, consume func(string, []byte) (bool, error)) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 16*1024*1024)
	event := ""
	data := []string{}
	dispatch := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		done, e := consume(event, []byte(strings.Join(data, "\n")))
		event = ""
		data = nil
		return done, e
	}
	for s.Scan() {
		line := strings.TrimSuffix(s.Text(), "\r")
		if line == "" {
			done, e := dispatch()
			if e != nil {
				return e
			}
			if done {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		k, v, _ := strings.Cut(line, ":")
		v = strings.TrimPrefix(v, " ")
		switch k {
		case "event":
			event = v
		case "data":
			data = append(data, v)
		}
	}
	if e := s.Err(); e != nil {
		return e
	}
	done, e := dispatch()
	if e != nil {
		return e
	}
	if done {
		return nil
	}
	return io.ErrUnexpectedEOF
}

type Stream struct {
	Target   domain.Protocol
	Ctx      Context
	Result   Result
	write    func(string, Object) error
	started  bool
	seq      int
	parts    []Part
	indices  map[string]int
	opened   map[int]bool
	finished bool
}

func NewStream(target domain.Protocol, model, id string, ctx Context, write func(string, Object) error) *Stream {
	return &Stream{Target: target, Ctx: ctx, Result: Result{ID: id, Model: model, Stop: "stop"}, write: write, indices: map[string]int{}, opened: map[int]bool{}}
}
func (s *Stream) emit(name string, o Object) error {
	if s.Target == domain.Responses {
		o["type"] = name
		o["sequence_number"] = s.seq
		s.seq++
	}
	return s.write(name, o)
}
func (s *Stream) start() error {
	if s.started {
		return nil
	}
	s.started = true
	if s.Target == domain.Messages {
		return s.emit("message_start", Object{"type": "message_start", "message": Object{"id": s.Result.ID, "type": "message", "role": "assistant", "model": s.Result.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": Object{"input_tokens": s.Result.Input, "output_tokens": 0}}})
	}
	base := Object{"id": s.Result.ID, "object": "response", "status": "in_progress", "model": s.Result.Model, "output": []any{}}
	if e := s.emit("response.created", Object{"response": base}); e != nil {
		return e
	}
	return s.emit("response.in_progress", Object{"response": base})
}
func (s *Stream) index(key, kind string) int {
	if i, ok := s.indices[key]; ok {
		return i
	}
	i := len(s.parts)
	s.indices[key] = i
	s.parts = append(s.parts, Part{Kind: kind})
	return i
}
func (s *Stream) open(i int) error {
	if s.opened[i] {
		return nil
	}
	p := s.parts[i]
	if p.Kind == "call" && (p.ID == "" || p.Name == "") {
		return nil
	}
	s.opened[i] = true
	if e := s.start(); e != nil {
		return e
	}
	if s.Target == domain.Messages {
		block := Object{"type": "text", "text": ""}
		if p.Kind == "reasoning" {
			block["type"] = "text"
		} else if p.Kind == "call" {
			t := toolFor(s.Ctx, p.Name)
			block = Object{"type": "tool_use", "id": p.ID, "name": t.Name, "input": Object{}}
		}
		return s.emit("content_block_start", Object{"type": "content_block_start", "index": i, "content_block": block})
	}
	item := Object{"id": fmt.Sprintf("msg_%s_%d", s.Result.ID, i), "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
	if p.Kind == "call" {
		item = responseCall(p, s.Ctx)
		item["status"] = "in_progress"
		if item["type"] == "custom_tool_call" {
			item["input"] = ""
		} else {
			item["arguments"] = ""
		}
	} else if p.Kind == "reasoning" {
		item = Object{"id": fmt.Sprintf("rs_%s_%d", s.Result.ID, i), "type": "reasoning", "summary": []any{}}
	}
	if e := s.emit("response.output_item.added", Object{"output_index": i, "item": item}); e != nil {
		return e
	}
	if p.Kind == "text" {
		return s.emit("response.content_part.added", Object{"output_index": i, "item_id": item["id"], "content_index": 0, "part": Object{"type": "output_text", "text": "", "annotations": []any{}}})
	}
	if p.Kind == "reasoning" {
		return s.emit("response.reasoning_summary_part.added", Object{"output_index": i, "item_id": item["id"], "summary_index": 0, "part": Object{"type": "summary_text", "text": ""}})
	}
	return nil
}
func (s *Stream) text(kind, key, delta string) error {
	if delta == "" {
		return nil
	}
	if kind == "reasoning" && s.Target == domain.Messages {
		return nil
	}
	i := s.index(key, kind)
	s.parts[i].Text += delta
	if e := s.open(i); e != nil {
		return e
	}
	if s.Target == domain.Messages {
		return s.emit("content_block_delta", Object{"type": "content_block_delta", "index": i, "delta": Object{"type": "text_delta", "text": delta}})
	}
	event := "response.output_text.delta"
	itemID := fmt.Sprintf("msg_%s_%d", s.Result.ID, i)
	o := Object{"output_index": i, "item_id": itemID, "content_index": 0, "delta": delta}
	if kind == "reasoning" {
		event = "response.reasoning_summary_text.delta"
		o["item_id"] = fmt.Sprintf("rs_%s_%d", s.Result.ID, i)
		o["summary_index"] = 0
	}
	return s.emit(event, o)
}
func (s *Stream) tool(key, id, name, args string) error {
	i := s.index(key, "call")
	p := &s.parts[i]
	if id != "" {
		p.ID = id
	}
	if name != "" {
		p.Name = name
	}
	old := p.Args
	p.Args += args
	wasOpen := s.opened[i]
	if e := s.open(i); e != nil {
		return e
	}
	if !s.opened[i] {
		return nil
	}
	delta := args
	if !wasOpen {
		delta = old + args
	}
	if delta == "" {
		return nil
	}
	if s.Target == domain.Messages {
		return s.emit("content_block_delta", Object{"type": "content_block_delta", "index": i, "delta": Object{"type": "input_json_delta", "partial_json": delta}})
	}
	if toolFor(s.Ctx, p.Name).Kind == "custom" {
		return nil
	}
	return s.emit("response.function_call_arguments.delta", Object{"output_index": i, "item_id": "fc_" + p.ID, "delta": delta})
}
func (s *Stream) usage(u Object, p domain.Protocol) {
	if u == nil || len(u) == 0 {
		return
	}
	s.Result.UsageKnown = true
	if p == domain.Chat {
		s.Result.Input = Num(u["prompt_tokens"])
		s.Result.Output = Num(u["completion_tokens"])
	} else {
		if n, ok := u["input_tokens"]; ok {
			s.Result.Input = Num(n)
		}
		if n, ok := u["output_tokens"]; ok {
			s.Result.Output = Num(n)
		}
	}
}

func (s *Stream) finish() error {
	if s.finished {
		return nil
	}
	s.finished = true
	for i, p := range s.parts {
		if p.Kind == "call" {
			if p.ID == "" || p.Name == "" {
				return errors.New("upstream tool call has no identity")
			}
			var value any
			if e := json.Unmarshal([]byte(p.Args), &value); e != nil {
				return fmt.Errorf("upstream tool call has invalid JSON arguments")
			}
			if toolFor(s.Ctx, p.Name).Kind == "custom" {
				if _, ok := Obj(value)["input"].(string); !ok {
					return errors.New("custom tool input must be a string")
				}
			}
		}
		if e := s.open(i); e != nil {
			return e
		}
	}
	if e := s.start(); e != nil {
		return e
	}
	s.Result.Parts = s.parts
	if s.Target == domain.Messages {
		for i := range s.parts {
			if e := s.emit("content_block_stop", Object{"type": "content_block_stop", "index": i}); e != nil {
				return e
			}
		}
		rendered := RenderResult(domain.Messages, s.Result, s.Ctx)
		if e := s.emit("message_delta", Object{"type": "message_delta", "delta": Object{"stop_reason": rendered["stop_reason"], "stop_sequence": nil}, "usage": Object{"input_tokens": s.Result.Input, "output_tokens": s.Result.Output}}); e != nil {
			return e
		}
		return s.emit("message_stop", Object{"type": "message_stop"})
	}
	result := RenderResult(domain.Responses, s.Result, s.Ctx)
	output := Arr(result["output"])
	for i, p := range s.parts {
		item := Obj(output[i])
		switch p.Kind {
		case "text":
			part := Obj(Arr(item["content"])[0])
			if e := s.emit("response.output_text.done", Object{"output_index": i, "item_id": item["id"], "content_index": 0, "text": p.Text}); e != nil {
				return e
			}
			if e := s.emit("response.content_part.done", Object{"output_index": i, "item_id": item["id"], "content_index": 0, "part": part}); e != nil {
				return e
			}
		case "reasoning":
			if e := s.emit("response.reasoning_summary_text.done", Object{"output_index": i, "item_id": item["id"], "summary_index": 0, "text": p.Text}); e != nil {
				return e
			}
			if e := s.emit("response.reasoning_summary_part.done", Object{"output_index": i, "item_id": item["id"], "summary_index": 0, "part": Object{"type": "summary_text", "text": p.Text}}); e != nil {
				return e
			}
		case "call":
			if item["type"] == "custom_tool_call" {
				if e := s.emit("response.custom_tool_call_input.delta", Object{"output_index": i, "item_id": item["id"], "delta": item["input"]}); e != nil {
					return e
				}
				if e := s.emit("response.custom_tool_call_input.done", Object{"output_index": i, "item_id": item["id"], "input": item["input"]}); e != nil {
					return e
				}
			} else {
				if e := s.emit("response.function_call_arguments.done", Object{"output_index": i, "item_id": item["id"], "arguments": p.Args, "name": item["name"]}); e != nil {
					return e
				}
			}
		}
		if e := s.emit("response.output_item.done", Object{"output_index": i, "item": item}); e != nil {
			return e
		}
	}
	event := "response.completed"
	if Str(result["status"]) == "incomplete" {
		event = "response.incomplete"
	}
	return s.emit(event, Object{"response": result})
}

func (s *Stream) Fail(message string) error {
	if s.Target == domain.Messages {
		return s.emit("error", Object{"type": "error", "error": Object{"type": "api_error", "message": message}})
	}
	return s.emit("response.failed", Object{"response": Object{"id": s.Result.ID, "object": "response", "status": "failed", "error": Object{"code": "upstream_stream_error", "message": message}, "output": []any{}}})
}

func (s *Stream) Run(source domain.Protocol, r io.Reader) error {
	toolBlocks := map[int]string{}
	done := false
	err := ReadSSE(r, func(event string, data []byte) (bool, error) {
		if string(data) == "[DONE]" {
			if source != domain.Chat {
				return false, nil
			}
			done = true
			return true, s.finish()
		}
		var b Object
		if e := json.Unmarshal(data, &b); e != nil {
			return false, fmt.Errorf("invalid upstream SSE JSON")
		}
		if b["error"] != nil || event == "error" || Str(b["type"]) == "error" {
			return false, errors.New("upstream stream error")
		}
		switch source {
		case domain.Chat:
			s.usage(Obj(b["usage"]), source)
			for _, v := range Arr(b["choices"]) {
				c := Obj(v)
				if Num(c["index"]) != 0 {
					continue
				}
				d := Obj(c["delta"])
				if e := s.text("reasoning", "reasoning", Str(d["reasoning_content"])); e != nil {
					return false, e
				}
				if e := s.text("text", "text", Str(d["content"])); e != nil {
					return false, e
				}
				for _, tv := range Arr(d["tool_calls"]) {
					t := Obj(tv)
					f := Obj(t["function"])
					key := fmt.Sprintf("tool_%d", Num(t["index"]))
					if e := s.tool(key, Str(t["id"]), Str(f["name"]), Str(f["arguments"])); e != nil {
						return false, e
					}
				}
				if stop := Str(c["finish_reason"]); stop != "" {
					s.Result.Stop = stop
				}
			}
		case domain.Messages:
			kind := Str(b["type"])
			idx := int(Num(b["index"]))
			key := fmt.Sprintf("block_%d", idx)
			switch kind {
			case "message_start":
				s.usage(Obj(Obj(b["message"])["usage"]), source)
			case "content_block_start":
				block := Obj(b["content_block"])
				toolBlocks[idx] = Str(block["type"])
				switch toolBlocks[idx] {
				case "tool_use":
					args := ""
					if len(Obj(block["input"])) > 0 {
						args = JSON(block["input"])
					}
					if e := s.tool(key, Str(block["id"]), Str(block["name"]), args); e != nil {
						return false, e
					}
				case "text":
					if e := s.text("text", key, Str(block["text"])); e != nil {
						return false, e
					}
				case "thinking":
					if e := s.text("reasoning", key, Str(block["thinking"])); e != nil {
						return false, e
					}
				}
			case "content_block_delta":
				d := Obj(b["delta"])
				switch Str(d["type"]) {
				case "text_delta":
					return false, s.text("text", key, Str(d["text"]))
				case "thinking_delta":
					return false, s.text("reasoning", key, Str(d["thinking"]))
				case "input_json_delta":
					return false, s.tool(key, "", "", Str(d["partial_json"]))
				}
			case "content_block_stop":
				if toolBlocks[idx] == "tool_use" {
					if i, ok := s.indices[key]; ok && s.parts[i].Args == "" {
						s.parts[i].Args = "{}"
					}
				}
			case "message_delta":
				s.usage(Obj(b["usage"]), source)
				if stop := Str(Obj(b["delta"])["stop_reason"]); stop != "" {
					s.Result.Stop = stop
				}
			case "message_stop":
				done = true
				return true, s.finish()
			}
		case domain.Responses:
			kind := Str(b["type"])
			if kind == "" {
				kind = event
			}
			key := fmt.Sprintf("item_%d", Num(b["output_index"]))
			switch kind {
			case "response.output_item.added":
				item := Obj(b["item"])
				if Str(item["type"]) == "function_call" {
					return false, s.tool(key, callID(item), Str(item["name"]), Str(item["arguments"]))
				}
			case "response.output_text.delta":
				return false, s.text("text", key, Str(b["delta"]))
			case "response.reasoning_summary_text.delta":
				return false, s.text("reasoning", key, Str(b["delta"]))
			case "response.function_call_arguments.delta":
				return false, s.tool(key, "", "", Str(b["delta"]))
			case "response.output_item.done":
				item := Obj(b["item"])
				if Str(item["type"]) == "function_call" {
					if i, ok := s.indices[key]; ok {
						if s.parts[i].Args == "" {
							return false, s.tool(key, callID(item), Str(item["name"]), Str(item["arguments"]))
						}
					}
				} else if Str(item["type"]) != "message" && Str(item["type"]) != "reasoning" {
					return false, fmt.Errorf("unsupported upstream output item %q", Str(item["type"]))
				}
			case "response.completed", "response.incomplete":
				response := Obj(b["response"])
				s.usage(Obj(response["usage"]), source)
				if kind == "response.incomplete" {
					s.Result.Stop = "length"
				}
				done = true
				return true, s.finish()
			case "response.failed":
				return false, errors.New("upstream response failed")
			}
		}
		return false, nil
	})
	if err == nil && !done {
		return io.ErrUnexpectedEOF
	}
	return err
}
