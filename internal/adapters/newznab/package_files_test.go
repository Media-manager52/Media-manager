package newznab

import "testing"

func TestPackageFilesExtractsFileSubjects(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
		<file poster="poster-a" subject="Movie.part01.rar yEnc (1/2)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">one@example</segment></segments></file>
		<file poster="poster-b" subject="Movie.part02.rar yEnc (2/2)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">two@example</segment></segments></file>
	</nzb>`)
	files, err := PackageFiles(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "Movie.part01.rar yEnc (1/2)" || files[1] != "Movie.part02.rar yEnc (2/2)" {
		t.Fatalf("files=%v", files)
	}
}

func TestPackageFilesRejectsNonNZBDocument(t *testing.T) {
	if _, err := PackageFiles([]byte(`<rss><channel></channel></rss>`)); err == nil {
		t.Fatal("expected a non-NZB document to be rejected")
	}
}
