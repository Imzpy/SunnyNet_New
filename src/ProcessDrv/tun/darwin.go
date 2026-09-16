//go:build darwin
// +build darwin

package tun

import (
	"strconv"

	"github.com/qtgolang/SunnyNet/src/ProcessDrv/darwin_helper"
	"github.com/qtgolang/SunnyNet/src/ProcessDrv/tun/Tun"
)

func call(args ...string) string {
	var a []string
	a = append(a, "utun")
	a = append(a, args...)
	as, _ := darwin_helper.Call(a...)
	return as
}
func IsRun() bool {
	return call("IsRun") == "true"
}

func Install() bool {
	return call("Install") == "true"
}

func SetHandle(Handle Tun.TcpFunc, udpSendReceiveFunc Tun.UdpFunc, sunny Tun.Interface) bool {
	darwin_helper.Handle = Handle
	darwin_helper.UdpSendReceiveFunc = udpSendReceiveFunc
	darwin_helper.Sunny = sunny
	return call("SetHandle", strconv.Itoa(sunny.Port())) == "true"
	/*
		dev.ProxyPort = uint16(sunny.Port())
		dev.Sunny = sunny
		dev.CheckProcess = ProcessCheck.CheckPidByName
		dev.SetHandle(Handle, udpSendReceiveFunc)
		return true
	*/
}
func Run() bool {
	return call("Run") == "true"
}
func Close() bool {
	return call("Close") == "true"
}
func Name() string {
	return call("Name")
}

func UnInstall() bool {
	return call("UnInstall") == "true"
}
func SetFd(fd int) bool {
	return call("SetFd", strconv.Itoa(fd)) == "true"
}
