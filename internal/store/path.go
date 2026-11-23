package store

import (
	"errors"
	"path/filepath"
)

// DataDirEnv overrides the folder holding the history database. Open tightens
// that folder to mode 0700, so it must name a folder used only for this.
const DataDirEnv = "COLIMA_UI_DATA_DIR"

// FileName is the database file name inside the data folder.
const FileName = "history.db"

// DefaultPath resolves the database file: $COLIMA_UI_DATA_DIR/history.db if the
// variable is set (it must be absolute), else <UserConfigDir>/colima-ui/history.db.
// getenv and userConfigDir are injected (pass os.Getenv and os.UserConfigDir)
// so tests never touch the real folder.
func DefaultPath(getenv func(string) string, userConfigDir func() (string, error)) (string, error) {
	if v := getenv(DataDirEnv); v != "" {
		if !filepath.IsAbs(v) {
			return "", errors.New("store: " + DataDirEnv + " must be an absolute path")
		}
		return filepath.Join(v, FileName), nil
	}
	base, err := userConfigDir()
	if err != nil {
		return "", errors.New("store: cannot locate the user config folder")
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("store: the user config folder is not absolute")
	}
	return filepath.Join(base, "colima-ui", FileName), nil
}
