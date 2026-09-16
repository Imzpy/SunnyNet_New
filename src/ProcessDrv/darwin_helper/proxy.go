//go:build darwin
// +build darwin

package darwin_helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var proxyGate = make(chan struct{}, 1)

func setSystemProxy(ctx context.Context, off bool, port int) error {
	if os.Geteuid() != 0 {
		return errors.New("系统代理只能由已授权的 root 服务修改")
	}
	if !off && (port < 1 || port > 65535) {
		return errors.New("代理端口必须在 1–65535 范围内")
	}
	select {
	case proxyGate <- struct{}{}:
		defer func() { <-proxyGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	output, err := runNetworkSetup(ctx, "-listallnetworkservices")
	if err != nil {
		return err
	}
	services := networkServices(output)
	if len(services) == 0 {
		return errors.New("未找到可配置代理的网络服务")
	}
	for _, service := range services {
		for _, protocol := range []string{"web", "secureweb", "socksfirewall"} {
			args := []string{"-set" + protocol + "proxy", service, "127.0.0.1", strconv.Itoa(port)}
			if off {
				args = []string{"-set" + protocol + "proxystate", service, "off"}
			}
			if _, err := runNetworkSetup(ctx, args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func networkServices(output string) []string {
	var services []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "An asterisk (*) ") {
			continue
		}
		if service := strings.TrimPrefix(line, "*"); service != "" {
			services = append(services, service)
		}
	}
	return services
}

func runNetworkSetup(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/sbin/networksetup", args...)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=/var/root", "LC_ALL=C"}
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("networksetup %q 失败: %w: %s", args, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
