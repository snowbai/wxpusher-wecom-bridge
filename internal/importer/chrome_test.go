package importer

import (
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
)

const testExtensionID = "abcdefghijklmnopabcdefghijklmnop"

func TestImportChromeReadsLocalExtensionSettings(t *testing.T) {
	profilePath := t.TempDir()
	dbPath := filepath.Join(profilePath, "Local Extension Settings", testExtensionID)
	writeLevelDB(t, dbPath, map[string]string{
		"deviceUuid":  `"du"`,
		"deviceToken": `"dt"`,
		"pushToken":   `"pt"`,
	})

	id, err := ImportChrome(profilePath, testExtensionID, "Chrome-Linux", "1.1.0")
	if err != nil {
		t.Fatalf("ImportChrome returned error: %v", err)
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
	if id.Source != "chrome" {
		t.Fatalf("Source = %q", id.Source)
	}
}

func TestImportChromeReadsRawStringLevelDBValues(t *testing.T) {
	profilePath := t.TempDir()
	dbPath := filepath.Join(profilePath, "Local Extension Settings", testExtensionID)
	writeLevelDB(t, dbPath, map[string]string{
		"deviceUuid":  "du",
		"deviceToken": "dt",
		"pushToken":   "pt",
	})

	id, err := ImportChrome(profilePath, testExtensionID, "Chrome-Linux", "1.1.0")
	if err != nil {
		t.Fatalf("ImportChrome returned error: %v", err)
	}

	if id.DeviceUUID != "du" || id.DeviceToken != "dt" || id.PushToken != "pt" {
		t.Fatalf("identity fields = %#v", id)
	}
}

func TestImportChromeReadsLocalStorageExtensionKeys(t *testing.T) {
	profilePath := t.TempDir()
	dbPath := filepath.Join(profilePath, "Local Storage", "leveldb")
	prefix := "chrome-extension://" + testExtensionID + "/"
	writeLevelDB(t, dbPath, map[string]string{
		prefix + "deviceUuid":  `"du"`,
		prefix + "deviceToken": `"dt"`,
		prefix + "pushToken":   `"pt"`,
	})

	id, err := ImportChrome(profilePath, testExtensionID, "Chrome-Linux", "1.1.0")
	if err != nil {
		t.Fatalf("ImportChrome returned error: %v", err)
	}

	if id.DeviceUUID != "du" || id.DeviceToken != "dt" || id.PushToken != "pt" {
		t.Fatalf("identity fields = %#v", id)
	}
	if id.Source != "chrome" {
		t.Fatalf("Source = %q", id.Source)
	}
}

func TestImportChromeRequiresProfilePathAndExtensionID(t *testing.T) {
	if _, err := ImportChrome("", testExtensionID, "Chrome-Linux", "1.1.0"); err == nil {
		t.Fatal("expected missing profile path error")
	}
	if _, err := ImportChrome(t.TempDir(), "", "Chrome-Linux", "1.1.0"); err == nil {
		t.Fatal("expected missing extension id error")
	}
}

func writeLevelDB(t *testing.T, path string, pairs map[string]string) {
	t.Helper()

	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		t.Fatalf("open leveldb fixture: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close leveldb fixture: %v", err)
		}
	}()

	for key, value := range pairs {
		if err := db.Put([]byte(key), []byte(value), nil); err != nil {
			t.Fatalf("put %q: %v", key, err)
		}
	}
}
