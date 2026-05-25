package tools

import (
	"context"
	"encoding/json"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	defaultDays  = 7
	maxDays      = 90
)

func okJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func errJSON(msg string) string {
	b, _ := json.Marshal(map[string]any{"status": "error", "error": msg})
	return string(b)
}

func wrap(name string, fn func(mcp.CallToolRequest) (string, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := fn(req)
		if err != nil {
			log.Printf("%s error: %v", name, err)
			return mcp.NewToolResultText(errJSON(err.Error())), nil
		}
		return mcp.NewToolResultText(result), nil
	}
}

func args(req mcp.CallToolRequest) map[string]any {
	a := req.GetArguments()
	if a == nil {
		return map[string]any{}
	}
	return a
}

func strArg(req mcp.CallToolRequest, key string) string {
	v, _ := args(req)[key].(string)
	return v
}

func boolDefault(req mcp.CallToolRequest, key string, def bool) bool {
	v, ok := args(req)[key]
	if !ok || v == nil {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func intDefault(req mcp.CallToolRequest, key string, def, max int) int {
	v, ok := args(req)[key]
	if !ok || v == nil {
		return def
	}
	f, ok := v.(float64)
	if !ok {
		return def
	}
	n := int(f)
	if n <= 0 {
		return def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}
