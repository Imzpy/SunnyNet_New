//go:build darwin && !cgo
// +build darwin,!cgo

package darwin_helper

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const nativeAuthorizationAvailable = false

func IsGUI() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	macOS := filepath.Dir(exe)
	contents := filepath.Dir(macOS)
	return filepath.Base(macOS) == "MacOS" && filepath.Base(contents) == "Contents" &&
		strings.EqualFold(filepath.Ext(filepath.Dir(contents)), ".app")
}

func nativeAuthorization(command, prompt string) (string, error) {
	return "", errors.New("GUI 管理员授权需要在 macOS 上启用 CGO 构建")
}
