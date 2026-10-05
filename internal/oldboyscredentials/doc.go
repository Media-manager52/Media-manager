// Package oldboyscredentials encrypts the owner's OldBoys API Token, PID, and
// RID separately from integration configuration, exposes status without secret
// values, and supports decryption only for server-side clients. It performs no
// provider requests and never enables an integration.
package oldboyscredentials
