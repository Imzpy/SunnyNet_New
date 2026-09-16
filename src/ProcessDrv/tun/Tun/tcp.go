//go:build android || darwin || linux
// +build android darwin linux

package Tun

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"

	"github.com/google/gopacket/layers"
)

// ------------------------------------------------
// IPv4 TCP 处理函数
// ------------------------------------------------
func (n *NewTun) handleTCPCommand(tcp *layers.TCP, srcIP, dstIP net.IP, clientPort, serverPort uint16, v4 bool) {
	if tcp.SYN && !tcp.ACK {
		pid, name := getPidByPort("tcp", uint16(tcp.SrcPort))
		s := NewDevConn(n.tun, srcIP, clientPort, dstIP, serverPort, v4, 6, tcp.Seq, n.Check)
		s.pid = uint32(pid)
		sessionsMu.Lock()
		sessions[clientPort] = s
		sessionsMu.Unlock()
		// send SYN/ACK to client
		if _, err := SendSynAckToClient(n.tun, s, tcp.Seq); err != nil {
			// 如果发送失败，删除会话
			sessionsMu.Lock()
			delete(sessions, clientPort)
			sessionsMu.Unlock()
			return
		}
		sessionsMu.Lock()
		call := n.handleTCPCallback
		sessionsMu.Unlock()
		_wait := func(c net.Conn, e error) {
			if e != nil {
				_ = s.Close()
				return
			}
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				_, _ = io.Copy(s, c)
				_ = s.Close()
				_ = c.Close()
				wg.Done()
			}()
			go func() {
				_, _ = io.Copy(c, s)
				_ = s.Close()
				_ = c.Close()
				wg.Done()
			}()
			wg.Wait()
		}
		if (s.packageName != "" && !s.packageRegistered) || n.pidFromCheck(pid, name) {
			go func() {
				var loader *net.TCPAddr
				if defaultGatewayIP != "" {
					loader = &net.TCPAddr{
						IP:   net.ParseIP(defaultGatewayIP),
						Port: 0,
					}
				}
				dialer := &net.Dialer{LocalAddr: loader}
				c, e := dialer.Dial("tcp", net.JoinHostPort(dstIP.String(), strconv.Itoa(int(serverPort))))
				_wait(c, e)
			}()
			return
		}
		if call != nil {
			if runtime.GOOS == "darwin" {
				go func() {
					im := 0
					_ip := fmt.Sprintf("127.0.0.1:%d", n.Sunny.Port())
					c, e := dialWithRandomLocalPort(
						defaultGatewayIP,
						_ip,
						func(i int) {
							im = i
							n.AddDevObj(uint16(i), s)
						}, func(i int) {
							n.DelDevObj(uint16(i))
						})
					_wait(c, e)
					n.DelDevObj(uint16(im))
				}()
			} else {
				n.AddDevObj(clientPort, s)
				go call(s)
			}
		}
		return
	}
	sessionsMu.Lock()
	sess, ok := sessions[clientPort]
	sessionsMu.Unlock()
	if !ok {
		h2 := NewDevConn(n.tun, srcIP, clientPort, dstIP, serverPort, v4, 6, tcp.Seq, n.Check)
		_ = SendRstToClient(h2)
		return
	}
	if tcp.FIN || tcp.RST {
		if tcp.FIN {
			_ = SendFinToClient(sess)
		} else if tcp.RST {
		}
		_ = sess.Close()
		return
	}
	sess.PushClientPayload(tcp.Payload, tcp.Seq)
	return
}

func (n *NewTun) handleTCP4(ip *layers.IPv4, tcp *layers.TCP) {
	clientIP := ip.SrcIP
	clientPort := uint16(tcp.SrcPort)
	serverIP := ip.DstIP
	serverPort := uint16(tcp.DstPort)
	n.handleTCPCommand(tcp, clientIP, serverIP, clientPort, serverPort, true)
}

func dialWithRandomLocalPort(localIP, remoteAddr string, addInfo, delInfo func(int)) (net.Conn, error) {
	ip := net.ParseIP(localIP)
	for i := 0; i < 500; i++ {
		// 随机选择一个动态端口
		port := 49152 + rand.Intn(65535-49152+1)

		dialer := &net.Dialer{
			LocalAddr: &net.TCPAddr{
				IP:   ip,
				Port: port,
			},
		}
		addInfo(port)
		conn, err := dialer.Dial("tcp", remoteAddr)
		if err == nil {
			return conn, nil
		}
		delInfo(port)
		// 本地端口已被占用，换一个继续
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			var sysErr *os.SyscallError
			if errors.As(opErr.Err, &sysErr) && errors.Is(sysErr.Err, syscall.EADDRINUSE) {
				continue
			}
		}

		return nil, err
	}

	return nil, fmt.Errorf("无法找到可用的本地端口")
}

// ------------------------------------------------
// IPv6 TCP 处理函数
// ------------------------------------------------
func (n *NewTun) handleTCP6(ip *layers.IPv6, tcp *layers.TCP) {
	clientIP := ip.SrcIP
	clientPort := uint16(tcp.SrcPort)
	serverIP := ip.DstIP
	serverPort := uint16(tcp.DstPort)
	n.handleTCPCommand(tcp, clientIP, serverIP, clientPort, serverPort, false)
}
