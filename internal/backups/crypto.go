package backups

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	chunkSize = 1 << 20
	magic     = "MMBAK001"
)

func ParseKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("backup encryption key must be a base64-encoded 32-byte value")
	}
	return key, nil
}

func Encrypt(destination io.Writer, source io.Reader, key []byte) error {
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	noncePrefix := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, noncePrefix); err != nil {
		return err
	}
	if _, err := destination.Write([]byte(magic)); err != nil {
		return err
	}
	if _, err := destination.Write(noncePrefix); err != nil {
		return err
	}
	buffer := make([]byte, chunkSize)
	for counter := uint32(0); ; counter++ {
		count, readErr := io.ReadFull(source, buffer)
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if count == 0 {
			return binary.Write(destination, binary.BigEndian, uint32(0))
		}
		if err := binary.Write(destination, binary.BigEndian, uint32(count)); err != nil {
			return err
		}
		nonce := makeNonce(noncePrefix, counter)
		if _, err := destination.Write(aead.Seal(nil, nonce, buffer[:count], nil)); err != nil {
			return err
		}
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			return binary.Write(destination, binary.BigEndian, uint32(0))
		}
	}
}

func Decrypt(destination io.Writer, source io.Reader, key []byte) error {
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	header := make([]byte, len(magic))
	if _, err := io.ReadFull(source, header); err != nil || string(header) != magic {
		return errors.New("invalid encrypted backup header")
	}
	noncePrefix := make([]byte, 8)
	if _, err := io.ReadFull(source, noncePrefix); err != nil {
		return errors.New("invalid encrypted backup nonce")
	}
	for counter := uint32(0); ; counter++ {
		var plaintextSize uint32
		if err := binary.Read(source, binary.BigEndian, &plaintextSize); err != nil {
			return errors.New("truncated encrypted backup")
		}
		if plaintextSize == 0 {
			var trailing [1]byte
			if count, err := source.Read(trailing[:]); err != io.EOF || count != 0 {
				return errors.New("encrypted backup contains trailing data")
			}
			return nil
		}
		if plaintextSize > chunkSize {
			return errors.New("invalid encrypted backup chunk size")
		}
		ciphertext := make([]byte, int(plaintextSize)+aead.Overhead())
		if _, err := io.ReadFull(source, ciphertext); err != nil {
			return errors.New("truncated encrypted backup chunk")
		}
		plaintext, err := aead.Open(nil, makeNonce(noncePrefix, counter), ciphertext, nil)
		if err != nil {
			return errors.New("encrypted backup authentication failed")
		}
		if _, err := destination.Write(plaintext); err != nil {
			return err
		}
	}
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid AES-256 key length %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func makeNonce(prefix []byte, counter uint32) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[8:], counter)
	return nonce
}
