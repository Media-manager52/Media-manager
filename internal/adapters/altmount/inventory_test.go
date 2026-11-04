package altmount

import (
	"os"
	"path/filepath"
	"testing"
)

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
