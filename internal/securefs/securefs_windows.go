//go:build windows

package securefs

import (
	"errors"
	"os"
)

func openFileNoFollow(path string, flags int, perm os.FileMode) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("refusing to open symlink path")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return os.OpenFile(path, flags, perm)
}
