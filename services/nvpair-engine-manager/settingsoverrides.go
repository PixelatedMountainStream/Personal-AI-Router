// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"

	"nvpair-shared/appdir"
	"nvpair-shared/noderec"
)

type overridesCache struct {
	mu   sync.Mutex
	last map[string]json.RawMessage
}

var settingsCache overridesCache

// settingsFileOverrides reads the engine capacity overrides node-settings keeps
// in settings.json under noderec.SettingsFileKey. An absent file or absent key
// means no overrides; a read or parse failure keeps the last valid overrides so
// a torn write cannot flip an explicit jit_load=false back to the default.
func settingsFileOverrides() map[string]json.RawMessage {
	path, err := appdir.Path("settings.json")
	if err != nil {
		return settingsCache.get("")
	}
	return settingsCache.get(path)
}

func (c *overridesCache) get(path string) map[string]json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, err := readSettingsOverrides(path)
	if err != nil {
		return c.last
	}
	c.last = raw
	return raw
}

// readSettingsOverrides returns (nil, nil) for absent settings and an error
// for any failure to read or parse them.
func readSettingsOverrides(path string) (map[string]json.RawMessage, error) {
	if path == "" {
		return nil, errors.New("no settings path")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	sec, ok := doc[noderec.SettingsFileKey]
	if !ok || string(sec) == "null" {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(sec, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
