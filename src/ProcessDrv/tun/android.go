//go:build android
// +build android

package tun

import (
	"github.com/qtgolang/SunnyNet/src/ProcessDrv/ProcessCheck"
	"github.com/qtgolang/SunnyNet/src/ProcessDrv/tun/Tun"
)

var dev = Tun.NewTun{}

func IsRun() bool {
	return dev.IsRunning
}

func Install() bool {
	return true
}

func SetHandle(Handle Tun.TcpFunc, udpSendReceiveFunc Tun.UdpFunc, sunny Tun.Interface) bool {
	dev.ProxyPort = uint16(sunny.Port())
	dev.Check = &ch{}
	dev.SetHandle(Handle, udpSendReceiveFunc)
	return true
}
func Run() bool {
	dev.IsRunning = true
	return true
}
func Close() bool {
	dev.IsRunning = false
	return true
}
func Name() string {
	return "tun"
}

func UnInstall() bool {
	return true
}
func SetFd(fd int) bool {
	go dev.OnTunCreated(fd)
	return true
}

type ch struct {
}

func (c ch) CheckPidByName(i int32, s string) bool {
	return ProcessCheck.CheckPidByName(i, s)
}

func (c ch) AddDevObj(connPort uint16, info ProcessCheck.DrvInfo) {
	ProcessCheck.AddDevObj(connPort, info)
}

func (c ch) DelDevObj(connPort uint16) {
	ProcessCheck.DelDevObj(connPort)
}
