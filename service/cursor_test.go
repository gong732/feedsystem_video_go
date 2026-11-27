package service

import "testing"

func TestFormatParseCursorRoundTrip(t *testing.T) {
	ts, id := int64(1700000000000), uint(42)
	s := formatCursor(ts, id)
	gotTs, gotID := parseCursor(s)
	if gotTs != ts || gotID != id {
		t.Fatalf("round trip = (%d,%d), want (%d,%d)", gotTs, gotID, ts, id)
	}
}

func TestParseCursorInvalid(t *testing.T) {
	bad := []string{"", "abc", "123:", ":42", "abc:42", "0:42", "123:0", "123:abc"}
	for _, s := range bad {
		ts, id := parseCursor(s)
		if ts != 0 || id != 0 {
			t.Fatalf("parseCursor(%q) = (%d,%d), want (0,0)", s, ts, id)
		}
	}
}
