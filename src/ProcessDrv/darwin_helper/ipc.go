//go:build darwin
// +build darwin

package darwin_helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxFrameSize = 4096000
	maxInFlight  = 32
	rpcTimeout   = 10 * time.Second
)

type rpcMessage struct {
	Type   string   `json:"type"`
	ID     uint64   `json:"id"`
	Args   []string `json:"args,omitempty"`
	Output string   `json:"output,omitempty"`
	Error  string   `json:"error,omitempty"`
}

type Response struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

type rpcRemoteError string

// Error 返回对端业务错误的原始说明，供调用方展示或记录。
// 错误仍保留 rpcRemoteError 类型，使自动恢复逻辑不会把业务失败当作连接断开。
func (e rpcRemoteError) Error() string {
	return string(e)
}

type rpcHandler func(context.Context, *rpcPeer, ...string) Response

type rpcPeer struct {
	conn      net.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	handler   rpcHandler
	writeGate chan struct{}
	done      chan struct{}
	readDone  chan struct{}
	closeOnce sync.Once
	handlers  sync.WaitGroup
	mu        sync.Mutex
	nextID    uint64
	pending   map[uint64]chan Response
	incoming  map[uint64]struct{}
	err       error
}

// afterContext 在 Go 1.20 中提供本项目所需的取消回调，替代新版 context.AfterFunc。
// ctx 结束后异步执行 fn；若注册时已经结束，立即安排回调且停止函数返回 false。
// 返回的停止函数仅在成功阻止回调时返回 true，不取消 ctx，也不等待已开始的回调。
// 原子竞争保证停止和触发只有一方成功；调用方应在不再监听时调用停止函数释放等待者。
func afterContext(ctx context.Context, fn func()) func() bool {
	var claimed atomic.Bool
	stopped := make(chan struct{})
	if ctx.Err() != nil {
		claimed.Store(true)
		go fn()
	} else if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				if claimed.CompareAndSwap(false, true) {
					fn()
				}
			case <-stopped:
			}
		}()
	}
	return func() bool {
		if !claimed.CompareAndSwap(false, true) {
			return false
		}
		close(stopped)
		return true
	}
}

// newRPCPeer 创建与 ctx 生命周期绑定的双向 RPC 节点，并接管 conn 的读写与关闭。
// 独立接收循环分发响应和请求；handler 可以并发执行，也可以通过 peer 反向调用。
// 调用前必须完成对端身份校验；使用结束后调用 Close，需要等待处理任务退出时再调用 Wait。
func newRPCPeer(ctx context.Context, conn net.Conn, handler rpcHandler) *rpcPeer {
	ctx, cancel := context.WithCancel(ctx)
	p := &rpcPeer{
		conn: conn, ctx: ctx, cancel: cancel, handler: handler,
		writeGate: make(chan struct{}, 1), done: make(chan struct{}),
		readDone: make(chan struct{}), pending: make(map[uint64]chan Response),
		incoming: make(map[uint64]struct{}),
	}
	go func() {
		defer close(p.readDone)
		stop := afterContext(ctx, func() { p.shutdown(ctx.Err()) })
		defer stop()
		p.shutdown(p.readLoop())
	}()
	return p
}

// shutdown 记录连接的首次终止原因，并一次性取消节点、关闭连接和广播退出通知。
// pending 与 incoming 在锁内清空，但不关闭单个响应通道，避免和正在分发的响应发生竞争。
// 所有等待调用通过 done 结束；可从读循环、写入失败或取消回调中重复调用。
func (p *rpcPeer) shutdown(err error) {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.err = err
		for id := range p.pending {
			delete(p.pending, id)
		}
		for id := range p.incoming {
			delete(p.incoming, id)
		}
		p.mu.Unlock()
		p.cancel()
		p.conn.Close()
		close(p.done)
	})
}

// Close 主动结束本地 RPC 节点，并将尚未记录的终止原因设为连接已关闭。
// 本方法可重复调用，不等待正在执行的 handler；需要完整等待时再调用 Wait。
func (p *rpcPeer) Close() error {
	p.shutdown(net.ErrClosed)
	return nil
}

