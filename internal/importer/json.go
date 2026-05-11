package importer

import (
	"encoding/json"
	"os"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

func ImportJSON(path, defaultPlatform, defaultVersion string) (identity.Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return identity.Identity{}, err
	}

	var id identity.Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return identity.Identity{}, err
	}

	id = id.WithDefaults(defaultPlatform, defaultVersion, "json")
	if err := id.Validate(); err != nil {
		return identity.Identity{}, err
	}
	return id, nil
}
