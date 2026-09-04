package commissioningv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrInvalidCommissioningDocument = errors.New("invalid Newznab commissioning document")

// SHA256Digest carries an already non-secret digest. The type never accepts or
// resolves an endpoint, account reference, or credential.
type SHA256Digest [sha256.Size]byte

func SumSHA256(value []byte) SHA256Digest { return sha256.Sum256(value) }

func SHA256DigestFromBytes(value []byte) (SHA256Digest, error) {
	var result SHA256Digest
	if len(value) != sha256.Size {
		return result, ErrInvalidCommissioningDocument
	}
	copy(result[:], value)
	return result, nil
}

func ParseSHA256Digest(value string) (SHA256Digest, error) {
	var result SHA256Digest
	if len(value) != sha256.Size*2 {
		return result, ErrInvalidCommissioningDocument
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return result, ErrInvalidCommissioningDocument
	}
	copy(result[:], decoded)
	return result, nil
}

func (digest SHA256Digest) String() string { return hex.EncodeToString(digest[:]) }

func (digest SHA256Digest) IsZero() bool {
	return digest == SHA256Digest{}
}

func (digest SHA256Digest) MarshalJSON() ([]byte, error) {
	if digest.IsZero() {
		return nil, ErrInvalidCommissioningDocument
	}
	return json.Marshal(digest.String())
}

func (digest *SHA256Digest) UnmarshalJSON(document []byte) error {
	var encoded string
	if err := json.Unmarshal(document, &encoded); err != nil {
		return ErrInvalidCommissioningDocument
	}
	parsed, err := ParseSHA256Digest(encoded)
	if err != nil {
		return err
	}
	*digest = parsed
	return nil
}

func (digest SHA256Digest) Bytes() []byte {
	result := make([]byte, sha256.Size)
	copy(result, digest[:])
	return result
}
