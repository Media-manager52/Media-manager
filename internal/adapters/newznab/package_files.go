package newznab

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// PackageFiles returns the subject labels declared by the NZB package file
// manifest. Media Manager uses it to screen archive-only releases before they
// are submitted to a download client, so the walk is bounded exactly like
// PackageIdentity and never trusts anything outside the file elements.
func PackageFiles(body []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	files := []string{}
	rootSeen := false
	tokens := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		tokens++
		if tokens > 4_000_000 {
			return nil, ErrInvalidNZB
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if !rootSeen {
			if !strings.EqualFold(start.Name.Local, "nzb") {
				return nil, ErrInvalidNZB
			}
			rootSeen = true
			continue
		}
		if !strings.EqualFold(start.Name.Local, "file") {
			continue
		}
		for _, attribute := range start.Attr {
			if strings.EqualFold(attribute.Name.Local, "subject") {
				files = append(files, attribute.Value)
				break
			}
		}
	}
	if !rootSeen {
		return nil, ErrInvalidNZB
	}
	return files, nil
}
