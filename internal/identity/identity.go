package identity

import (
	"errors"
	"strings"
	"time"
)

type Identity struct {
	DeviceUUID  string    `json:"deviceUuid"`
	DeviceToken string    `json:"deviceToken"`
	PushToken   string    `json:"pushToken"`
	Platform    string    `json:"platform"`
	Version     string    `json:"version"`
	Source      string    `json:"source"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (i Identity) Validate() error {
	var problems []string
	if strings.TrimSpace(i.DeviceUUID) == "" {
		problems = append(problems, "deviceUuid is required")
	}
	if strings.TrimSpace(i.DeviceToken) == "" {
		problems = append(problems, "deviceToken is required")
	}
	if strings.TrimSpace(i.Platform) == "" {
		problems = append(problems, "platform is required")
	}
	if strings.TrimSpace(i.Version) == "" {
		problems = append(problems, "version is required")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (i Identity) WithDefaults(platform, version, source string) Identity {
	if i.Platform == "" {
		i.Platform = platform
	}
	if i.Version == "" {
		i.Version = version
	}
	if i.Source == "" {
		i.Source = source
	}
	if i.UpdatedAt.IsZero() {
		i.UpdatedAt = time.Now().UTC()
	}
	return i
}
