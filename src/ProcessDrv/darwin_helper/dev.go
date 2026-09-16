//go:build darwin
// +build darwin

package darwin_helper

import (
	"context"
	"encoding/base64"
	"net"
	"strconv"

	"github.com/qtgolang/SunnyNet/src/ProcessDrv/ProcessCheck"
	"github.com/qtgolang/SunnyNet/src/ProcessDrv/tun/Tun"
)

var dev = Tun.NewTun{}

type _sunny_dev struct {
	peer *rpcPeer
}

func (x *_sunny_dev) AddDevObj(connPort uint16, info ProcessCheck.DrvInfo) {
	_, _ = x.peer.Call(context.Background(), "AddDevObj",
		strconv.Itoa(int(connPort)),
		info.GetPid(),
		info.GetRemoteAddress(),
		info.GetPackageName(),
		strconv.FormatUint(info.ID(), 10),
		strconv.Itoa(int(info.GetRemotePort())),
		boolOf(info.IsV6()),
	)
}

func (x *_sunny_dev) DelDevObj(connPort uint16) {
	_, _ = x.peer.Call(context.Background(), "DelDevObj",
		strconv.Itoa(int(connPort)),
	)
}

func (x *_sunny_dev) SetOutRouterIP(RouterIP string) bool {
	a, _ := x.peer.Call(context.Background(), "SetOutRouterIP", RouterIP)
	return a == "true"
}

func (x *_sunny_dev) Port() int {
	a, _ := x.peer.Call(context.Background(), "Port")
	_aa, _ := strconv.Atoi(a)
	return _aa
}
func (x *_sunny_dev) CheckPidByName(pid int32, name string) bool {
	a, _ := x.peer.Call(context.Background(), "CheckPidByName", strconv.Itoa(int(pid)), name)
	return a == "true"
}
func (e *RootExecutor) tun(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) < 2 {
		return Response{Error: "参数错误"}
	}
	op := args[1]
	switch op {
	case "IsRun":
		return Response{Output: boolOf(dev.IsRunning)}
	case "Install":
		return Response{Output: boolOf(true)}
	case "SetHandle":
		s, _ := peer.Call(context.Background(), "pid")
		_s, _ := strconv.Atoi(s)
		Tun.SetMyPid(int32(_s))
		tcp := Tun.TcpFunc(func(net.Conn) {
			//这里不使用回调,代码中直接转向到sunny
		})
		udp := Tun.UdpFunc(func(Type int, Theoni int64, pid uint32, LocalAddress string, RemoteAddress string, data []byte, PackageName string) []byte {
			bs, _ := peer.Call(
				context.Background(), "udp",
				strconv.Itoa(Type),
				strconv.FormatInt(Theoni, 10),
				strconv.Itoa(int(pid)),
				LocalAddress,
				RemoteAddress,
				base64.StdEncoding.EncodeToString(data),
				PackageName,
			)
			bs2, _ := base64.StdEncoding.DecodeString(bs)
			return bs2
		})
		_p, _ := strconv.Atoi(args[2])
		dev.ProxyPort = uint16(_p)
		_pv := &_sunny_dev{peer: peer}
		dev.Sunny = _pv
		dev.Check = _pv
		dev.SetHandle(tcp, udp)
		return Response{Output: boolOf(true)}
	case "Run":
		if dev.IsRunning {
			return Response{Output: boolOf(true)}
		}
		return Response{Output: boolOf(dev.OnTunCreated(0))}
	case "Close":
		dev.IsRunning = false
		return Response{Output: boolOf(true)}
	case "Name":
		return Response{Output: "utun"}
	case "UnInstall":
		return Response{Output: boolOf(true)}
	case "SetFd":
		/*
			fd, _ := strconv.Atoi(args[2])
		*/
		return Response{Output: boolOf(true)}
	}
	return Response{}
}
