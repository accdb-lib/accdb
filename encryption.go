package accdb

import (
	"errors"
	"fmt"
)

var (
	ErrPasswordRequired = errors.New("password required for encrypted database")
	ErrInvalidPassword  = errors.New("invalid password")
)

// SetPassword sets the database password.
// Access encryption is not yet implemented in Alpha.
func (db *Database) SetPassword(password string) error {
	return fmt.Errorf("%w: Access encryption", ErrNotImplemented)
}

// Decrypt decrypts the database with the given password.
// Access encryption is not yet implemented in Alpha.
func (db *Database) Decrypt(password string) error {
	return fmt.Errorf("%w: Access encryption", ErrNotImplemented)
}

// VerifyPassword verifies if the password is correct.
// Access encryption is not yet implemented in Alpha.
func (db *Database) VerifyPassword(password string) (bool, error) {
	return false, fmt.Errorf("%w: Access encryption", ErrNotImplemented)
}

// ChangePassword changes the database password.
// Access encryption is not yet implemented in Alpha.
func (db *Database) ChangePassword(oldPassword, newPassword string) error {
	return fmt.Errorf("%w: Access encryption", ErrNotImplemented)
}
