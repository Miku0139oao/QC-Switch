//go:build windows

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
)

func instanceLock(dir string) (func(), error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	hash := sha256.Sum256([]byte(strings.ToLower(abs)))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\QC-Switch-%x", hash[:16]))
	handle, e := windows.CreateMutex(nil, false, name)
	if errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
		return nil, errors.New("QC Switch 已在執行；請從系統匣開啟目前視窗")
	}
	if e != nil {
		return nil, e
	}
	return func() { _ = windows.CloseHandle(handle) }, nil
}
