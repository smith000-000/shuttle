package patchapply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type Operation string

const (
	OperationCreate Operation = "create"
	OperationUpdate Operation = "update"
	OperationDelete Operation = "delete"
	OperationRename Operation = "rename"
)

type FileChange struct {
	Operation Operation
	OldPath   string
	NewPath   string
}

type Result struct {
	WorkspaceRoot string
	Validation    string
	Files         []FileChange
	Created       int
	Updated       int
	Deleted       int
	Renamed       int
}

type Service struct {
	root string
}

type preparedPatch struct {
	result    Result
	staged    []stagedFile
	originals map[string]originalFile
}

func New(root string) (*Service, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("workspace root is required")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}

	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", absoluteRoot)
	}

	return &Service{root: absoluteRoot}, nil
}

func (s *Service) Validate(ctx context.Context, patch string) (Result, error) {
	prepared, err := s.prepare(ctx, patch)
	if err != nil {
		return Result{}, err
	}
	return prepared.result, nil
}

func (s *Service) Apply(ctx context.Context, patch string) (Result, error) {
	prepared, err := s.prepare(ctx, patch)
	if err != nil {
		return Result{}, err
	}
	if err := s.commit(prepared.staged, prepared.originals); err != nil {
		return Result{}, err
	}
	return prepared.result, nil
}

type stagedFile struct {
	change   FileChange
	oldAbs   string
	newAbs   string
	data     []byte
	fileMode fs.FileMode
}

type originalFile struct {
	path     string
	existed  bool
	data     []byte
	fileMode fs.FileMode
}

func (s *Service) prepare(ctx context.Context, patch string) (preparedPatch, error) {
	if strings.TrimSpace(patch) == "" {
		return preparedPatch{}, errors.New("patch is empty")
	}
	if strings.Contains(patch, "*** Begin Patch") {
		return preparedPatch{}, errors.New("patch must be a unified diff; *** Begin Patch format is not supported")
	}

	files, preamble, err := gitdiff.Parse(strings.NewReader(patch + "\n"))
	if err != nil {
		return preparedPatch{}, fmt.Errorf("parse patch: %w", err)
	}
	if strings.TrimSpace(preamble) != "" {
		return preparedPatch{}, errors.New("patch contains unsupported preamble before the first diff")
	}
	if len(files) == 0 {
		return preparedPatch{}, errors.New("patch does not contain any file changes")
	}

	result := Result{
		WorkspaceRoot: s.root,
		Validation:    "native",
		Files:         make([]FileChange, 0, len(files)),
	}

	for index, file := range files {
		if file == nil {
			return preparedPatch{}, fmt.Errorf("patch file %d is nil", index+1)
		}
		change, err := s.validateFile(file)
		if err != nil {
			return preparedPatch{}, err
		}
		result.Files = append(result.Files, change)
		switch change.Operation {
		case OperationCreate:
			result.Created++
		case OperationUpdate:
			result.Updated++
		case OperationDelete:
			result.Deleted++
		case OperationRename:
			result.Renamed++
		}
	}
	if err := validatePatchPathConflicts(result.Files); err != nil {
		return preparedPatch{}, err
	}

	staged, originals, err := s.stageFiles(files)
	if err != nil {
		return preparedPatch{}, err
	}

	if err := s.runGitApplyCheck(ctx, patch); err != nil {
		return preparedPatch{}, err
	}
	if gitAvailable(s.root) {
		result.Validation = "native+git_apply_check"
	}

	return preparedPatch{
		result:    result,
		staged:    staged,
		originals: originals,
	}, nil
}

