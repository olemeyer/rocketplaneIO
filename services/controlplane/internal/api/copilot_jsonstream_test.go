package api

import (
	"math/rand"
	"strings"
	"testing"
)

// feedChunks füttert s in zufälligen Chunk-Größen (deterministischer Seed je Fall).
func feedChunks(t *testing.T, j *jsonFieldStreamer, s string, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	for len(s) > 0 {
		n := 1 + rng.Intn(7)
		if n > len(s) {
			n = len(s)
		}
		j.Feed(s[:n])
		s = s[n:]
	}
}

func TestJSONFieldStreamerBasic(t *testing.T) {
	var out strings.Builder
	j := newJSONFieldStreamer("message", func(s string) { out.WriteString(s) })
	j.Feed(`{"message":"Hallo Welt","status":"done"}`)
	if out.String() != "Hallo Welt" {
		t.Fatalf("got %q", out.String())
	}
	if j.Emitted() == 0 {
		t.Fatal("Emitted should be > 0")
	}
}

func TestJSONFieldStreamerChunked(t *testing.T) {
	full := `{"reasoning":"check pods first","message":"ClickHouse ist degradiert:\n2/3 Pods crashen — \"cluster.xml\" prüfen. Pfad: C:\\logs","status":"working"}`
	want := "ClickHouse ist degradiert:\n2/3 Pods crashen — \"cluster.xml\" prüfen. Pfad: C:\\logs"
	for seed := int64(0); seed < 25; seed++ {
		var out strings.Builder
		j := newJSONFieldStreamer("message", func(s string) { out.WriteString(s) })
		feedChunks(t, j, full, seed)
		if out.String() != want {
			t.Fatalf("seed %d: got %q want %q", seed, out.String(), want)
		}
	}
}

func TestJSONFieldStreamerUnicodeEscapes(t *testing.T) {
	full := `{"message":"café → ok"}`
	want := "café → ok"
	for seed := int64(0); seed < 25; seed++ {
		var out strings.Builder
		j := newJSONFieldStreamer("message", func(s string) { out.WriteString(s) })
		feedChunks(t, j, full, seed)
		if out.String() != want {
			t.Fatalf("seed %d: got %q want %q", seed, out.String(), want)
		}
	}
}

func TestJSONFieldStreamerDecoyInOtherValue(t *testing.T) {
	// Ein escaped "message"-Vorkommen in einem ANDEREN Feldwert darf nicht triggern.
	full := `{"reasoning":"the \"message\" here is a decoy","status":"working","message":"real"}`
	for seed := int64(0); seed < 25; seed++ {
		var out strings.Builder
		j := newJSONFieldStreamer("message", func(s string) { out.WriteString(s) })
		feedChunks(t, j, full, seed)
		if out.String() != "real" {
			t.Fatalf("seed %d: got %q want %q", seed, out.String(), "real")
		}
	}
}

func TestJSONFieldStreamerNestedObjectValue(t *testing.T) {
	// message-Key in einem VERSCHACHTELTEN Objekt (depth 2) darf nicht triggern.
	full := `{"meta":{"message":"nested decoy"},"message":"top"}`
	var out strings.Builder
	j := newJSONFieldStreamer("message", func(s string) { out.WriteString(s) })
	j.Feed(full)
	if out.String() != "top" {
		t.Fatalf("got %q want %q", out.String(), "top")
	}
}

func TestJSONFieldStreamerNoField(t *testing.T) {
	var out strings.Builder
	j := newJSONFieldStreamer("message", func(s string) { out.WriteString(s) })
	j.Feed(`{"status":"working"}`)
	if out.String() != "" || j.Emitted() != 0 {
		t.Fatalf("nothing should be emitted, got %q", out.String())
	}
}
