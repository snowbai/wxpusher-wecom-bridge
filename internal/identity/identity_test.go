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
