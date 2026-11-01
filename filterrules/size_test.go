package filterrules

import (
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	valid := map[string]int64{
		"none": 0, "1k": 1024, "1K": 1024, "10M": 10 << 20, "10m": 10 << 20, "2G": 2 << 30, "4k": 4096,
	}
	for text, want := range valid {
		if got, err := ParseSize(text); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v, want %d", text, got, err, want)
		}
	}
	for _, text := range []string{"", "10", "10MB", "10 M", "1.5G", "0k", "-1k", "+1k", "k", "10T", "None", "99999999999999999999g"} {
		if got, err := ParseSize(text); err == nil {
			t.Errorf("ParseSize(%q) = %d, want an error", text, got)
		}
	}
}

func TestRulesMaxFileSize(t *testing.T) {
	r := newFileRules(t, "version 1\nmax-file-size 4k\nuse default\n")
	if text, bytes := r.MaxFileSize(); text != "4k" || bytes != 4096 {
		t.Errorf("MaxFileSize() = %q, %d", text, bytes)
	}
	if r.TooLarge(4096) || !r.TooLarge(4097) {
		t.Error("exactly 4k is kept, one byte more is not")
	}
	if !strings.Contains(r.Dump(), "max-file-size 4k\n") {
		t.Errorf("Dump():\n%s", r.Dump())
	}

	none := newFileRules(t, "version 1\nmax-file-size none\nuse default\n")
	if text, bytes := none.MaxFileSize(); text != "none" || bytes != 0 || none.TooLarge(1<<40) {
		t.Errorf("none: %q, %d", text, bytes)
	}
	absent := newFileRules(t, "version 1\nuse default\n")
	if text, bytes := absent.MaxFileSize(); text != "" || bytes != 0 || absent.TooLarge(1<<40) {
		t.Errorf("absent: %q, %d", text, bytes)
	}
}
