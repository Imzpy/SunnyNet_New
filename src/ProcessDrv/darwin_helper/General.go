package darwin_helper

import (
	"strconv"

	"github.com/qtgolang/SunnyNet/src/ProcessDrv/tun/Tun"
)

var Handle Tun.TcpFunc
var UdpSendReceiveFunc Tun.UdpFunc
var Sunny Tun.Interface

func toInt(a string) int {
	_p, _ := strconv.Atoi(a)
	return _p
}

type connDrv struct {
	args [7]string
}

func (c connDrv) GetRemoteAddress() string {
	return c.args[1]
}

func (c connDrv) GetRemotePort() uint16 {
	return uint16(toInt(c.args[5]))
}

func (c connDrv) GetPid() string {
	return c.args[1]
}

func (c connDrv) GetPackageName() string {
	return c.args[3]
}

func (c connDrv) IsV6() bool {
	return c.args[6] == "true"
}

func (c connDrv) ID() uint64 {
	in, _ := strconv.ParseInt(c.args[4], 10, 64)
	return uint64(in)
}

type Addr struct {
	Addr string
	Net  string
}
type Data struct {
	Length int
	Data   string
}
