package importer

import (
	"os"
	"path/filepath"
	"strings"
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

func TestImportChromeIgnoresJSONNonStringLevelDBValues(t *testing.T) {
	profilePath := t.TempDir()
	dbPath := filepath.Join(profilePath, "Local Extension Settings", testExtensionID)
	writeLevelDB(t, dbPath, map[string]string{
		"deviceUuid":  `{"value":"du"}`,
		"deviceToken": `123`,
		"pushToken":   "pt",
	})

	_, err := ImportChrome(profilePath, testExtensionID, "Chrome-Linux", "1.1.0")
	if err == nil {
		t.Fatal("expected validation error for ignored non-string JSON identity values")
	}
	if !strings.Contains(err.Error(), "deviceUuid is required") || !strings.Contains(err.Error(), "deviceToken is required") {
		t.Fatalf("error = %q", err)
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

func TestImportChromeRejectsInvalidExtensionIDs(t *testing.T) {
	tests := []string{
		"../x",
		"abc/def",
		"abcdefghijklmnop",
		"abcdefghijklmnopabcdefghijklmnzq",
	}

	for _, extensionID := range tests {
		t.Run(extensionID, func(t *testing.T) {
			_, err := ImportChrome(t.TempDir(), extensionID, "Chrome-Linux", "1.1.0")
			if err == nil {
				t.Fatal("expected invalid extension id error")
			}
			if !strings.Contains(err.Error(), "extension id") {
				t.Fatalf("error = %q", err)
			}
		})
	}
}

func TestImportChromeWrapsLocalExtensionSettingsErrors(t *testing.T) {
	profilePath := t.TempDir()
	dbPath := filepath.Join(profilePath, "Local Extension Settings", testExtensionID)
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatalf("mkdir corrupt db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dbPath, "CURRENT"), []byte("missing manifest\n"), 0o600); err != nil {
		t.Fatalf("write corrupt db file: %v", err)
	}

	_, err := ImportChrome(profilePath, testExtensionID, "Chrome-Linux", "1.1.0")
	if err == nil {
		t.Fatal("expected corrupt leveldb error")
	}
	if !strings.Contains(err.Error(), "Local Extension Settings") {
		t.Fatalf("error = %q", err)
	}
}

func TestImportChromeWrapsLocalStorageErrors(t *testing.T) {
	profilePath := t.TempDir()
	dbPath := filepath.Join(profilePath, "Local Storage", "leveldb")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatalf("mkdir corrupt db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dbPath, "CURRENT"), []byte("missing manifest\n"), 0o600); err != nil {
		t.Fatalf("write corrupt db file: %v", err)
	}

	_, err := ImportChrome(profilePath, testExtensionID, "Chrome-Linux", "1.1.0")
	if err == nil {
		t.Fatal("expected corrupt leveldb error")
	}
	if !strings.Contains(err.Error(), "Local Storage") {
		t.Fatalf("error = %q", err)
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