func (s *Service) validateFile(file *gitdiff.File) (FileChange, error) {
	if file.IsBinary {
		return FileChange{}, errors.New("binary patches are not supported")
	}
	if file.IsCopy {
		return FileChange{}, errors.New("copy patches are not supported")
	}
	if !isSupportedMode(file.OldMode) || !isSupportedMode(file.NewMode) {
		return FileChange{}, errors.New("only regular text-file patches are supported")
	}

	var (
		change FileChange
		err    error
	)

	switch {
	case file.IsRename:
		change.Operation = OperationRename
		change.OldPath, err = sanitizePatchPath(file.OldName)
		if err != nil {
			return FileChange{}, err
		}
		change.NewPath, err = sanitizePatchPath(file.NewName)
		if err != nil {
			return FileChange{}, err
		}
	case file.IsNew:
		change.Operation = OperationCreate
		change.NewPath, err = sanitizePatchPath(file.NewName)
		if err != nil {
			return FileChange{}, err
		}
	case file.IsDelete:
		change.Operation = OperationDelete
		change.OldPath, err = sanitizePatchPath(file.OldName)
		if err != nil {
			return FileChange{}, err
		}
	default:
		change.Operation = OperationUpdate
		change.OldPath, err = sanitizePatchPath(file.OldName)
		if err != nil {
			return FileChange{}, err
		}
		change.NewPath, err = sanitizePatchPath(file.NewName)
		if err != nil {
			return FileChange{}, err
		}
	}
	if change.Operation == OperationRename && change.OldPath == change.NewPath {
		return FileChange{}, fmt.Errorf("rename source and target are identical for %q", change.OldPath)
	}

	if !file.IsRename && !file.IsNew && !file.IsDelete && len(file.TextFragments) == 0 {
		return FileChange{}, errors.New("mode-only patches are not supported")
	}

	for _, fragment := range file.TextFragments {
		if fragment == nil {
			return FileChange{}, errors.New("patch contains a nil text fragment")
		}
		if err := fragment.Validate(); err != nil {
			return FileChange{}, fmt.Errorf("validate fragment for %s: %w", preferredPath(change), err)
		}
	}

	return change, nil
}

func validatePatchPathConflicts(changes []FileChange) error {
	if len(changes) == 0 {
		return nil
	}

	sources := make(map[string]Operation, len(changes))
	targets := make(map[string]Operation, len(changes))
	for _, change := range changes {
		if change.OldPath != "" {
			if prior, exists := sources[change.OldPath]; exists {
				return fmt.Errorf("patch touches source path %q more than once (%s and %s)", change.OldPath, prior, change.Operation)
			}
			sources[change.OldPath] = change.Operation
		}
		if change.NewPath != "" {
			if prior, exists := targets[change.NewPath]; exists {
				return fmt.Errorf("patch touches target path %q more than once (%s and %s)", change.NewPath, prior, change.Operation)
			}
			targets[change.NewPath] = change.Operation
		}
	}

	return nil
}

func sanitizePatchPath(path string) (string, error) {
	path = filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if path == "" || path == "." {
		return "", errors.New("patch contains an empty path")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path %q is not allowed", path)
	}
	if path == ".." || strings.HasPrefix(path, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the workspace root", path)
	}
	return path, nil
}

func preferredPath(change FileChange) string {
	if change.NewPath != "" {
		return change.NewPath
	}
	return change.OldPath
}

func isSupportedMode(mode fs.FileMode) bool {
	if mode == 0 {
		return true
	}
	switch mode & 0o170000 {
	case 0, 0o100000:
		return true
	default:
		return false
	}
}

func (s *Service) resolveWorkspacePath(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	absolute := filepath.Join(s.root, rel)
	absolute = filepath.Clean(absolute)
	if absolute != s.root && !strings.HasPrefix(absolute, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the workspace root", rel)
	}
	return absolute, nil
}

func (s *Service) stageFiles(files []*gitdiff.File) ([]stagedFile, map[string]originalFile, error) {
	staged := make([]stagedFile, 0, len(files))
	originals := make(map[string]originalFile)

	for _, file := range files {
		change, err := s.validateFile(file)
		if err != nil {
			return nil, nil, err
		}

		oldAbs, err := s.resolveWorkspacePath(change.OldPath)
		if err != nil {
			return nil, nil, err
		}
		newAbs, err := s.resolveWorkspacePath(change.NewPath)
		if err != nil {
			return nil, nil, err
		}

		oldOriginal, oldData, err := readOriginalFile(oldAbs)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", change.OldPath, err)
		}
		if oldAbs != "" {
			originals[oldAbs] = oldOriginal
		}

		newOriginal, _, err := readOriginalFile(newAbs)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", change.NewPath, err)
		}
		if newAbs != "" {
			originals[newAbs] = newOriginal
		}

		switch change.Operation {
		case OperationCreate:
			if newOriginal.existed {
				return nil, nil, fmt.Errorf("create target %q already exists", change.NewPath)
			}
		case OperationUpdate:
			if !oldOriginal.existed {
				return nil, nil, fmt.Errorf("update target %q does not exist", change.OldPath)
			}
		case OperationDelete:
			if !oldOriginal.existed {
				return nil, nil, fmt.Errorf("delete target %q does not exist", change.OldPath)
			}
		case OperationRename:
			if !oldOriginal.existed {
				return nil, nil, fmt.Errorf("rename source %q does not exist", change.OldPath)
			}
			if newAbs != "" && newAbs != oldAbs && newOriginal.existed {
				return nil, nil, fmt.Errorf("rename target %q already exists", change.NewPath)
			}
		}

		var output bytes.Buffer
		if err := gitdiff.Apply(&output, bytes.NewReader(oldData), file); err != nil {
			return nil, nil, fmt.Errorf("apply %s: %w", preferredPath(change), err)
		}

		staged = append(staged, stagedFile{
			change:   change,
			oldAbs:   oldAbs,
			newAbs:   newAbs,
			data:     output.Bytes(),
			fileMode: chooseFileMode(file, oldOriginal, newOriginal),
		})
	}

	return staged, originals, nil
}

