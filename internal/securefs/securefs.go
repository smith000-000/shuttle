package securefs

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func EnsurePrivateDir(path string) error {
	if path == "" {
		return fmt.Errorf("directory path must not be empty")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func OpenFileNoFollow(path string, flags int, perm os.FileMode) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(perm))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func OpenAppendPrivate(path string, perm os.FileMode) (*os.File, error) {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	file, err := OpenFileNoFollow(path, syscall.O_CREAT|syscall.O_APPEND|syscall.O_WRONLY, perm)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func EnsureFilePrivate(path string, perm os.FileMode) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	file, err := OpenFileNoFollow(path, syscall.O_CREAT|syscall.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Chmod(perm)
}

func WriteExclusivePrivate(path string, data []byte, perm os.FileMode) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	file, err := OpenFileNoFollow(path, syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Chmod(perm)
}

func WriteAtomicPrivate(path string, data []byte, perm os.FileMode) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	if err := tempFile.Chmod(perm); err != nil {
		return err
	}
	if _, err := tempFile.Write(data); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}
