package releases

import "testing"

func TestClassifyPackageFiles(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		kind  PackageKind
		code  string
	}{
		{"single playable file", []string{`"Movie.2024.1080p.WEB-DL.mkv" yEnc (1/100)`}, PackagePlayable, ""},
		{"season pack of playable files", []string{`"Show.S01E01.mkv" yEnc`, `"Show.S01E02.mkv" yEnc`}, PackagePlayable, ""},
		{"modern rar set", []string{`"Movie.2024.part01.rar" yEnc`, `"Movie.2024.part02.rar" yEnc`}, PackageStreamableArchive, ""},
		{"legacy rar set", []string{`Movie.rar yEnc`, `Movie.r00 yEnc`, `Movie.r01 yEnc`}, PackageStreamableArchive, ""},
		{"seven zip set", []string{`Movie.7z yEnc`}, PackageStreamableArchive, ""},
		{"nordic scene rar set", []string{`"ncis.s16.nordic.1080p.web.h264-egen.rar" yEnc`, `"ncis.s16.nordic.1080p.web.h264-egen.r00" yEnc`, `"ncis.s16.nordic.1080p.web.h264-egen.nfo" yEnc`}, PackageStreamableArchive, ""},
		{"zip archive", []string{`Movie.zip yEnc`}, PackageUnsupported, PackageFailureArchiveUnsupported},
		{"tarball", []string{`Movie.tar.gz yEnc`}, PackageUnsupported, PackageFailureArchiveUnsupported},
		{"iso only", []string{`Movie.DVDR-UNiTY.iso yEnc`}, PackageUnsupported, PackageFailureNoPlayableMedia},
		{"dvd structure only", []string{`VIDEO_TS.VOB yEnc`}, PackageUnsupported, PackageFailureNoPlayableMedia},
		{"sample beside rar set", []string{`Movie.part01.rar yEnc`, `Movie.sample.mkv yEnc`}, PackageStreamableArchive, ""},
		{"rar set beside iso", []string{`Movie.part01.rar yEnc`, `Movie.iso yEnc`}, PackageStreamableArchive, ""},
		{"obfuscated manifest", []string{`aGVsbG8gd29ybGQ yEnc`, `Zm9vYmFy yEnc`}, PackageUnknown, ""},
		{"companions only", []string{`Movie.nfo`, `Movie.par2`}, PackageUnknown, ""},
		{"empty manifest", nil, PackageUnknown, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification := ClassifyPackageFiles(test.files)
			if classification.Kind != test.kind || classification.Code != test.code {
				t.Fatalf("classification=%+v, want kind=%s code=%s", classification, test.kind, test.code)
			}
		})
	}
}
