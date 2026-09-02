package routers

import "testing"

func TestClientExtractedContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "null-content marker event (content=delta.role only)",
			in:   `{"choices":[{"finish_reason":null,"index":0,"delta":{"role":"assistant","content":null}}],"created":1788213881,"id":"chatcmpl-abc","model":"gemma4","system_fingerprint":"b0-unknown","object":"chat.completion.chunk"}`,
			want: "",
		},
		{
			name: "plain text token",
			in:   `Hello world`,
			want: `Hello world`,
		},
		{
			name: "content form",
			in:   `{"content":"Hello world"}`,
			want: `Hello world`,
		},
		{
			name: "delta content form",
			in:   `{"choices":[{"delta":{"content":"Hello world"}}]}`,
			want: `Hello world`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clientExtractedContent(c.in)
			if got != c.want {
				t.Errorf("clientExtractedContent(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
