//go:build !windows

package auth

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const machineProtectPrefix = "localkey:v1:"

func protectMachineData(plain []byte) (string, error) {
	key, err := loadLocalMachineKey()
	if err != nil {
		return "", err
	}
	sealed, err := sealRaw(key, plain, machineWrappingAAD())
	if err != nil {
		return "", err
	}
	return machineProtectPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func unprotectMachineData(wrapped string) ([]byte, error) {
	if !strings.HasPrefix(wrapped, machineProtectPrefix) {
		return nil, fmt.Errorf("unsupported machine key wrapper")
	}
	key, err := loadLocalMachineKey()
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(wrapped, machineProtectPrefix))
	if err != nil {
		return nil, err
	}
	return openRaw(key, raw, machineWrappingAAD())
}

func loadLocalMachineKey() ([]byte, error) {
	path, err := localMachineKeyPath()
	if err != nil {
		return nil, err
	}
	if raw, readErr := os.ReadFile(path); readErr == nil {
		key, decErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decErr == nil && len(key) == 32 {
			return key, nil
		}
	}
	key, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.RawURLEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func localMachineKeyPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	dirName := "EveFlipper"
	if runtime.GOOS == "darwin" {
		dirName = "EVE Flipper"
	}
	return filepath.Join(base, dirName, "vault_machine.key"), nil
}
