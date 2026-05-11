package browser

import (
	"reflect"
	"testing"
)

func TestExtractURLs(t *testing.T) {
	got := ExtractURLs("read https://example.com/a?b=1 and http://x.test/path.")
	want := []string{"https://example.com/a?b=1", "http://x.test/path"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractURLs() = %#v, want %#v", got, want)
	}
}

func TestExtractURLsNoURLs(t *testing.T) {
	got := ExtractURLs("nothing to see here")
	if len(got) != 0 {
		t.Fatalf("ExtractURLs() = %#v, want no URLs", got)
	}
}

func TestExtractURLsRepeatedURLsPreserveOrder(t *testing.T) {
	got := ExtractURLs("first https://example.com/a then https://example.com/a")
	want := []string{"https://example.com/a", "https://example.com/a"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractURLs() = %#v, want %#v", got, want)
	}
}

func TestTrimSummaryKeepsRuneBoundary(t *testing.T) {
	got := TrimSummary("你好世界", 3)
	want := "你好世"

	if got != want {
		t.Fatalf("TrimSummary() = %q, want %q", got, want)
	}
}
