package tools

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// ToolDefinition JSON marshaling
// ---------------------------------------------------------------------------

func TestToolDefinition_JSONMarshal(t *testing.T) {
	t.Parallel()

	def := ToolDefinition{
		Name:        "search",
		Description: "Search files for a pattern",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "search term",
				},
			},
			"required": []string{"query"},
		},
	}

	data, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var roundtrip ToolDefinition
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if roundtrip.Name != def.Name {
		t.Errorf("Name = %q, want %q", roundtrip.Name, def.Name)
	}
	if roundtrip.Description != def.Description {
		t.Errorf("Description = %q, want %q", roundtrip.Description, def.Description)
	}
	if roundtrip.InputSchema == nil {
		t.Error("InputSchema should not be nil after round-trip")
	}
}

func TestToolDefinition_JSONFieldNames(t *testing.T) {
	t.Parallel()

	def := ToolDefinition{
		Name:        "my_tool",
		Description: "does stuff",
		InputSchema: map[string]any{"type": "object"},
	}

	data, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// Verify JSON field names match the Anthropic API expectations.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	for _, key := range []string{"name", "description", "input_schema"} {
		if _, ok := m[key]; !ok {
			t.Errorf("JSON key %q is missing; got keys %v", key, jsonKeys(m))
		}
	}
}

func jsonKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ---------------------------------------------------------------------------
// ToolResult
// ---------------------------------------------------------------------------

func TestToolResult_IsError(t *testing.T) {
	t.Parallel()

	ok := ToolResult{Content: "success", IsError: false}
	if ok.IsError {
		t.Error("IsError should be false for success result")
	}
	if ok.Content != "success" {
		t.Errorf("Content = %q, want %q", ok.Content, "success")
	}

	errResult := ToolResult{Content: "something went wrong", IsError: true}
	if !errResult.IsError {
		t.Error("IsError should be true for error result")
	}
}

func TestToolResult_ZeroValue(t *testing.T) {
	t.Parallel()

	var r ToolResult
	if r.IsError {
		t.Error("zero-value ToolResult should not be an error")
	}
	if r.Content != "" {
		t.Errorf("zero-value Content = %q, want empty", r.Content)
	}
}
