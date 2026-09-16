package Tun

import (
	n2 "net"
	"os"

	"github.com/qtgolang/SunnyNet/src/ProcessDrv/ProcessCheck"
)

type Interface interface {
	SetOutRouterIP(RouterIP string) bool
	Port() int
}

// UdpFunc PackageName 为安卓模式下的包名，其他系统下无值
type UdpFunc func(Type int, Theoni int64, pid uint32, LocalAddress string, RemoteAddress string, data []byte, PackageName string) []byte
type TcpFunc func(conn n2.Conn)

var _myPid1 = int32(os.Getpid())

var _myPid2 = int32(0)

type Check interface {
	CheckPidByName(int32, string) bool
	AddDevObj(connPort uint16, info ProcessCheck.DrvInfo)
	DelDevObj(connPort uint16)
}

func SetMyPid(i int32) {
	_myPid2 = i
}
