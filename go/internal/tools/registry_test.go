package tools

import (
	"strings"
	"testing"
)

// stubTool is a minimal Tool implementation for testing.
type stubTool struct {
	def ToolDefinition
}

func (s stubTool) Definition() ToolDefinition { return s.def }
func (s stubTool) Execute(_ map[string]any) ToolResult {
	return ToolResult{Content: "stub result"}
}

func newStub(name, description string) stubTool {
	return stubTool{def: ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{"type": "object"},
	}}
}

// ---------------------------------------------------------------------------
// Register + Get
// ---------------------------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	tool := newStub("search", "Search files")
	r.Register(tool)

	got := r.Get("search")
	if got == nil {
		t.Fatal("Get(\"search\") = nil, want registered tool")
	}
	if got.Definition().Name != "search" {
		t.Errorf("Definition().Name = %q, want %q", got.Definition().Name, "search")
	}
}

func TestRegistry_Get_Missing(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if got := r.Get("unknown"); got != nil {
		t.Errorf("Get(\"unknown\") = %v, want nil", got)
	}
}

func TestRegistry_Register_Overwrites(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(newStub("calc", "first version"))
	r.Register(newStub("calc", "second version"))

	got := r.Get("calc")
	if got == nil {
		t.Fatal("Get(\"calc\") = nil")
	}
	if got.Definition().Description != "second version" {
		t.Errorf("Description = %q, want %q", got.Definition().Description, "second version")
	}
}

// ---------------------------------------------------------------------------
// GetDefinitions
// ---------------------------------------------------------------------------

func TestRegistry_GetDefinitions_AllTools(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(newStub("tool_a", "Alpha"))
	r.Register(newStub("tool_b", "Beta"))
	r.Register(newStub("tool_c", "Gamma"))

	// nil enabled means all tools.
	defs := r.GetDefinitions(nil)
	if len(defs) != 3 {
		t.Errorf("GetDefinitions(nil) len = %d, want 3", len(defs))
	}
}

func TestRegistry_GetDefinitions_FilteredList(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(newStub("tool_a", "Alpha"))
	r.Register(newStub("tool_b", "Beta"))
	r.Register(newStub("tool_c", "Gamma"))

	defs := r.GetDefinitions([]string{"tool_a", "tool_c"})
	if len(defs) != 2 {
		t.Fatalf("GetDefinitions([a,c]) len = %d, want 2", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["tool_a"] || !names["tool_c"] {
		t.Errorf("unexpected tools in result: %v", names)
	}
	if names["tool_b"] {
		t.Error("tool_b should not be included")
	}
}

func TestRegistry_GetDefinitions_EmptySlice(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(newStub("tool_a", "Alpha"))

	// Empty (not nil) slice → also returns all tools (len(enabled)==0 branch).
	defs := r.GetDefinitions([]string{})
	if len(defs) != 1 {
		t.Errorf("GetDefinitions([]) len = %d, want 1", len(defs))
	}
}

// ---------------------------------------------------------------------------
// ListNames
// ---------------------------------------------------------------------------

func TestRegistry_ListNames(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if names := r.ListNames(); len(names) != 0 {
		t.Errorf("empty registry ListNames() = %v, want []", names)
	}

	r.Register(newStub("alpha", ""))
	r.Register(newStub("beta", ""))

	names := r.ListNames()
	if len(names) != 2 {
		t.Fatalf("ListNames() len = %d, want 2", len(names))
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Errorf("ListNames() = %v, missing expected names", names)
	}
}

// ---------------------------------------------------------------------------
// BuildToolDescriptions
// ---------------------------------------------------------------------------

func TestRegistry_BuildToolDescriptions_EmptyRegistry(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if got := r.BuildToolDescriptions(nil); got != "" {
		t.Errorf("BuildToolDescriptions on empty registry = %q, want empty", got)
	}
}

func TestRegistry_BuildToolDescriptions_WithTools(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(stubTool{def: ToolDefinition{
		Name:        "grep",
		Description: "Search text in files",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"description": "search pattern"},
				"path":    map[string]any{"description": "file path"},
			},
			"required": []any{"pattern"},
		},
	}})

	desc := r.BuildToolDescriptions(nil)

	// Must contain preamble.
	if !strings.Contains(desc, "tool_call") {
		t.Error("description should contain 'tool_call' JSON example")
	}
	// Must contain tool name and description.
	if !strings.Contains(desc, "grep") {
		t.Error("description should contain tool name 'grep'")
	}
	if !strings.Contains(desc, "Search text in files") {
		t.Error("description should contain tool description")
	}
	// Must list parameters.
	if !strings.Contains(desc, "pattern") {
		t.Error("description should mention parameter 'pattern'")
	}
	if !strings.Contains(desc, "path") {
		t.Error("description should mention parameter 'path'")
	}
	// Required param must be flagged.
	if !strings.Contains(desc, "(required)") {
		t.Error("description should mark required parameters")
	}
}

func TestRegistry_BuildToolDescriptions_Deterministic(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(newStub("z_tool", "last alphabetically"))
	r.Register(newStub("a_tool", "first alphabetically"))
	r.Register(newStub("m_tool", "middle alphabetically"))

	// Call twice — output should be identical.
	first := r.BuildToolDescriptions(nil)
	second := r.BuildToolDescriptions(nil)

	if first != second {
		t.Error("BuildToolDescriptions() is not deterministic")
	}
	// Verify alphabetical order: a_tool before m_tool before z_tool.
	posA := strings.Index(first, "a_tool")
	posM := strings.Index(first, "m_tool")
	posZ := strings.Index(first, "z_tool")
	if posA >= posM || posM >= posZ {
		t.Errorf("tools not in alphabetical order: a=%d m=%d z=%d", posA, posM, posZ)
	}
}

func TestRegistry_BuildToolDescriptions_EnabledFilter(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(newStub("allowed", "This one is enabled"))
	r.Register(newStub("blocked", "This one is disabled"))

	desc := r.BuildToolDescriptions([]string{"allowed"})

	if !strings.Contains(desc, "allowed") {
		t.Error("should contain 'allowed' tool")
	}
	if strings.Contains(desc, "blocked") {
		t.Error("should NOT contain 'blocked' tool")
	}
}

// ---------------------------------------------------------------------------
// Concurrent access (race detector)
// ---------------------------------------------------------------------------

func TestRegistryConcurrentAccess(t *testing.T) {
	// This test is intentionally NOT marked t.Parallel() at the top level so
	// that -race can be reliably exercised. Individual goroutines race internally.

	const goroutines = 20
	const iterations = 50

	r := NewRegistry()

	// Pre-seed a few tools so reads have something to find.
	for i := 0; i < 5; i++ {
		name := strings.Repeat("x", i+1) // "x", "xx", "xxx", ...
		r.Register(newStub(name, "pre-seeded"))
	}

	done := make(chan struct{})
	// Writers
	for w := 0; w < goroutines/2; w++ {
		w := w
		go func() {
			for i := 0; i < iterations; i++ {
				name := strings.Repeat("w", (w*iterations+i)%10+1)
				r.Register(newStub(name, "concurrent write"))
			}
			done <- struct{}{}
		}()
	}
	// Readers
	for rd := 0; rd < goroutines/2; rd++ {
		go func() {
			for i := 0; i < iterations; i++ {
				_ = r.Get("x")
				_ = r.ListNames()
				_ = r.GetDefinitions(nil)
				_ = r.BuildToolDescriptions(nil)
			}
			done <- struct{}{}
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
}
