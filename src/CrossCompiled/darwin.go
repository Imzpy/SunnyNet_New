//go:build darwin
// +build darwin

package CrossCompiled

import (
	"strconv"

	"github.com/qtgolang/SunnyNet/src/ProcessDrv/darwin_helper"
)

func SetIeProxy(Off bool, Port int) bool {
	var rs string
	if Off {
		rs, _ = darwin_helper.Call("setIE", "off", strconv.Itoa(Port))
	} else {
		rs, _ = darwin_helper.Call("setIE", "on", strconv.Itoa(Port))
	}
	return rs == "true"
}

func SetNetworkConnectNumber() {
}

// CloseCurrentSocket  关闭指定进程的所有TCP连接
func CloseCurrentSocket(PID int, ulAf uint) {
}

// InstallCert 安装证书 将证书安装到Windows系统内
func InstallCert(certificates []byte) string {
	return "no Windows"
}

// 添加 Windows 防火墙规则
func AddFirewallRule() {

}

func (N NFAPI) UnInstall() bool {
	return false
}

func (N NFAPI) Install() bool {
	return false
}

func (N NFAPI) IsRun() bool {
	return false
}

func (N NFAPI) SetHandle() bool {
	return false
}

func (N NFAPI) Run() bool {
	return false
}

func (N NFAPI) Close() bool {
	return false
}

func (N NFAPI) Name() string {
	return "NFAPI"
}

func (p Pr) Install() bool {
	return false
}

func (p Pr) IsRun() bool {
	return false
}

func (p Pr) SetHandle() bool {
	return false
}

func (p Pr) Run() bool {
	return false
}

func (p Pr) Close() bool {
	return false
}

func (p Pr) Name() string {
	return "Proxifier"
}

func (p Pr) UnInstall() bool {
	return false
}
