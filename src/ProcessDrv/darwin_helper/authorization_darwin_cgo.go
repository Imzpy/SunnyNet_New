//go:build darwin && cgo
// +build darwin,cgo

package darwin_helper

/*
#cgo LDFLAGS: -framework Foundation -framework AppKit
#include <stdlib.h>
int sunny_is_gui_application(void);
char *sunny_authorize(const char *command, const char *prompt, char **failure);
*/
import "C"

import (
	"errors"
	"runtime"
	"strings"
	"unicode/utf8"
	"unsafe"
)

const nativeAuthorizationAvailable = true

func IsGUI() bool {
	return C.sunny_is_gui_application() != 0
}

func nativeAuthorization(command, prompt string) (string, error) {
	if !utf8.ValidString(command) || strings.IndexByte(command, 0) >= 0 {
		return "", errors.New("GUI 授权不支持包含无效 UTF-8 或 NUL 的路径")
	}
	commandText := C.CString(command)
	promptText := C.CString(prompt)
	defer C.free(unsafe.Pointer(commandText))
	defer C.free(unsafe.Pointer(promptText))
	// AppleScript 只在隔离的授权子进程内、同一线程上执行。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var failure *C.char
	output := C.sunny_authorize(commandText, promptText, &failure)
	if failure != nil {
		defer C.free(unsafe.Pointer(failure))
		return "", errors.New(C.GoString(failure))
	}
	if output == nil {
		return "", errors.New("系统授权未返回结果")
	}
	defer C.free(unsafe.Pointer(output))
	return C.GoString(output), nil
}
