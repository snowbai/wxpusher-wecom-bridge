package browser

import (
	"context"
	"net"
	"net/netip"
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
	if err := validateFetchURLWithResolver(context.Background(), "https://example.com/a", staticResolver("93.184.216.34")); err != nil {
		t.Fatalf("validateFetchURLWithResolver() error = %v, want nil", err)
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
			if err := validateFetchURLWithResolver(context.Background(), input, staticResolver("93.184.216.34")); err == nil {
				t.Fatalf("validateFetchURLWithResolver(%q) error = nil, want error", input)
			}
		})
	}
}

func TestValidateFetchURLWithResolverAcceptsPublicHostname(t *testing.T) {
	if err := validateFetchURLWithResolver(context.Background(), "https://public.test/a", staticResolver("93.184.216.34")); err != nil {
		t.Fatalf("validateFetchURLWithResolver() error = %v, want nil", err)
	}
}

func TestValidateFetchURLWithResolverRejectsInternalHostname(t *testing.T) {
	if err := validateFetchURLWithResolver(context.Background(), "https://internal.test/a", staticResolver("10.0.0.5")); err == nil {
		t.Fatal("validateFetchURLWithResolver() error = nil, want error")
	}
}

func TestValidateFetchURLRejectsUnsafeIPv6Literals(t *testing.T) {
	tests := []string{
		"http://[::1]/x",
		"http://[fe80::1]/x",
		"http://[fd00::1]/x",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if err := validateFetchURLWithResolver(context.Background(), input, staticResolver("93.184.216.34")); err == nil {
				t.Fatalf("validateFetchURLWithResolver(%q) error = nil, want error", input)
			}
		})
	}
}

func TestValidateFetchURLWithResolverRejectsUnsafeIPv6Hostnames(t *testing.T) {
	tests := []string{
		"::1",
		"fe80::1",
		"fd00::1",
		"2001:2::1",
		"2001:20::1",
		"64:ff9b:1::1",
	}

	for _, resolved := range tests {
		t.Run(resolved, func(t *testing.T) {
			if err := validateFetchURLWithResolver(context.Background(), "https://ipv6.test/a", staticResolver(resolved)); err == nil {
				t.Fatalf("validateFetchURLWithResolver() error = nil for resolved %q, want error", resolved)
			}
		})
	}
}

func TestValidateResolvedAddrRejectsSpecialUseRanges(t *testing.T) {
	tests := []string{
		"100.64.0.1",
		"192.0.0.1",
		"192.0.2.1",
		"198.51.100.1",
		"203.0.113.1",
		"198.18.0.1",
		"0.1.2.3",
		"240.0.0.1",
		"2001:db8::1",
		"100::1",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			addr := netip.MustParseAddr(input)
			if err := validateResolvedAddr(addr); err == nil {
				t.Fatalf("validateResolvedAddr(%s) error = nil, want error", input)
			}
		})
	}
}

func TestValidateResolvedAddrAcceptsPublicAddress(t *testing.T) {
	if err := validateResolvedAddr(netip.MustParseAddr("93.184.216.34")); err != nil {
		t.Fatalf("validateResolvedAddr() error = %v, want nil", err)
	}
}

func TestIsFetchURLAllowedWithResolver(t *testing.T) {
	if !isFetchURLAllowed(context.Background(), "https://public.test/a", staticResolver("93.184.216.34")) {
		t.Fatal("isFetchURLAllowed() = false, want true")
	}
	if isFetchURLAllowed(context.Background(), "https://internal.test/a", staticResolver("10.0.0.5")) {
		t.Fatal("isFetchURLAllowed() = true, want false")
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

func staticResolver(addrs ...string) func(context.Context, string) ([]net.IP, error) {
	return func(context.Context, string) ([]net.IP, error) {
		ips := make([]net.IP, 0, len(addrs))
		for _, addr := range addrs {
			ips = append(ips, net.ParseIP(addr))
		}
		return ips, nil
	}
}
