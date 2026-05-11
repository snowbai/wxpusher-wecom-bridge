package importer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportJSONReadsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	data := []byte(`{
		"deviceUuid": "du",
		"deviceToken": "dt",
		"pushToken": "pt",
		"platform": "Chrome-Linux",
		"version": "1.1.0"
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write identity json: %v", err)
	}

	id, err := ImportJSON(path, "Chrome-Linux", "1.1.0")
	if err != nil {
		t.Fatalf("ImportJSON returned error: %v", err)
	}

	if id.DeviceUUID != "du" {
		t.Fatalf("DeviceUUID = %q", id.DeviceUUID)
	}
	if id.DeviceToken != "dt" {
		t.Fatalf("DeviceToken = %q", id.DeviceToken)
	}
	if id.PushToken != "pt" {
		t.Fatalf("PushToken = %q", id.PushToken)
	}
	if id.Platform != "Chrome-Linux" {
		t.Fatalf("Platform = %q", id.Platform)
	}
	if id.Version != "1.1.0" {
		t.Fatalf("Version = %q", id.Version)
	}
	if id.Source != "json" {
		t.Fatalf("Source = %q", id.Source)
	}
}
