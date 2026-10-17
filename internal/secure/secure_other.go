//go:build !windows

package secure

import "errors"

func Protect(b []byte) ([]byte, error) {
	return nil, errors.New("credential storage requires Windows DPAPI")
}
func Unprotect(b []byte) ([]byte, error) {
	return nil, errors.New("credential storage requires Windows DPAPI")
}
