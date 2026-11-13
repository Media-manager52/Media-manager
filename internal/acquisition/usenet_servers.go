package acquisition

import (
	"errors"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
)

// Usenet servers are AltMount's NNTP providers. The HTTP API manages them
// through these names, so it never imports the AltMount adapter itself.
type (
	UsenetServer       = altmount.Provider
	UsenetServerInput  = altmount.ProviderInput
	UsenetServerTest   = altmount.ProviderTest
	UsenetServerClient = altmount.HTTPClient
)

// ErrInvalidUsenetServerID is returned for an ID AltMount could not have
// issued; no request is sent.
var ErrInvalidUsenetServerID = altmount.ErrInvalidProviderID

// UsenetServerRefusal reports the HTTP status and reason of a request
// AltMount answered with an error. ok is false for any other error, such as
// AltMount not answering.
func UsenetServerRefusal(err error) (status int, message string, ok bool) {
	var apiError altmount.APIError
	if !errors.As(err, &apiError) {
		return 0, "", false
	}
	return apiError.StatusCode, apiError.Message, true
}