func chooseFileMode(file *gitdiff.File, oldOriginal originalFile, newOriginal originalFile) fs.FileMode {
	switch {
	case file.NewMode != 0:
		return file.NewMode.Perm()
	case newOriginal.existed:
		return newOriginal.fileMode.Perm()
	case oldOriginal.existed:
		return oldOriginal.fileMode.Perm()
	default:
		return 0o644
	}
}

func readOriginalFile(path string) (originalFile, []byte, error) {
	if path == "" {
		return originalFile{}, nil, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return originalFile{path: path}, nil, nil
		}
		return originalFile{}, nil, err
	}
	if !info.Mode().IsRegular() {
		return originalFile{}, nil, fmt.Errorf("%q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return originalFile{}, nil, err
	}
	return originalFile{
		path:     path,
		existed:  true,
		data:     data,
		fileMode: info.Mode().Perm(),
	}, data, nil
}

func (s *Service) commit(staged []stagedFile, originals map[string]originalFile) error {
	touched := make([]string, 0, len(staged)*2)

	for _, file := range staged {
		if err := s.commitFile(file); err != nil {
			s.rollback(touched, originals)
			return err
		}
		for _, path := range []string{file.oldAbs, file.newAbs} {
			if path == "" || slices.Contains(touched, path) {
				continue
			}
			touched = append(touched, path)
		}
	}

	return nil
}

func (s *Service) commitFile(file stagedFile) error {
	switch file.change.Operation {
	case OperationDelete:
		if err := os.Remove(file.oldAbs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %q: %w", file.change.OldPath, err)
		}
		return nil
	case OperationRename:
		if err := writeFileAtomically(file.newAbs, file.data, file.fileMode); err != nil {
			return fmt.Errorf("write rename target %q: %w", file.change.NewPath, err)
		}
		if file.oldAbs != "" && file.oldAbs != file.newAbs {
			if err := os.Remove(file.oldAbs); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove rename source %q: %w", file.change.OldPath, err)
			}
		}
		return nil
	case OperationCreate, OperationUpdate:
		if err := writeFileAtomically(file.newAbs, file.data, file.fileMode); err != nil {
			return fmt.Errorf("write %q: %w", preferredPath(file.change), err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported operation %q", file.change.Operation)
	}
}

func writeFileAtomically(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	temp, err := os.CreateTemp(dir, ".shuttle-patch-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (s *Service) rollback(paths []string, originals map[string]originalFile) {
	for index := len(paths) - 1; index >= 0; index-- {
		original := originals[paths[index]]
		if original.path == "" {
			continue
		}
		if !original.existed {
			_ = os.Remove(original.path)
			continue
		}
		_ = writeFileAtomically(original.path, original.data, original.fileMode)
	}
}

func (s *Service) runGitApplyCheck(ctx context.Context, patch string) error {
	if !gitAvailable(s.root) {
		return nil
	}

	check := exec.CommandContext(ctx, "git", "-C", s.root, "apply", "--check", "-")
	check.Stdin = strings.NewReader(patch + "\n")
	output, err := check.CombinedOutput()
	if err == nil {
		return nil
	}

	text := strings.TrimSpace(string(output))
	if text == "" {
		text = err.Error()
	}
	return fmt.Errorf("git apply --check: %s", text)
}

func gitAvailable(root string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	check := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
	return check.Run() == nil
}
