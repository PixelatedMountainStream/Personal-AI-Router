package main

import (
	"encoding/json"
	"os"

	"nvpair-shared/appdir"
	"nvpair-shared/noderec"
)

// settingsFileOverrides reads the engine capacity overrides node-settings keeps
// in settings.json under noderec.SettingsFileKey. A missing or unreadable file
// means no overrides.
func settingsFileOverrides() map[string]json.RawMessage {
	path, err := appdir.Path("settings.json")
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(doc[noderec.SettingsFileKey], &raw) != nil {
		return nil
	}
	return raw
}