// connectionError 在互斥锁保护下读取节点的首次关闭原因。
// 返回 nil 表示尚未记录连接终止，不代表对端业务一定成功或进程状态已经单独探测过。
func (p *rpcPeer) connectionError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Wait 先等待接收循环退出，再等待所有已登记的请求处理任务完成。
// 此顺序避免工作组仍在增加任务时就开始等待；handler 必须响应自身上下文取消。
// 正常 EOF、主动关闭和取消视为正常退出，协议错误或其他通信故障则原样返回。
func (p *rpcPeer) Wait() error {
	<-p.readDone
	p.handlers.Wait()
	err := p.connectionError()
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Call 发送完整 Args，首项为命令名，其余为参数，并按请求 ID 等待结果。
// 单次等待受 ctx 和 rpcTimeout 共同限制；成功发送后取消本地等待不会撤回远端操作。
// 未应答请求继续占用额度，直到收到响应或连接关闭，避免超时重发压垮对端。
// 已收到的响应优先于后续断连，业务失败返回 rpcRemoteError，防止恢复层误判并重复执行。
func (p *rpcPeer) Call(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(args) == 0 || args[0] == "" {
		return "", errors.New("Args[0] 不能为空")
	}
	args = append([]string(nil), args...)
	p.mu.Lock()
	if p.err != nil {
		err := p.err
		p.mu.Unlock()
		return "", err
	}
	if len(p.pending) >= maxInFlight {
		p.mu.Unlock()
		return "", errors.New("待响应的调用过多")
	}
	p.nextID++
	id := p.nextID
	result := make(chan Response, 1)
	p.pending[id] = result
	p.mu.Unlock()
	sent := false
	defer func() {
		if !sent {
			p.mu.Lock()
			delete(p.pending, id)
			p.mu.Unlock()
		}
	}()
	if err := p.send(ctx, rpcMessage{Type: "request", ID: id, Args: args}); err != nil {
		return "", err
	}
	// 已发送请求在远端完成前仍占用额度，取消本地等待不能提前释放。
	sent = true
	var reply Response
	select {
	case reply = <-result:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.done:
		// 已收到的响应优先于随后发生的断连，避免误重启和重放。
		select {
		case reply = <-result:
		default:
			return "", p.connectionError()
		}
	}
	if reply.Error != "" {
		return "", rpcRemoteError(reply.Error)
	}
	return reply.Output, nil
}

// send 将一条消息编码为带换行终止符的 JSON 帧，并在长度限制内串行写入连接。
// 写锁等待和写入均受上下文限制，且只设置写超时，不干扰另一方向的接收循环。
// 响应真正写出前释放入站名额，允许对端收到响应后立即补发请求。
// 写入中断或短写可能留下半帧，因此必须关闭连接，不能继续复用字节流。
func (p *rpcPeer) send(ctx context.Context, message rpcMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(data)+1 > maxFrameSize {
		return errors.New("RPC 消息超过长度限制")
	}
	data = append(data, '\n')
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	select {
	case p.writeGate <- struct{}{}:
		defer func() { <-p.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return p.connectionError()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 写入中断可能留下半帧，必须关闭整个连接。
	stop := afterContext(ctx, func() { p.shutdown(ctx.Err()) })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := p.conn.SetWriteDeadline(deadline); err != nil {
		p.shutdown(err)
		return err
	}
	if message.Type == "response" {
		// 对端看到响应后可能立即补发请求，必须先释放入站名额。
		p.mu.Lock()
		delete(p.incoming, message.ID)
		p.mu.Unlock()
	}
	n, err := p.conn.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if !stop() {
		err = ctx.Err()
	}
	if err != nil {
		p.shutdown(err)
	}
	return err
}

// readLoop 是节点唯一的收包入口，校验完整消息、字段、请求 ID 和并发上限。
// 响应按 ID 投递给等待者；已结束等待的迟到响应只释放额度，不会交给其他调用。
// 请求交由独立任务处理，避免处理函数反向调用时阻塞收包而形成死锁。
// 遇到 EOF、超时或协议错误时返回原因，由创建节点的 goroutine 统一执行关闭。
func (p *rpcPeer) readLoop() error {
	scanner := bufio.NewScanner(p.conn)
	scanner.Split(scanFrame)
	scanner.Buffer(make([]byte, 1024), maxFrameSize)
	for {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			return io.EOF
		}
		var message rpcMessage
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&message); err != nil {
			return fmt.Errorf("无效 RPC 消息: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return errors.New("每行只能包含一条 RPC 消息")
		}
		if message.ID == 0 {
			return errors.New("RPC 消息缺少有效 ID")
		}
		switch message.Type {
		case "response":
			if len(message.Args) != 0 {
				return errors.New("响应不能包含 Args")
			}
			p.mu.Lock()
			result := p.pending[message.ID]
			delete(p.pending, message.ID)
			p.mu.Unlock()
			// 已超时调用的迟到响应不能交给其他调用。
			if result != nil {
				result <- Response{Output: message.Output, Error: message.Error}
			}
		case "request":
			if len(message.Args) == 0 || message.Args[0] == "" || message.Output != "" || message.Error != "" {
				return errors.New("无效 RPC 请求字段")
			}
			p.mu.Lock()
			if p.err != nil {
				err := p.err
				p.mu.Unlock()
				return err
			}
			_, duplicate := p.incoming[message.ID]
			if duplicate || len(p.incoming) >= maxInFlight {
				p.mu.Unlock()
				return errors.New("请求 ID 重复或并发请求过多")
			}
			p.incoming[message.ID] = struct{}{}
			p.mu.Unlock()
			// 收包循环不能等待业务处理，否则反向调用会死锁。
			p.handlers.Add(1)
			go p.handleRequest(message)
		default:
			return errors.New("未知 RPC 消息类型")
		}
	}
}

// handleRequest 在受限上下文中执行一个已登记请求，并将业务结果返回给对端。
// 任务结束时减少工作组计数；连接已经取消时不再发送响应。
// handler 可使用 peer 发起反向调用，但必须及时响应 ctx，不能依赖不可中断的阻塞操作。
func (p *rpcPeer) handleRequest(message rpcMessage) {
	defer p.handlers.Done()
	ctx, cancel := context.WithTimeout(p.ctx, rpcTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return
	}
	reply := p.handler(ctx, p, message.Args...)
	if p.ctx.Err() != nil {
		return
	}
	if err := p.send(p.ctx, rpcMessage{
		Type: "response", ID: message.ID, Output: reply.Output, Error: reply.Error,
	}); err != nil {
		p.shutdown(err)
	}
}

// scanFrame 为 Scanner 提供严格的换行分帧，返回消费字节数、帧内容和解析错误。
// 只有遇到换行符才交付一帧；EOF 或读失败时仍有尾部数据则返回不完整帧错误。
// 这可防止未发送完的 JSON 在断连或超时后被错误地当作完整请求执行。
func scanFrame(data []byte, atEOF bool) (int, []byte, error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return 0, nil, nil
}

// serve 管理 root 端的一条已验证连接，并主动调用主进程的 get-state 验证反向通信。
// 随后保持双向 RPC 服务，直到上下文取消、对端断开或通信出错。
// 返回前关闭节点并等待在途任务收尾，避免退出后留下仍在执行的命令。
func serve(ctx context.Context, conn net.Conn) error {
	executor := NewRootExecutor()
	peer := newRPCPeer(ctx, conn, executor.Execute)
	defer func() {
		peer.Close()
		peer.Wait()
	}()
	if _, err := peer.Call(ctx, "get-state"); err != nil {
		return fmt.Errorf("回调主进程失败: %w", err)
	}
	return peer.Wait()
}

type commandSpec struct {
	run       rpcHandler
	retrySafe bool
}

type commandRegistry struct {
	whitelist map[string]commandSpec
}

// Execute 用 Args[0] 查白名单，并将完整 Args 交给处理器，未登记项一律拒绝。
func (e *commandRegistry) Execute(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) == 0 || args[0] == "" {
		return Response{Error: "Args[0] 不能为空"}
	}
	command, allowed := e.whitelist[args[0]]
	if !allowed || command.run == nil {
		return Response{Error: "不允许的操作"}
	}
	if err := ctx.Err(); err != nil {
		return Response{Error: err.Error()}
	}
	return command.run(ctx, peer, args...)
}

// CanRetry 与命令执行共用白名单，只有显式声明安全的命令才允许断连重试。
func (e *commandRegistry) CanRetry(args ...string) bool {
	if len(args) == 0 || args[0] == "" {
		return false
	}
	command, allowed := e.whitelist[args[0]]
	return allowed && command.run != nil && command.retrySafe
}

// ping 拒绝额外参数，避免把业务输入无声地当作心跳忽略。
func (e *commandRegistry) ping(ctx context.Context, peer *rpcPeer, args ...string) Response {
	if len(args) != 1 {
		return Response{Error: "ping 不接受额外参数"}
	}
	return Response{Output: "pong"}
}

// executeID 以当前进程身份执行固定的系统 id 命令，供普通端和 root 端共用。
// 命令路径、工作目录和环境均固定；args 按独立参数传给 id，不经过 Shell 解释。
// 成功返回标准输出与标准错误的合并文本，失败时保留退出原因和命令诊断内容。
func executeID(ctx context.Context, args ...string) Response {
	// 用户参数只能影响固定的 id 程序，不能选择可执行文件或拼接 Shell。
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/id", args...)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Response{Error: fmt.Sprintf("id 执行失败: %v: %s", err, output)}
	}
	return Response{Output: string(output)}
}
