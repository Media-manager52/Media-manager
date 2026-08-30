package adoption

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanIsBoundedAndDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Movie"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name := range map[string]bool{"Movie.mkv": true, "Movie.en.srt": true, "Movie.sample.txt": true, "unrelated.nfo": true} {
		if err := os.WriteFile(filepath.Join(root, "Movie", name), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Hidden.mkv"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	items, err := Scan(context.Background(), root, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].Relative != "Movie/Movie.mkv" || len(items[0].RecognizedCompanions) != 1 || len(items[0].UnknownCompanions) != 1 {
		t.Fatalf("unexpected discovery item: %+v", items[0])
	}
	if _, err := Scan(context.Background(), root, 2); err != ErrScanLimit {
		t.Fatalf("scan limit error=%v", err)
	}
}
