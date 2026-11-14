package jsonc

import "testing"

func TestStrip_PreservesStrings(t *testing.T) {
	in := `{"a": "http://example.com//not-a-comment", "b": "/*keep*/",}`
	want := `{"a": "http://example.com//not-a-comment", "b": "/*keep*/"}`
	if got := string(Strip([]byte(in))); got != want {
		t.Fatalf("Strip mangled strings:\n got: %s\nwant: %s", got, want)
	}
}

func TestStrip_CommentsAndTrailingCommas(t *testing.T) {
	in := "{\n  // line\n  \"a\": 1, /* block */\n}"
	want := "{\n  \n  \"a\": 1 \n}"
	if got := string(Strip([]byte(in))); got != want {
		t.Fatalf("Strip:\n got: %q\nwant: %q", got, want)
	}
}
