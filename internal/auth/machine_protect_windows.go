//go:build windows

package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const machineProtectPrefix = "dpapi:v1:"

func protectMachineData(plain []byte) (string, error) {
	if len(plain) == 0 {
		return "", fmt.Errorf("empty machine secret")
	}
	in := bytesToDataBlob(plain)
	entropyBytes := machineWrappingAAD()
	entropy := bytesToDataBlob(entropyBytes)
	var out windows.DataBlob
	if err := windows.CryptProtectData(in, nil, entropy, 0, nil, 0, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	protected := dataBlobToBytes(out)
	return machineProtectPrefix + base64.RawURLEncoding.EncodeToString(protected), nil
}

func unprotectMachineData(wrapped string) ([]byte, error) {
	if !strings.HasPrefix(wrapped, machineProtectPrefix) {
		return nil, fmt.Errorf("unsupported machine key wrapper")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(wrapped, machineProtectPrefix))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty machine secret")
	}
	in := bytesToDataBlob(raw)
	entropyBytes := machineWrappingAAD()
	entropy := bytesToDataBlob(entropyBytes)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(in, nil, entropy, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return dataBlobToBytes(out), nil
}

func bytesToDataBlob(in []byte) *windows.DataBlob {
	return &windows.DataBlob{
		Size: uint32(len(in)),
		Data: &in[0],
	}
}

func dataBlobToBytes(blob windows.DataBlob) []byte {
	if blob.Size == 0 || blob.Data == nil {
		return nil
	}
	view := unsafe.Slice(blob.Data, int(blob.Size))
	return append([]byte(nil), view...)
}
