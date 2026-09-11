package ragindex

import (
	"strconv"
	"strings"
	"testing"
)

// patternText builds a string where every byte carries a distinct digit (0-9),
// e.g. "01234567890123456789". This lets a test inspect a chunk and recover the
// exact byte offsets it came from by reading the digits back.
func patternText(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(strconv.Itoa(i % 10))
	}
	return b.String()
}

// TestChunkTextNoOverlap verifies the plain fixed-size split behavior.
func TestChunkTextNoOverlap(t *testing.T) {
	// size 5, overlap 0 => non-overlapping chunks of 5.
	got := chunkText(patternText(13), 5, 0)
	want := []string{"01234", "56789", "012"}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestChunkTextAppliesOverlap is the regression test for the overlap bug. It
// proves overlap is actually applied by using a patterned string and asserting
// that the last `overlap` bytes of each chunk equal the first `overlap` bytes of
// the next chunk.
func TestChunkTextAppliesOverlap(t *testing.T) {
	got := chunkText(patternText(13), 5, 2)
	// step = size - overlap = 3
	// start0: [0,5)="01234"  start1: [3,8)="34567"  start2: [6,11)="67890"  start3: [9,13)="9012"
	// step = 3 => starts at 0,3,6,9,12. The last start (12) yields a 1-byte
	// chunk "2" (the 13th character).
	want := []string{"01234", "34567", "67890", "9012", "2"}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Independent overlap check: last `overlap` bytes of chunk i == first
	// `overlap` bytes of chunk i+1. Guard slicing so short trailing chunks
	// (length < overlap) don't panic.
	overlap := 2
	for i := 0; i+1 < len(got); i++ {
		if len(got[i]) < overlap || len(got[i+1]) < overlap {
			continue
		}
		tail := got[i][len(got[i])-overlap:]
		head := got[i+1][:overlap]
		if tail != head {
			t.Errorf("no overlap between chunk %d and %d: tail=%q head=%q", i, i+1, tail, head)
		}
	}
}

func TestChunkTextSmallerThanSize(t *testing.T) {
	got := chunkText("hi", 5, 2)
	if len(got) != 1 || got[0] != "hi" {
		t.Fatalf("single small input = %v, want [\"hi\"]", got)
	}
}

func TestChunkTextEmpty(t *testing.T) {
	if got := chunkText("", 5, 2); got != nil {
		t.Fatalf("empty input = %v, want nil", got)
	}
	if got := chunkText("   ", 5, 2); got != nil {
		t.Fatalf("whitespace input = %v, want nil", got)
	}
}

func TestChunkTextInvalidSize(t *testing.T) {
	if got := chunkText(patternText(20), 0, 5); got != nil {
		t.Fatalf("size 0 = %v, want nil", got)
	}
	if got := chunkText(patternText(20), -5, 5); got != nil {
		t.Fatalf("negative size = %v, want nil", got)
	}
}

func TestChunkTextOverlapBiggerThanSize(t *testing.T) {
	// overlap >= size must not infinite-loop or produce empty chunks; the step
	// clamps to `size` (non-overlapping).
	got := chunkText(patternText(12), 5, 100)
	if len(got) == 0 {
		t.Fatal("got zero chunks")
	}
	for _, c := range got {
		if c == "" {
			t.Error("produced an empty chunk")
		}
		if len(c) > 5 {
			t.Errorf("chunk %q exceeds size 5", c)
		}
	}
	// With step clamped to 5, expect exactly ceil(12/5) = 3 chunks.
	if len(got) != 3 {
		t.Errorf("got %d chunks %v, want 3", len(got), got)
	}
}

// TestChunkTextRealDefaults guards the real-world defaults (512 / 50): overlap
// must be applied and no chunk may exceed the configured size.
func TestChunkTextRealDefaults(t *testing.T) {
	got := chunkText(patternText(512*3), 512, 50)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(got))
	}
	// Step should be 512-50 = 462, so chunks overlap.
	if got[1][:50] != got[0][len(got[0])-50:] {
		t.Error("no 50-byte overlap between chunk 0 and 1")
	}
	for _, c := range got {
		if len(c) > 512 {
			t.Fatalf("chunk %q has length %d > 512", c, len(c))
		}
	}
}

// TestNewPreservesConfiguredChunkSize verifies the indexer uses the configured
// chunk size / overlap (previously handleFile ignored config and used hardcoded
// DefaultChunkSize / DefaultOverlap).
func TestNewPreservesConfiguredChunkSize(t *testing.T) {
	ix, err := New(nil, Options{DocsDir: "resources/rag_documents", ChunkSize: 400, ChunkOverlap: 30})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ix.opts.ChunkSize != 400 {
		t.Errorf("ChunkSize not preserved: got %d, want 400", ix.opts.ChunkSize)
	}
	if ix.opts.ChunkOverlap != 30 {
		t.Errorf("ChunkOverlap not preserved: got %d, want 30", ix.opts.ChunkOverlap)
	}
}
