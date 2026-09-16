//go:build darwin
// +build darwin

package darwin_helper

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qtgolang/SunnyNet/src/public"
	"golang.org/x/sys/unix"
)

// 内部子进程必须在宿主 GUI 初始化前结束，避免重新创建主窗口。
func init() {
	if len(os.Args) < 2 {
		return
	}
	var err error
	switch os.Args[1] {
	case "--root-service":
		err = runRootService(context.Background(), os.Args[2:])
	case "--sunny-authorize":
		err = runAuthorization(os.Args[2:])
	default:
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

const prefix = "[*SunnyNet*]"
const suffix = "[+SunnyNet+]"

func runAuthorization(args []string) error {
	if len(args) != 4 || !filepath.IsAbs(args[0]) {
		return errors.New("无效的 GUI 授权参数")
	}
	uid, uidErr := strconv.Atoi(args[1])
	pid, pidErr := strconv.Atoi(args[2])
	if uidErr != nil || pidErr != nil || uid == 0 || uid != os.Geteuid() || pid <= 0 || pid != os.Getppid() {
		return errors.New("GUI 授权子进程身份不匹配")
	}
	if err := validateAuthorizationPrompt(args[3]); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	output, err := nativeAuthorization(rootLaunchCommand(exe, args[0], uid, pid), args[3])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, prefix+string(output)+suffix)
	return err
}

type rootLauncher func(context.Context, context.Context) (*rpcPeer, int, error)

type rootSession struct {
	peer    *rpcPeer
	pid     int
	restart *rootRestart
}

type rootRestart struct {
	done    chan struct{}
	session *rootSession
	err     error
}

type rootClient struct {
	ctx          context.Context
	cancel       context.CancelFunc
	launch       rootLauncher
	rootCommands *RootExecutor
	mu           sync.Mutex
	session      *rootSession
}

// startRootService 使用真实的系统授权启动器创建可自动恢复的 root 客户端。
// 返回客户端及首次服务 PID；后续发生重启时，应通过客户端的 PID 方法读取当前值。
func startRootService(ctx context.Context) (*rootClient, int, error) {
	return newRootClient(ctx, launchRootService)
}

// newRootClient 建立独立的客户端生命周期，并通过 launch 完成首次服务启动。
// launch 同时接收启动上下文和长期生命周期上下文，测试可注入普通权限启动器替代真实授权。
// 初始化失败时撤销上下文并关闭已产生的连接，成功后由客户端 Close 统一管理释放。
func newRootClient(ctx context.Context, launch rootLauncher) (*rootClient, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, 0, err
	}
	peer, pid, err := launch(ctx, ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cancel()
		if peer != nil {
			peer.Close()
		}
		return nil, 0, err
	}
	client := &rootClient{
		ctx: ctx, cancel: cancel, launch: launch, rootCommands: NewRootExecutor(),
		session: &rootSession{peer: peer, pid: pid},
	}
	return client, pid, nil
}

// PID 在锁内返回当前服务会话对应的进程号，自动恢复成功后会读取到新 PID。
// 该值是最近一次建连的结果，不单独证明进程仍然存活；可用性由 Call 的通信结果判断。
func (c *rootClient) PID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session.pid
}

// Close 撤销客户端的长期上下文并关闭当前连接，阻止之后继续自动恢复服务。
// 可以重复调用；它不等待远端进程退出，服务端会在检测到断连后自行清理。
func (c *rootClient) Close() error {
	c.cancel()
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	return session.peer.Close()
}

