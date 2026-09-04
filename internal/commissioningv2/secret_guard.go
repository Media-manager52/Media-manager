package commissioningv2

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maximumCredentialBytes       = 4096
	maximumProviderMaterialBytes = 16 << 20
	maximumProviderMaterials     = 128
	maximumSecretDecodePasses    = 3
	maximumSecretRepresentations = 128
)

var (
	ErrInvalidSecretGuardInput = errors.New("invalid commissioning secret-guard input")
	ErrExactSecretDetected     = errors.New("commissioning response contained configured credential material")
)

type MaterialContext string

const (
	MaterialRaw        MaterialContext = "raw"
	MaterialQuery      MaterialContext = "query"
	MaterialHeader     MaterialContext = "header"
	MaterialXMLDecoded MaterialContext = "xml_decoded"
)

type ProviderMaterial struct {
	Context MaterialContext
	Value   []byte
}

// SecretWireUse declares only encodings the configured client actually uses.
// Newznab API keys use QueryParameter. BasicUsername and Bearer are available
// for focused seam tests or a later reviewed authentication mode; neither is
// inferred from provider content.
type SecretWireUse struct {
	QueryParameter bool
	PathParameter  bool
	Bearer         bool
	BasicUsername  string
}

// InspectExactSecret is pure and accepts the already-resolved credential only
// as an explicit argument. It has no resolver, logger, persistence, or network
// dependency and returns errors that never contain the credential/material.
func InspectExactSecret(credential string, use SecretWireUse, materials ...ProviderMaterial) error {
	if credential == "" || len(credential) > maximumCredentialBytes || !utf8.ValidString(credential) || strings.ContainsAny(credential, "\x00\r\n") || !validSecretWireUse(use) || len(materials) > maximumProviderMaterials {
		return ErrInvalidSecretGuardInput
	}
	representations := exactSecretRepresentations(credential, use)
	totalBytes := 0
	for _, material := range materials {
		if !validMaterialContext(material.Context) || len(material.Value) > maximumProviderMaterialBytes || totalBytes > maximumProviderMaterialBytes-len(material.Value) {
			return ErrInvalidSecretGuardInput
		}
		totalBytes += len(material.Value)
		if containsRepresentation(material.Value, representations) {
			return ErrExactSecretDetected
		}
		current := append([]byte(nil), material.Value...)
		for pass := 0; pass < maximumSecretDecodePasses; pass++ {
			decoded, changed := decodeSecretPercentTriplets(current)
			if !changed {
				break
			}
			if containsRepresentation(decoded, representations) {
				return ErrExactSecretDetected
			}
			current = decoded
		}
		if material.Context == MaterialQuery {
			current, queryChanged := decodeSecretQueryPluses(material.Value)
			if queryChanged && containsRepresentation(current, representations) {
				return ErrExactSecretDetected
			}
			for pass := 0; pass < maximumSecretDecodePasses; pass++ {
				decoded, changed := decodeSecretPercentTriplets(current)
				if !changed {
					break
				}
				current = decoded
				if containsRepresentation(current, representations) {
					return ErrExactSecretDetected
				}
			}
		}
	}
	return nil
}

// decodeSecretQueryPluses applies query-form space normalization only to
// literal plus bytes in the original query material. Percent-produced pluses
// are not normalized, although every decoded form is still conservatively
// compared with the complete configured wire-representation set.
func decodeSecretQueryPluses(value []byte) ([]byte, bool) {
	if !bytes.Contains(value, []byte{'+'}) {
		return append([]byte(nil), value...), false
	}
	result := append([]byte(nil), value...)
	for index := range result {
		if result[index] == '+' {
			result[index] = ' '
		}
	}
	return result, true
}

func exactSecretRepresentations(credential string, use SecretWireUse) [][]byte {
	seen := make(map[string]struct{}, 32)
	ordered := make([]string, 0, 32)
	add := func(value string) {
		if value == "" {
			return
		}
		if _, exists := seen[value]; !exists && len(ordered) < maximumSecretRepresentations {
			seen[value] = struct{}{}
			ordered = append(ordered, value)
		}
	}
	add(credential)
	encoded := make([]string, 0, 3)
	if use.QueryParameter {
		queryEncoded := url.QueryEscape(credential)
		encoded = append(encoded, queryEncoded)
		add(queryEncoded)
		for pass := 1; pass < maximumSecretDecodePasses; pass++ {
			queryEncoded = url.QueryEscape(queryEncoded)
			add(queryEncoded)
		}
		encoded = append(encoded, percentEncodeEveryByte([]byte(credential)))
	}
	if use.PathParameter {
		encoded = append(encoded, url.PathEscape(credential))
	}
	for _, seed := range encoded {
		current := seed
		for pass := 0; pass <= maximumSecretDecodePasses; pass++ {
			add(current)
			current = percentEncodePercentSigns(current)
		}
	}
	if use.QueryParameter {
		base := append([]string(nil), ordered...)
		for _, value := range base {
			add("apikey=" + value)
			add("api_key=" + value)
		}
	}
	if use.Bearer {
		add("Bearer " + credential)
		add("Authorization: Bearer " + credential)
	}
	if use.BasicUsername != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(use.BasicUsername + ":" + credential))
		add(encoded)
		add("Basic " + encoded)
		add("Authorization: Basic " + encoded)
	}
	result := make([][]byte, 0, len(ordered))
	for _, value := range ordered {
		result = append(result, []byte(value))
	}
	return result
}

func containsRepresentation(material []byte, representations [][]byte) bool {
	for _, representation := range representations {
		if bytes.Contains(material, representation) {
			return true
		}
	}
	return false
}

func decodeSecretPercentTriplets(value []byte) ([]byte, bool) {
	first := bytes.IndexByte(value, '%')
	if first < 0 {
		return value, false
	}
	result := make([]byte, 0, len(value))
	result = append(result, value[:first]...)
	changed := false
	for index := first; index < len(value); {
		if value[index] == '%' && index+2 < len(value) {
			high, highOK := secretHexNibble(value[index+1])
			low, lowOK := secretHexNibble(value[index+2])
			if highOK && lowOK {
				result = append(result, high<<4|low)
				index += 3
				changed = true
				continue
			}
		}
		result = append(result, value[index])
		index++
	}
	if !changed {
		return value, false
	}
	return result, true
}

func secretHexNibble(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func percentEncodeEveryByte(value []byte) string {
	const hexadecimal = "0123456789ABCDEF"
	result := make([]byte, 0, len(value)*3)
	for _, current := range value {
		result = append(result, '%', hexadecimal[current>>4], hexadecimal[current&15])
	}
	return string(result)
}

func percentEncodePercentSigns(value string) string {
	return strings.ReplaceAll(value, "%", "%25")
}

func validSecretWireUse(use SecretWireUse) bool {
	return use.BasicUsername == "" || len(use.BasicUsername) <= 256 && utf8.ValidString(use.BasicUsername) && !strings.ContainsAny(use.BasicUsername, "\x00\r\n")
}

func validMaterialContext(context MaterialContext) bool {
	switch context {
	case MaterialRaw, MaterialQuery, MaterialHeader, MaterialXMLDecoded:
		return true
	default:
		return false
	}
}
