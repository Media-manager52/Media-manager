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
