package altmount

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnumerateRejectsInvalidUTF8WithoutChangingFiles(t *testing.T) {
	for _, name := range []string{"movie-\xfc.mkv", "folder-\xe4/movie.mkv"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "job", name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
				t.Fatal(err)
			}
			inventory, err := Enumerate(OutputMapping{ReportedRoot: "/", LocalReadOnlyRoot: root}, "/job")
			if !errors.Is(err, ErrOutputEncoding) || len(inventory.Files) != 0 {
				t.Fatalf("invalid filename accepted: inventory=%+v err=%v", inventory, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "video" {
				t.Fatalf("source changed: %q err=%v", data, err)
			}
		})
	}
}

func TestEnumeratePreservesValidUnicodeExactly(t *testing.T) {
	root := t.TempDir()
	name := "Blåbær-映画.mkv"
	if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory, err := Enumerate(OutputMapping{ReportedRoot: "/", LocalReadOnlyRoot: root}, "/")
	if err != nil || len(inventory.Files) != 1 || inventory.Files[0].RelativePath != name {
		t.Fatalf("valid Unicode filename changed: inventory=%+v err=%v", inventory, err)
	}
}

func TestEnumerateKeepsColonFileNames(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "media-manager", "miraculous-s05e05--mm-01a14ec6")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "Miraculous:.Tales.of.Ladybug.and.Cat.Noir.S05E05.1080p.mkv"
	if err := os.WriteFile(filepath.Join(folder, name), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory, err := Enumerate(OutputMapping{ReportedRoot: "/", LocalReadOnlyRoot: root}, "/media-manager/miraculous-s05e05--mm-01a14ec6")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Files) != 1 || inventory.Files[0].RelativePath != name || inventory.Files[0].Size != 5 {
		t.Fatalf("inventory = %+v", inventory)
	}
}
