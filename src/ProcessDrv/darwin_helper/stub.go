//go:build !darwin
// +build !darwin

package darwin_helper

func Call(args ...string) (string, error) {
	return "", nil
}

func IsGUI() bool {
	return false
}
