package identity

import "testing"

func TestIdentityValidateRequiresDeviceFields(t *testing.T) {
	id := Identity{DeviceUUID: "du", DeviceToken: "dt", PushToken: "pt", Platform: "Chrome-Linux", Version: "1.1.0", Source: "json"}
	if err := id.Validate(); err != nil {
		t.Fatalf("expected valid identity: %v", err)
	}
	id.DeviceToken = ""
	if err := id.Validate(); err == nil {
		t.Fatal("expected missing device token error")
	}
}

func TestIdentityWithDefaultsFillsMissingFields(t *testing.T) {
	id := Identity{DeviceUUID: "du", DeviceToken: "dt"}.WithDefaults("Chrome-Linux", "1.1.0", "json")

	if id.Platform != "Chrome-Linux" {
		t.Fatalf("platform = %q", id.Platform)
	}
	if id.Version != "1.1.0" {
		t.Fatalf("version = %q", id.Version)
	}
	if id.Source != "json" {
		t.Fatalf("source = %q", id.Source)
	}
	if id.UpdatedAt.IsZero() {
		t.Fatal("expected UpdatedAt to be set")
	}
}
