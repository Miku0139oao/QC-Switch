package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"

	"qc-switch/internal/app"
	"qc-switch/internal/config"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("data-dir", app.DefaultDirectory(), "portable data directory")
	flag.Parse()
	release, e := instanceLock(*dir)
	if e != nil {
		return e
	}
	defer release()
	service, e := app.New(*dir)
	if e != nil {
		return e
	}
	defer service.Close()
	var mu sync.Mutex
	encoder := json.NewEncoder(os.Stdout)
	send := func(v any) { mu.Lock(); defer mu.Unlock(); _ = encoder.Encode(v) }
	service.Notify = func() { send(map[string]any{"jsonrpc": "2.0", "method": "state.changed", "params": map[string]any{}}) }
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 8192), 16<<20)
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if e := json.Unmarshal(scanner.Bytes(), &req); e != nil {
			send(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "Invalid JSON"}})
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32600, "message": "Invalid request"}})
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		result, e := service.Handle(context.Background(), req.Method, req.Params)
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if e != nil {
			failure := map[string]any{"code": -32000, "message": e.Error()}
			var conflicts *config.ConflictError
			if errors.As(e, &conflicts) {
				failure["code"] = -32009
				failure["data"] = conflicts
			}
			response["error"] = failure
		} else {
			response["result"] = result
		}
		send(response)
		if req.Method == "shutdown" && e == nil {
			return nil
		}
	}
	return scanner.Err()
}
