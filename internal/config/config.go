// Package config는 navermap 커맨드들이 공유하는 설정 파일 로더.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Lists  []ListEntry `json:"lists"`
	Notion Notion      `json:"notion"`
}

type ListEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// Visited 리스트에 있는 장소는 Notion에서 "가봤음"으로 표시된다.
	Visited bool `json:"visited,omitempty"`
}

type Notion struct {
	DatabaseID string `json:"databaseId"`
}

// Load는 설정 파일을 읽는다. 파일이 없으면 빈 설정을 돌려준다.
func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("설정 파일 읽기 실패 %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("설정 파일 파싱 실패 %s: %w", path, err)
	}
	return cfg, nil
}