// Call 在普通 RPC 调用外增加连接恢复：调用前发现失效先重连，调用中断连则按安全规则处理。
// 业务错误、调用取消和主动关闭不会触发重启；同一旧会话的并发调用共用恢复结果。
// 是否允许重试由 RootExecutor 白名单决定，通信层不再硬编码命令名。
// 调用者的 ctx 只约束本次请求与它发起的启动等待，不会在返回后关闭已经恢复的长期连接。
func (c *rootClient) Call(ctx context.Context, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := c.ctx.Err(); err != nil {
		return "", err
	}
	if len(args) == 0 || args[0] == "" {
		return "", errors.New("Args[0] 不能为空")
	}
	args = append([]string(nil), args...)
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session.peer.connectionError() != nil {
		recovered, err := c.recover(ctx, session)
		if err != nil {
			return "", fmt.Errorf("重启 root 服务失败: %w", err)
		}
		return recovered.peer.Call(ctx, args...)
	}

	output, err := session.peer.Call(ctx, args...)
	var remoteErr rpcRemoteError
	if err == nil || errors.As(err, &remoteErr) || ctx.Err() != nil || c.ctx.Err() != nil {
		return output, err
	}
	if session.peer.connectionError() == nil {
		return output, err
	}
	recovered, restartErr := c.recover(ctx, session)
	if restartErr != nil {
		return "", fmt.Errorf("root 服务连接中断 (%v)，重启失败: %w", err, restartErr)
	}
	// 断连时原操作可能已执行，只按白名单显式声明的安全策略重试一次。
	if !c.rootCommands.CanRetry(args...) {
		return "", fmt.Errorf("root 服务已重启，但操作 %q 的结果无法确认，未自动重试: %w", args[0], err)
	}
	return recovered.peer.Call(ctx, args...)
}

// recover 为 failed 会话合并一次服务重启尝试，与 Go 的 panic 恢复函数不是同一概念。
// 只有首个调用实际启动服务，其余调用等待同一个完成通知，避免并发重复弹出授权窗口。
// 启动阶段受调用上下文和客户端关闭共同约束，成功连接则绑定客户端的长期上下文。
// 旧会话保留成功或失败结果；失败后的新调用可以再试，关闭后迟到的连接会被丢弃。
func (c *rootClient) recover(ctx context.Context, failed *rootSession) (*rootSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if err := c.ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	flight := failed.restart
	leader := flight == nil
	if leader {
		flight = &rootRestart{done: make(chan struct{})}
		failed.restart = flight
	}
	c.mu.Unlock()

	if leader {
		failed.peer.Close()
		startupCtx, cancel := context.WithCancel(ctx)
		stop := afterContext(c.ctx, cancel)
		peer, pid, err := c.launch(startupCtx, c.ctx)
		if err == nil {
			err = startupCtx.Err()
		}
		stop()
		cancel()

		c.mu.Lock()
		if err == nil {
			err = c.ctx.Err()
		}
		if err == nil {
			flight.session = &rootSession{peer: peer, pid: pid}
			c.session = flight.session
		} else {
			if peer != nil {
				peer.Close()
			}
			// 同一旧连接的调用共享失败结果，后续新调用才可再次申请启动。
			c.session = &rootSession{peer: failed.peer, pid: failed.pid}
		}
		flight.err = err
		close(flight.done)
		c.mu.Unlock()
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	case <-flight.done:
		return flight.session, flight.err
	}
}

