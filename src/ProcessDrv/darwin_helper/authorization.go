//go:build darwin && cgo
// +build darwin,cgo

package darwin_helper

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const defaultAuthorizationPrompt = "请求管理员权限。"

var authorizationPrompt struct {
	sync.RWMutex
	text string
}

// 空字符串恢复默认文案；系统密码框的标题和外观仍由 macOS 决定。
func SetAuthorizationPrompt(prompt string) error {
	if err := validateAuthorizationPrompt(prompt); err != nil {
		return err
	}
	authorizationPrompt.Lock()
	authorizationPrompt.text = prompt
	authorizationPrompt.Unlock()
	return nil
}

func validateAuthorizationPrompt(prompt string) error {
	if len(prompt) > 1024 || !utf8.ValidString(prompt) {
		return errors.New("授权提示必须是有效的 UTF-8 文本，且不超过 1024 字节")
	}
	for _, r := range prompt {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("授权提示不能包含终端控制字符")
		}
	}
	return nil
}

func currentAuthorizationPrompt() string {
	authorizationPrompt.RLock()
	defer authorizationPrompt.RUnlock()
	if authorizationPrompt.text == "" {
		return defaultAuthorizationPrompt
	}
	return authorizationPrompt.text
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func rootLaunchCommand(exe, socket string, uid, pid int) string {
	// env 会将带等号的可执行文件路径误当作环境赋值。
	return `/usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME=/var/root LANG=C /bin/sh -c 'exec "$@"' sh ` +
		shellQuote(exe) + " --root-service --socket " + shellQuote(socket) +
		" --parent-uid " + strconv.Itoa(uid) + " --parent-pid " + strconv.Itoa(pid) +
		" </dev/null >/dev/null 2>&1 & echo $!"
}

func elevationCommand(ctx context.Context, exe, socket string, uid, pid int, gui, terminal bool, prompt string) (*exec.Cmd, error) {
	if err := validateAuthorizationPrompt(prompt); err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	switch {
	case uid == 0:
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", rootLaunchCommand(exe, socket, uid, pid))
	case gui:
		cmd = exec.CommandContext(ctx, exe, "--sunny-authorize", socket, strconv.Itoa(uid), strconv.Itoa(pid), prompt)
	case terminal:
		// sudo 会展开提示中的百分号转义，调用方文案必须保持原样。
		prompt = strings.ReplaceAll(prompt, "%", "%%") + "\n管理员密码 (%p): "
		cmd = exec.CommandContext(ctx, "/usr/bin/sudo", "-p", prompt, "--", "/bin/sh", "-c", rootLaunchCommand(exe, socket, uid, pid))
	default:
		return nil, errors.New("当前进程不是 GUI 应用，且没有可交互的前台终端；请从终端运行或使用 sudo 启动")
	}
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=en_US.UTF-8"}
	cmd.WaitDelay = time.Second
	return cmd, nil
}
