//go:build darwin
// +build darwin

package darwin_helper

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

type RootExecutor struct {
	commandRegistry
}

// NewRootExecutor 中的表只管理 root 端权限，启动后应保持只读。
func NewRootExecutor() *RootExecutor {
	e := &RootExecutor{}
	e.whitelist = map[string]commandSpec{
		"ping":  {run: e.ping, retrySafe: true},
		"id":    {run: e.id, retrySafe: true},
		"setIE": {run: e.setIE, retrySafe: true},
		"utun":  {run: e.tun, retrySafe: true},
	}
	return e
}

func (e *RootExecutor) id(ctx context.Context, peer *rpcPeer, args ...string) Response {
	local := executeID(ctx, args[1:]...)
	if local.Error != "" || peer == nil {
		return local
	}
	mainOutput, err := peer.Call(ctx, args...)
	if err != nil {
		return Response{Error: fmt.Sprintf("调用主进程 id 失败: %v", err)}
	}
	return Response{Output: fmt.Sprintf("root 服务 id:\n%s主进程 id:\n%s", local.Output, mainOutput)}
}

func boolOf(a bool) string {
	if a {
		return "true"
	}
	return "false"
}
func (e *RootExecutor) setIE(ctx context.Context, peer *rpcPeer, args ...string) Response {
	off, port, err := proxySettings(args)
	if err != nil {
		return Response{Error: err.Error()}
	}
	if err := setSystemProxy(ctx, off, port); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{Output: "true"}
}

func proxySettings(args []string) (bool, int, error) {
	if len(args) < 2 || len(args) > 3 || (args[1] != "on" && args[1] != "off") {
		return false, 0, errors.New("setIE 参数必须为 on <端口> 或 off [端口]")
	}
	off := args[1] == "off"
	port := 0
	if len(args) == 3 {
		var err error
		port, err = strconv.Atoi(args[2])
		if err != nil || port < 0 || port > 65535 {
			return false, 0, errors.New("代理端口必须是 0–65535 范围内的整数")
		}
	}
	if !off && port == 0 {
		return false, 0, errors.New("启用代理时端口必须在 1–65535 范围内")
	}
	return off, port, nil
}

type rootInitialization struct {
	done   chan struct{}
	client *rootClient
	err    error
}
type rootClientCache struct {
	mu       sync.Mutex
	client   *rootClient
	starting *rootInitialization
}

func (c *rootClientCache) get(launch rootLauncher) (*rootClient, error) {
	c.mu.Lock()
	if c.client != nil {
		client := c.client
		c.mu.Unlock()
		return client, nil
	}
	flight := c.starting
	leader := flight == nil
	if leader {
		flight = &rootInitialization{done: make(chan struct{})}
		c.starting = flight
	}
	c.mu.Unlock()
	if leader {
		client, _, err := newRootClient(context.Background(), launch)
		c.mu.Lock()
		if err == nil {
			c.client = client
		}
		flight.client, flight.err = client, err
		close(flight.done)
		c.starting = nil
		c.mu.Unlock()
	}
	<-flight.done
	return flight.client, flight.err
}

var root rootClientCache

func Call(args ...string) (string, error) {
	if len(args) == 0 || args[0] == "" {
		return "", errors.New("参数不能为空")
	}
	if _, allowed := NewRootExecutor().whitelist[args[0]]; !allowed {
		return "", errors.New("不允许的操作")
	}
	client, err := root.get(launchRootService)
	if err != nil {
		return "", err
	}
	return client.Call(context.Background(), args...)
}