// launchRootService 创建私有 Unix Socket，并请求系统授权以后台方式运行当前程序的服务模式。
// ctx 控制授权和建连阶段，lifetimeCtx 只用于成功后的长期 RPC，二者不能混用。
// 连接必须同时通过 root UID 和启动返回 PID 的校验；授权失败、超时或启动异常均返回错误。
// 临时监听路径在建连后清理，不安装持久系统服务，也不读取或保存管理员密码。
func launchRootService(ctx, lifetimeCtx context.Context) (*rpcPeer, int, error) {
	exe, err := os.Executable()
	if err != nil {
		exe, err = exec.LookPath(os.Args[0])
		if err != nil {
			return nil, 0, fmt.Errorf("定位当前程序: %w", err)
		}
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return nil, 0, err
	}

	// 反向连接避免 root 服务在用户可写目录中创建文件。
	dir, err := os.MkdirTemp("/tmp", "sunnynet-auth-")
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(dir)
	socket := filepath.Join(dir, "rpc.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, 0, err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		return nil, 0, err
	}

	startupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := afterContext(startupCtx, func() { listener.Close() })
	defer stop()
	gui := os.Geteuid() != 0 && IsGUI()
	if gui && !nativeAuthorizationAvailable {
		return nil, 0, errors.New("GUI 管理员授权需要在 macOS 上启用 CGO 构建")
	}
	var terminal *os.File
	if os.Geteuid() != 0 && !gui {
		terminal, _ = os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if terminal != nil {
			foreground, err := unix.IoctlGetInt(int(terminal.Fd()), unix.TIOCGPGRP)
			if err != nil || foreground != unix.Getpgrp() {
				terminal.Close()
				terminal = nil
			} else {
				defer terminal.Close()
			}
		}
	}
	cmd, err := elevationCommand(startupCtx, exe, socket, os.Geteuid(), os.Getpid(), gui, terminal != nil, currentAuthorizationPrompt())
	if err != nil {
		return nil, 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if terminal != nil {
		state, err := unix.IoctlGetTermios(int(terminal.Fd()), unix.TIOCGETA)
		if err != nil {
			return nil, 0, fmt.Errorf("读取授权终端状态: %w", err)
		}
		defer unix.IoctlSetTermios(int(terminal.Fd()), unix.TIOCSETA, state)
		cmd.Stdin = terminal
		cmd.Stderr = terminal
		cmd.Cancel = func() error { return cmd.Process.Signal(unix.SIGTERM) }
	}
	output, err := cmd.Output()
	if startupCtx.Err() != nil {
		return nil, 0, startupCtx.Err()
	}
	if err != nil {
		return nil, 0, fmt.Errorf("管理员授权失败或已取消: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(public.SubString(string(output), prefix, suffix)))
	if err != nil || pid <= 0 {
		return nil, 0, errors.New("系统未返回有效的服务 PID")
	}
	if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, 0, err
	}
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if startupCtx.Err() != nil {
				return nil, 0, startupCtx.Err()
			}
			return nil, 0, fmt.Errorf("等待 root 服务连接失败: %w", err)
		}
		if err := verifyPeer(conn, 0, pid); err != nil {
			conn.Close()
			continue
		}
		executor := NewMainExecutor()
		return newRPCPeer(lifetimeCtx, conn, executor.Execute), pid, nil
	}
}

// runRootService 处理内部服务启动参数，要求当前进程已为 root，并校验 Socket 路径及主进程标识。
// 它主动连接主进程，验证对端实际 UID 和 PID 后才进入双向服务，避免接受伪造调用者。
// 连接失败、身份不符或参数无效时直接返回错误，不会再次申请提权或递归启动服务。
func runRootService(ctx context.Context, args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("--root-service 只能由 root 运行，请直接启动程序以请求系统授权")
	}
	flags := flag.NewFlagSet("root-service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "")
	parentUID := flags.Int("parent-uid", -1, "")
	parentPID := flags.Int("parent-pid", 0, "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*socket) || *parentUID < 0 ||
		uint64(*parentUID) > uint64(^uint32(0)) || *parentPID <= 0 {
		return errors.New("无效的 root 服务启动参数")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", *socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := verifyPeer(conn.(*net.UnixConn), *parentUID, *parentPID); err != nil {
		return fmt.Errorf("主进程身份校验失败: %w", err)
	}
	return serve(ctx, conn)
}

// verifyPeer 从 Unix Socket 的内核凭据读取对端 UID 与 PID，并与期望身份同时比较。
// 校验不依赖 RPC 消息中自报的身份，主进程和服务端都必须在处理业务前调用。
// 返回系统调用错误或身份不匹配错误；不会修改对端进程或连接内容。
func verifyPeer(conn *net.UnixConn, wantUID, wantPID int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		credentials, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			peerErr = err
			return
		}
		pid, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if err != nil {
			peerErr = err
			return
		}
		if uint64(credentials.Uid) != uint64(wantUID) || pid != wantPID {
			peerErr = fmt.Errorf("拒绝 IPC 对端 UID=%d PID=%d", credentials.Uid, pid)
		}
	})
	if err != nil {
		return err
	}
	return peerErr
}
