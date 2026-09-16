//go:build darwin
// +build darwin

package darwin_helper

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"

	"github.com/qtgolang/SunnyNet/src/ProcessDrv/ProcessCheck"
)

type MainExecutor struct {
	commandRegistry
}

// NewMainExecutor 中的表与 root 端独立，添加命令不会自动开放给另一端。
func NewMainExecutor() *MainExecutor {
	e := &MainExecutor{}
	e.whitelist = map[string]commandSpec{
		"ping":           {run: e.ping, retrySafe: true},
		"get-state":      {run: e.state, retrySafe: true},
		"id":             {run: e.id, retrySafe: true},
		"SetOutRouterIP": {run: e.setOutRouterIP, retrySafe: true},
		"Port":           {run: e.port, retrySafe: true},
		"CheckPidByName": {run: e.checkPidByName, retrySafe: true},
		"AddDevObj":      {run: e.addDevObj, retrySafe: true},
		"DelDevObj":      {run: e.delDevObj, retrySafe: true},
		"udp":            {run: e.udp, retrySafe: true},
		"pid":            {run: e.pid, retrySafe: true},
	}
	return e
}

func (e *MainExecutor) pid(ctx context.Context, peer *rpcPeer, args ...string) Response {
	return Response{Output: strconv.Itoa(os.Getpid())}
}

// id 在主进程端只执行本地命令，不再次回调，避免两端递归调用。
func (e *MainExecutor) id(ctx context.Context, peer *rpcPeer, args ...string) Response {
	return executeID(ctx, args[1:]...)
}

// state 只暴露当前主进程身份，不接受可改变查询范围的参数。
func (e *MainExecutor) state(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) != 1 {
		return Response{Error: "get-state 不接受额外参数"}
	}
	return Response{Output: fmt.Sprintf("PID=%d，EUID=%d", os.Getpid(), os.Geteuid())}
}

func (e *MainExecutor) setOutRouterIP(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) != 2 {
		return Response{Error: "参数错误"}
	}
	return Response{Output: boolOf(Sunny.SetOutRouterIP(args[1]))}
}

func (e *MainExecutor) port(ctx context.Context, peer *rpcPeer, args ...string) Response {
	return Response{Output: strconv.Itoa(Sunny.Port())}
}

func (e *MainExecutor) checkPidByName(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) != 3 {
		return Response{Error: "参数错误"}
	}
	return Response{Output: boolOf(ProcessCheck.CheckPidByName(int32(toInt(args[1])), args[2]))}
}

func (e *MainExecutor) addDevObj(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) != 8 {
		return Response{Error: "参数错误"}
	}
	var arr [7]string
	arr[0] = args[1] //connPort
	arr[1] = args[2] //pid
	arr[2] = args[3] //RemoteAddress
	arr[3] = args[4] //PackageName
	arr[4] = args[5] //ID
	arr[5] = args[6] //RemotePort
	arr[6] = args[7] //IsV6
	ProcessCheck.AddDevObj(uint16(toInt(args[1])), &connDrv{args: arr})
	return Response{Output: "true"}
}

func (e *MainExecutor) delDevObj(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) != 2 {
		return Response{Error: "参数错误"}
	}
	ProcessCheck.DelDevObj(uint16(toInt(args[1])))
	return Response{Output: "true"}
}

func (e *MainExecutor) udp(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) != 8 {
		return Response{Error: "参数错误"}
	}
	bs, _ := base64.StdEncoding.DecodeString(args[6])
	rs := UdpSendReceiveFunc(
		toInt(args[1]),
		int64(toInt(args[2])),
		uint32(toInt(args[3])),
		args[4],
		args[5],
		bs,
		args[7],
	)
	return Response{Output: base64.StdEncoding.EncodeToString(rs)}
}
