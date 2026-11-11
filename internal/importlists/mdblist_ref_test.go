package importlists

import (
	"errors"
	"testing"
)

func TestParseMDBListURLAcceptsCopiedAddresses(t *testing.T) {
	want := MDBListRef{Owner: "someone", Slug: "best-danish-films"}
	for _, input := range []string{
		"https://mdblist.com/lists/someone/best-danish-films",
		"https://mdblist.com/lists/someone/best-danish-films/",
		"https://www.mdblist.com/lists/someone/best-danish-films/json",
		"https://mdblist.com/lists/someone/best-danish-films?sort=rank#top",
		"http://mdblist.com/lists/someone/best-danish-films",
		"mdblist.com/lists/someone/best-danish-films",
		"  someone/best-danish-films  ",
	} {
		got, err := ParseMDBListURL(input)
		if err != nil || got != want {
			t.Errorf("ParseMDBListURL(%q) = %+v, %v; want %+v", input, got, err, want)
		}
	}
	if want.URL() != "https://mdblist.com/lists/someone/best-danish-films" {
		t.Fatalf("URL() = %q", want.URL())
	}
}

func TestParseMDBListURLRefusesOtherAddresses(t *testing.T) {
	for _, input := range []string{
		"",
		"https://evil.example/lists/someone/list",
		"https://mdblist.com.evil.example/lists/someone/list",
		"https://mdblist.com:8443/lists/someone/list",
		"https://user@mdblist.com/lists/someone/list",
		"https://mdblist.com/movie/tt0111161",
		"https://mdblist.com/lists/someone",
		"https://mdblist.com/lists/someone/list/extra",
		"https://mdblist.com/lists/../list",
		"someone/list with spaces",
		"ftp://mdblist.com/lists/someone/list",
	} {
		if got, err := ParseMDBListURL(input); !errors.Is(err, ErrInvalidMDBListURL) {
			t.Errorf("ParseMDBListURL(%q) = %+v, %v; want ErrInvalidMDBListURL", input, got, err)
		}
	}
}

func TestParseMDBListURLKeepsDanishLetters(t *testing.T) {
	want := MDBListRef{Owner: "søren", Slug: "julefilm-på-dansk-æøå"}
	for _, input := range []string{
		"https://mdblist.com/lists/søren/julefilm-på-dansk-æøå",
		"https://mdblist.com/lists/s%C3%B8ren/julefilm-p%C3%A5-dansk-%C3%A6%C3%B8%C3%A5/",
		"mdblist.com/lists/søren/julefilm-på-dansk-æøå/json",
		"søren/julefilm-på-dansk-æøå",
		"s%C3%B8ren/julefilm-p%C3%A5-dansk-%C3%A6%C3%B8%C3%A5",
	} {
		got, err := ParseMDBListURL(input)
		if err != nil || got != want {
			t.Errorf("ParseMDBListURL(%q) = %+v, %v; want %+v", input, got, err, want)
		}
	}
	if got := want.URL(); got != "https://mdblist.com/lists/s%C3%B8ren/julefilm-p%C3%A5-dansk-%C3%A6%C3%B8%C3%A5" {
		t.Fatalf("URL() = %q", got)
	}
	for _, input := range []string{"someone/a%2Fb", "someone/%zz", "someone/a%20b", "someone/%2E%2E", "someone/a\\b", "someone/a%25b"} {
		if got, err := ParseMDBListURL(input); !errors.Is(err, ErrInvalidMDBListURL) {
			t.Errorf("ParseMDBListURL(%q) = %+v, %v; want ErrInvalidMDBListURL", input, got, err)
		}
	}
}
