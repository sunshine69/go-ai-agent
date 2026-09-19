package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEncodeBodyBackendToolChoice asserts that encodeBody emits the bare-string
// tool_choice that local llama.cpp / ollama servers require, and the OpenAI
// object form for generic backends. This is the core regression test for the
// "Wrong type supplied for parameter 'tool_choice' ... is object" bug.
func TestEncodeBodyBackendToolChoice(t *testing.T) {
	// Build a request that carries an "auto" tool choice.
	makeReq := func() CompletionRequest {
		return CompletionRequest{
			Model: "some-llama",
			Messages: []ChatMessage{
				{Role: "user", Content: "hi"},
			},
			ToolChoice: ToolChoiceAuto(),
		}
	}

	// For llama.cpp the emitted body must carry "tool_choice":"auto" (a bare
	// string), NOT the object form {type:object}.
	body, err := encodeBody("llama_cpp", makeReq())
	if err != nil {
		t.Fatalf("encodeBody(llama_cpp): %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if string(raw["tool_choice"]) != `"auto"` {
		t.Fatalf("llama_cpp tool_choice = %s; want %q", raw["tool_choice"], `"auto"`)
	}
	// Sanity: the object form must be absent.
	if strings.Contains(string(body), `"type":"function"`) {
		t.Fatalf("llama_cpp body unexpectedly contains an object tool_choice")
	}

	// For ollama a pinned tool must come out as "function:<name>".
	req := makeReq()
	req.ToolChoice = ToolChoicePinned("add_tool")
	body, err = encodeBody("ollama", req)
	if err != nil {
		t.Fatalf("encodeBody(ollama): %v", err)
	}
	var obj2 map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj2); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if string(obj2["tool_choice"]) != `"function:add_tool"` {
		t.Fatalf("ollama tool_choice = %s; want %q", obj2["tool_choice"], `"function:add_tool"`)
	}

	// For a generic OpenAI backend the object form must be preserved.
	body, err = encodeBody("", makeReq())
	if err != nil {
		t.Fatalf("encodeBody(generic): %v", err)
	}
	var obj3 map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj3); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	var tc ToolChoice
	if err := json.Unmarshal(obj3["tool_choice"], &tc); err != nil {
		t.Fatalf("tool_choice is not an object: %v (raw=%s)", err, obj3["tool_choice"])
	}
	if tc.Type != "function" || tc.Function.Name != "auto" {
		t.Fatalf("generic tool_choice = %+v; want {function auto}", tc)
	}
}

// TestDetectBackend verifies backend sniffing from model name and base URL.
func TestDetectBackend(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"model llama", Config{Model: "llama3.1-8b"}, "llama_cpp"},
		{"model ollama prefix", Config{Model: "ollama/mistral"}, "ollama"},
		{"url llama", Config{BaseURL: "http://llama.local:8080"}, "llama_cpp"},
		{"url ollama", Config{BaseURL: "http://ollama.local:11434"}, "ollama"},
		{"explicit wins over model", Config{Model: "llama3.1", Backend: "ollama"}, "ollama"},
		{"generic", Config{Model: "gpt-4o"}, ""},
		{"explicit generic", Config{Backend: "openai"}, "openai"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectBackend(c.cfg); got != c.want {
				t.Fatalf("detectBackend(%+v) = %q; want %q", c.cfg, got, c.want)
			}
		})
	}
}
