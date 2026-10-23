package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyReplacesLongerDestination(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
	if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old trailing records"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "new" {
		t.Fatalf("copy=%q err=%v", got, err)
	}
}
