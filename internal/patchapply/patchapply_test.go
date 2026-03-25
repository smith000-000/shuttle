package patchapply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceApplyCreatesUpdatesDeletesAndRenames(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "update.txt", "before\n")
	mustWriteFile(t, root, "delete.txt", "remove me\n")
	mustWriteFile(t, root, "rename.txt", "rename me\n")

	service, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/create.txt b/create.txt
new file mode 100644
--- /dev/null
+++ b/create.txt
@@ -0,0 +1 @@
+created
diff --git a/update.txt b/update.txt
index 4f87e0b..38a1f73 100644
--- a/update.txt
+++ b/update.txt
@@ -1 +1 @@
-before
+after
diff --git a/delete.txt b/delete.txt
deleted file mode 100644
--- a/delete.txt
+++ /dev/null
@@ -1 +0,0 @@
-remove me
diff --git a/rename.txt b/renamed.txt
similarity index 100%
rename from rename.txt
rename to renamed.txt
`)

	result, err := service.Apply(context.Background(), patch)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Created != 1 || result.Updated != 1 || result.Deleted != 1 || result.Renamed != 1 {
		t.Fatalf("unexpected result %#v", result)
	}
	assertFileContent(t, filepath.Join(root, "create.txt"), "created\n")
	assertFileContent(t, filepath.Join(root, "update.txt"), "after\n")
	if _, err := os.Stat(filepath.Join(root, "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected delete.txt to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "rename.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected rename.txt to be removed, stat err = %v", err)
	}
	assertFileContent(t, filepath.Join(root, "renamed.txt"), "rename me\n")
}

func TestServiceApplyRejectsOutsideRootPath(t *testing.T) {
	service, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/../escape.txt b/../escape.txt
new file mode 100644
--- /dev/null
+++ b/../escape.txt
@@ -0,0 +1 @@
+escape
`)

	if _, err := service.Apply(context.Background(), patch); err == nil || !strings.Contains(err.Error(), "escapes the workspace root") {
		t.Fatalf("expected outside-root rejection, got %v", err)
	}
}

func TestServiceApplyRejectsBinaryPatch(t *testing.T) {
	service, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/bin.dat b/bin.dat
new file mode 100644
index 0000000..e69de29
GIT binary patch
literal 3
KcmZQzWC8#H2LJ>B
`)

	if _, err := service.Apply(context.Background(), patch); err == nil {
		t.Fatal("expected binary rejection")
	} else if !strings.Contains(err.Error(), "binary") && !strings.Contains(err.Error(), "parse patch") {
		t.Fatalf("expected binary-related rejection, got %v", err)
	}
}

func TestServiceApplyRejectsSymlinkPatch(t *testing.T) {
	service, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/link b/link
new file mode 120000
--- /dev/null
+++ b/link
@@ -0,0 +1 @@
+/tmp/target
`)

	if _, err := service.Apply(context.Background(), patch); err == nil || !strings.Contains(err.Error(), "regular text-file patches") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestServiceApplyRollsBackOnCommitFailure(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "ok.txt", "before\n")
	mustWriteFile(t, root, "blocked.txt", "before\n")
	blocker := filepath.Join(root, "dir-blocker")
	mustWriteFile(t, root, "dir-blocker", "not a directory\n")

	service, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/ok.txt b/ok.txt
index 4f87e0b..38a1f73 100644
--- a/ok.txt
+++ b/ok.txt
@@ -1 +1 @@
-before
+after
diff --git a/blocked.txt b/dir-blocker/out.txt
similarity index 100%
rename from blocked.txt
rename to dir-blocker/out.txt
`)

	if _, err := service.Apply(context.Background(), patch); err == nil {
		t.Fatal("expected commit failure")
	}

	assertFileContent(t, filepath.Join(root, "ok.txt"), "before\n")
	assertFileContent(t, blocker, "not a directory\n")
	assertFileContent(t, filepath.Join(root, "blocked.txt"), "before\n")
}

func TestServiceValidateRejectsBeginPatchFormat(t *testing.T) {
	service, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
*** Begin Patch
*** Update File: main.go
+hello
*** End Patch
`)

	if _, err := service.Validate(context.Background(), patch); err == nil || !strings.Contains(err.Error(), "unified diff") {
		t.Fatalf("expected unified-diff rejection, got %v", err)
	}
}

func TestServiceApplyPreservesWhitespaceOnlyTrailingHunkLine(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "spaces.txt", "alpha\n")

	service, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := "" +
		"diff --git a/spaces.txt b/spaces.txt\n" +
		"--- a/spaces.txt\n" +
		"+++ b/spaces.txt\n" +
		"@@ -1 +1,2 @@\n" +
		" alpha\n" +
		"+   "

	if _, err := service.Apply(context.Background(), patch); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	assertFileContent(t, filepath.Join(root, "spaces.txt"), "alpha\n   \n")
}

func TestServiceValidateRejectsStaleContentWithoutGitRepo(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "hello.txt", "current\n")

	service, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/hello.txt b/hello.txt
--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-expected-old
+updated
`)

	if _, err := service.Validate(context.Background(), patch); err == nil || !strings.Contains(err.Error(), "apply hello.txt") {
		t.Fatalf("expected native preflight rejection, got %v", err)
	}
}

func TestServiceValidateRejectsDuplicateTargetPath(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "one.txt", "one\n")
	mustWriteFile(t, root, "two.txt", "two\n")

	service, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/one.txt b/shared.txt
similarity index 100%
rename from one.txt
rename to shared.txt
diff --git a/two.txt b/shared.txt
similarity index 100%
rename from two.txt
rename to shared.txt
`)

	if _, err := service.Validate(context.Background(), patch); err == nil || !strings.Contains(err.Error(), "target path") {
		t.Fatalf("expected duplicate target rejection, got %v", err)
	}
}

func TestServiceValidateRejectsSelfRename(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "same.txt", "same\n")

	service, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	patch := strings.TrimSpace(`
diff --git a/same.txt b/same.txt
similarity index 100%
rename from same.txt
rename to same.txt
`)

	if _, err := service.Validate(context.Background(), patch); err == nil || !strings.Contains(err.Error(), "identical") {
		t.Fatalf("expected self-rename rejection, got %v", err)
	}
}

func mustWriteFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("content %s = %q, want %q", path, data, want)
	}
}
