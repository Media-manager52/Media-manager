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

func TestScanSkipsTheRecycleBinAndKeepsSiblingVideosApart(t *testing.T) {
	root := t.TempDir()
	write := func(relative string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".media-manager-recycle/entry/Old Movie (2020)/Old Movie (2020).mkv")
	write("Movie/Movie.mkv")
	write("Movie/Movie.extra.mkv")
	write("Movie/Movie.da.srt")
	items, err := Scan(context.Background(), root, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for _, item := range items {
		if item.Relative != "Movie/Movie.extra.mkv" && item.Relative != "Movie/Movie.mkv" {
			t.Fatalf("a retained recycle-bin file was offered for adoption: %+v", item)
		}
		for _, companion := range append(append([]string{}, item.RecognizedCompanions...), item.UnknownCompanions...) {
			if filepath.Ext(companion) == ".mkv" {
				t.Fatalf("a second video was treated as a companion: %+v", item)
			}
		}
	}
	if _, err := Scan(context.Background(), filepath.Join(root, ".media-manager-recycle"), 10); err != ErrUnsafeRoot {
		t.Fatalf("scanning inside the recycle bin error=%v", err)
	}
}
