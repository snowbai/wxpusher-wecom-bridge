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

func TestExtractURLsTrimsMarkdownDelimiters(t *testing.T) {
	got := ExtractURLs("see (https://example.com/a). and [https://example.com/b]")
	want := []string{"https://example.com/a", "https://example.com/b"}

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

func TestValidateFetchURLAcceptsPublicHTTPS(t *testing.T) {
	if err := validateFetchURL("https://example.com/a"); err != nil {
		t.Fatalf("validateFetchURL() error = %v, want nil", err)
	}
}

func TestValidateFetchURLRejectsUnsafeURLs(t *testing.T) {
	tests := []string{
		"file:///etc/passwd",
		"http://localhost/x",
		"http://127.0.0.1/x",
		"http://10.0.0.1/x",
		"://bad",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if err := validateFetchURL(input); err == nil {
				t.Fatalf("validateFetchURL(%q) error = nil, want error", input)
			}
		})
	}
}

func TestEffectiveSummaryLimit(t *testing.T) {
	if got := effectiveSummaryLimit(42); got != 42 {
		t.Fatalf("effectiveSummaryLimit(42) = %d, want 42", got)
	}
	if got := effectiveSummaryLimit(0); got != defaultSummaryMaxChars {
		t.Fatalf("effectiveSummaryLimit(0) = %d, want %d", got, defaultSummaryMaxChars)
	}
	if got := effectiveSummaryLimit(-1); got != defaultSummaryMaxChars {
		t.Fatalf("effectiveSummaryLimit(-1) = %d, want %d", got, defaultSummaryMaxChars)
	}
}
