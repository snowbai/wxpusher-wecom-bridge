package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
	"github.com/syndtr/goleveldb/leveldb"
)

var chromeIdentityFields = map[string]struct{}{
	"deviceUuid":  {},
	"deviceToken": {},
	"pushToken":   {},
}

func ImportChrome(profilePath, extensionID, defaultPlatform, defaultVersion string) (identity.Identity, error) {
	if strings.TrimSpace(profilePath) == "" {
		return identity.Identity{}, errors.New("profile path is required")
	}
	if err := validateChromeExtensionID(extensionID); err != nil {
		return identity.Identity{}, err
	}

	values := make(map[string]string)
	localExtensionSettings := filepath.Join(profilePath, "Local Extension Settings", extensionID)
	if dirExists(localExtensionSettings) {
		localValues, err := readLevelDBIdentity(localExtensionSettings, func(key string) bool {
			_, ok := chromeIdentityFields[normalizeChromeStorageKey(key)]
			return ok
		})
		if err != nil {
			return identity.Identity{}, fmt.Errorf("read Local Extension Settings: %w", err)
		}
		mergeIdentityValues(values, localValues)
	}

	localStorage := filepath.Join(profilePath, "Local Storage", "leveldb")
	if dirExists(localStorage) {
		localValues, err := readLevelDBIdentity(localStorage, func(key string) bool {
			return strings.Contains(key, "chrome-extension://"+extensionID)
		})
		if err != nil {
			return identity.Identity{}, fmt.Errorf("read Local Storage: %w", err)
		}
		mergeIdentityValues(values, localValues)
	}

	id := identity.Identity{
		DeviceUUID:  values["deviceUuid"],
		DeviceToken: values["deviceToken"],
		PushToken:   values["pushToken"],
	}
	id = id.WithDefaults(defaultPlatform, defaultVersion, "chrome")
	if err := id.Validate(); err != nil {
		return identity.Identity{}, err
	}
	return id, nil
}

func validateChromeExtensionID(extensionID string) error {
	if len(extensionID) != 32 {
		return errors.New("extension id must be exactly 32 characters")
	}
	for _, r := range extensionID {
		if r < 'a' || r > 'p' {
			return errors.New("extension id must contain only characters a-p")
		}
	}
	return nil
}

func readLevelDBIdentity(path string, keyFilter func(string) bool) (map[string]string, error) {
	tempDir, err := os.MkdirTemp("", "wxpusher-chrome-leveldb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	copyPath := filepath.Join(tempDir, "db")
	if err := copyDir(path, copyPath); err != nil {
		return nil, err
	}

	db, err := leveldb.OpenFile(copyPath, nil)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	values := make(map[string]string)
	iter := db.NewIterator(nil, nil)
	defer iter.Release()
	for iter.Next() {
		key := string(iter.Key())
		if !keyFilter(key) {
			continue
		}
		normalizedKey := normalizeChromeStorageKey(key)
		if _, ok := chromeIdentityFields[normalizedKey]; !ok {
			continue
		}
		value, err := decodeChromeStorageValue(iter.Value())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", normalizedKey, err)
		}
		values[normalizedKey] = value
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return values, nil
}

func normalizeChromeStorageKey(key string) string {
	key = strings.Trim(key, "\x00")
	for field := range chromeIdentityFields {
		if key == field || strings.HasSuffix(key, "/"+field) || strings.HasSuffix(key, "\x00"+field) {
			return field
		}
	}
	return key
}

func decodeChromeStorageValue(value []byte) (string, error) {
	raw := strings.TrimSpace(string(value))
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		value, ok := decoded.(string)
		if !ok {
			return "", errors.New("JSON storage value must be a string")
		}
		return value, nil
	}
	return raw, nil
}

func mergeIdentityValues(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
