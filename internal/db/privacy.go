package db

import "strings"

// PrivacyCodec protects user-scoped private text fields at rest.
// Implementations must preserve plaintext compatibility for legacy values.
type PrivacyCodec interface {
	ProtectStringForStorage(userID, purpose, value string) (string, error)
	OpenStringFromStorage(userID, purpose, value string) (string, error)
}

func (d *DB) protectPrivateString(userID, purpose, value string) (string, error) {
	if d == nil || d.privacy == nil || strings.TrimSpace(value) == "" {
		return value, nil
	}
	return d.privacy.ProtectStringForStorage(normalizeUserID(userID), purpose, value)
}

func (d *DB) openPrivateString(userID, purpose, value string) (string, error) {
	if d == nil || d.privacy == nil || strings.TrimSpace(value) == "" {
		return value, nil
	}
	return d.privacy.OpenStringFromStorage(normalizeUserID(userID), purpose, value)
}

func (d *DB) warmPrivateString(userID, purpose string) error {
	if d == nil || d.privacy == nil {
		return nil
	}
	_, err := d.privacy.ProtectStringForStorage(normalizeUserID(userID), purpose, "warmup")
	return err
}
