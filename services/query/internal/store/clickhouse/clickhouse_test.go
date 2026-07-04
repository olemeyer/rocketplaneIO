package clickhouse

import (
	"reflect"
	"testing"
	"time"
)

func mustTime() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func TestDecodeJSONEachRow(t *testing.T) {
	data := []byte("{\"name\":\"a\",\"n\":1}\n\n{\"name\":\"b\",\"n\":2}\n")
	var out []struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	if err := decodeJSONEachRow(data, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].Name != "a" || out[1].N != 2 {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestDecodeJSONEachRowEmpty(t *testing.T) {
	var out []struct{ Name string }
	if err := decodeJSONEachRow([]byte("\n \n"), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want empty, got %d", len(out))
	}
}

func TestCursorRoundtrip(t *testing.T) {
	for _, n := range []int{0, 1, 50, 12345} {
		if got := decodeCursor(encodeCursor(n)); got != n {
			t.Errorf("cursor %d -> %q -> %d", n, encodeCursor(n), got)
		}
	}
	if decodeCursor("") != 0 || decodeCursor("garbage") != 0 || decodeCursor("o1x") != 0 {
		t.Error("invalid cursors must decode to 0")
	}
}

func TestRound1(t *testing.T) {
	cases := map[float64]float64{1.24: 1.2, 1.25: 1.3, 842.37: 842.4}
	for in, want := range cases {
		if got := round1(in); got != want {
			t.Errorf("round1(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestChTimeFormat(t *testing.T) {
	// Sicherstellen, dass das ClickHouse-DateTime64(9)-Format stabil bleibt.
	got := chTime(mustTime())
	want := "2023-11-14 22:13:20.000000000"
	if got != want {
		t.Errorf("chTime = %q, want %q", got, want)
	}
	if reflect.TypeOf(got).Kind() != reflect.String {
		t.Error("chTime must return string")
	}
}
