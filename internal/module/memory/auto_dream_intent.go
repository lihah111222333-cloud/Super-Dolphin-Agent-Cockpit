package memory

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// autoDreamIntentFileName is the on-disk record of the user's last manual
// toggle for the auto-dream stop hook. It lives next to the memory root so
// NewConfig can pick it up without taking a dependency on uistate.
const autoDreamIntentFileName = "auto-dream-intent.json"

type autoDreamIntentFile struct {
	Enabled bool `json:"enabled"`
}

// ReadAutoDreamIntent returns the user's persisted auto-dream toggle.
// (nil, nil) means "no manual override" — env defaults still apply.
func ReadAutoDreamIntent(rootDir string) (*bool, error) {
	path := autoDreamIntentPath(rootDir)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var payload autoDreamIntentFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	v := payload.Enabled
	return &v, nil
}

// WriteAutoDreamIntent persists the user's auto-dream toggle atomically.
func WriteAutoDreamIntent(rootDir string, enabled bool) error {
	path := autoDreamIntentPath(rootDir)
	if path == "" {
		return errors.New("auto-dream intent: empty root dir")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(autoDreamIntentFile{Enabled: enabled})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auto-dream-intent-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func autoDreamIntentPath(rootDir string) string {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return ""
	}
	return filepath.Join(rootDir, autoDreamIntentFileName)
}
