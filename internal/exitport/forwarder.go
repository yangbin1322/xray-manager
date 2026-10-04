// Package exitport 实现「出口端口」：一个固定的本地端口始终对应某个出口 IP，
// 背后由哪个节点承载无所谓。
//
// 转发器只做透明 TCP 转发：每条新连接按候选顺序挑一个节点的本地混合端口，
// 原样搬运字节流。不解析代理协议，因此 HTTP 与 SOCKS5 客户端都天然可用；
// 成员集合由调用方动态给出，增减节点无需重启任何内核进程。
package exitport

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// defaultDialTimeout 连本机节点端口的超时。都是 127.0.0.1，正常是毫秒级；
	// 连不上通常是节点进程刚退出，没必要久等，尽快换下一个候选。
	defaultDialTimeout = 2 * time.Second

	// defaultCooldown 拨号失败的上游在这段时间内排到最后，
	// 避免每条新连接都先撞一次已经挂掉的节点。
	defaultCooldown = 30 * time.Second
)

// Config 转发器配置。
type Config struct {
	ListenAddr string

	// Candidates 返回当前可用的上游本地端口，按优先级排好序。
	// 每条新连接调用一次，调用方需自行保证它足够快（可做短 TTL 缓存）。
	// 返回空切片表示当前没有任何节点承载该出口 IP，连接将被直接拒绝——
	// 宁可断开，也不能让流量从别的 IP 出去。
	Candidates func() []int

	Logf func(msg string)

	DialTimeout time.Duration // 为 0 时取默认值
	Cooldown    time.Duration // 为 0 时取默认值
}

// Stats 运行时统计。
type Stats struct {
	ActiveConns   int64
	TotalConns    int64
	RejectedConns int64 // 没有可用候选而被拒绝的连接数
	BytesUp       int64
	BytesDown     int64
	LastUpstream  int // 最近一次成功建立连接的上游端口
}

// Forwarder 出口端口转发器。
type Forwarder struct {
	cfg      Config
	listener net.Listener

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	activeConns   int64
	totalConns    int64
	rejectedConns int64
	bytesUp       int64
	bytesDown     int64
	lastUpstream  int64

	mu       sync.Mutex
	failedAt map[int]time.Time             // 上游端口 → 最近一次拨号失败时间
	conns    map[int]map[net.Conn]net.Conn // 上游端口 → 活动连接（客户端 → 上游）
}

// New 创建转发器，需调用 Start 才开始监听。
func New(cfg Config) *Forwarder {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = defaultCooldown
	}
	return &Forwarder{
		cfg:      cfg,
		done:     make(chan struct{}),
		failedAt: make(map[int]time.Time),
		conns:    make(map[int]map[net.Conn]net.Conn),
	}
}

// Start 开始监听并在后台接受连接。
func (f *Forwarder) Start() error {
	if f.cfg.Candidates == nil {
		return errors.New("未提供候选节点来源")
	}
	listener, err := net.Listen("tcp", f.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %v", f.cfg.ListenAddr, err)
	}
	f.listener = listener

	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		f.acceptLoop()
	}()
	return nil
}

// Addr 返回实际监听地址（端口为 0 时可取到系统分配的端口）。
func (f *Forwarder) Addr() net.Addr {
	if f.listener == nil {
		return nil
	}
	return f.listener.Addr()
}

// Stop 停止监听，断开全部连接并等待收尾。
func (f *Forwarder) Stop() error {
	var err error
	f.closeOnce.Do(func() {
		close(f.done)
		if f.listener != nil {
			err = f.listener.Close()
		}
	})
	f.wg.Wait()
	return err
}

// Stats 返回当前统计快照。
func (f *Forwarder) Stats() Stats {
	return Stats{
		ActiveConns:   atomic.LoadInt64(&f.activeConns),
		TotalConns:    atomic.LoadInt64(&f.totalConns),
		RejectedConns: atomic.LoadInt64(&f.rejectedConns),
		BytesUp:       atomic.LoadInt64(&f.bytesUp),
		BytesDown:     atomic.LoadInt64(&f.bytesDown),
		LastUpstream:  int(atomic.LoadInt64(&f.lastUpstream)),
	}
}

// DropUpstream 立即断开所有经该上游端口的现有连接。
//
// 成员节点出口 IP 变了或节点停了时调用：新连接自然不会再选它，
// 但已建立的长连接（WebSocket、下载、连接池里的 keep-alive）会一直
// 从错误的 IP 出去，必须主动切断，让客户端重连到正确的节点。
func (f *Forwarder) DropUpstream(port int) int {
	f.mu.Lock()
	set := f.conns[port]
	delete(f.conns, port)
	f.mu.Unlock()

	for client, upstream := range set {
		client.Close()
		upstream.Close()
	}
	return len(set)
}

// ActiveUpstreams 返回当前有活动连接的上游端口。
func (f *Forwarder) ActiveUpstreams() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	ports := make([]int, 0, len(f.conns))
	for port, set := range f.conns {
		if len(set) > 0 {
			ports = append(ports, port)
		}
	}
	return ports
}

func (f *Forwarder) logf(format string, args ...interface{}) {
	if f.cfg.Logf != nil {
		f.cfg.Logf(fmt.Sprintf(format, args...))
	}
}

func (f *Forwarder) acceptLoop() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			select {
			case <-f.done:
				return
			default:
			}
			// 临时错误（如 fd 耗尽）退避后重试，其余直接退出
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			f.logf("接受连接失败: %v", err)
			return
		}

		atomic.AddInt64(&f.totalConns, 1)
		atomic.AddInt64(&f.activeConns, 1)
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer atomic.AddInt64(&f.activeConns, -1)
			defer conn.Close()
			f.handleConn(conn)
		}()
	}
}

func (f *Forwarder) handleConn(clientConn net.Conn) {
	upstream, port, err := f.dialUpstream()
	if err != nil {
		atomic.AddInt64(&f.rejectedConns, 1)
		f.logf("%v，已拒绝连接", err)
		return
	}
	atomic.StoreInt64(&f.lastUpstream, int64(port))

	if !f.track(port, clientConn, upstream) {
		// 转发器正在停止
		upstream.Close()
		return
	}
	defer f.untrack(port, clientConn)

	f.pipe(clientConn, upstream)
}

// dialUpstream 按优先级逐个尝试候选，冷却中的排到最后兜底。
func (f *Forwarder) dialUpstream() (net.Conn, int, error) {
	candidates := f.cfg.Candidates()
	if len(candidates) == 0 {
		return nil, 0, errors.New("当前没有节点承载该出口 IP")
	}

	now := time.Now()
	ordered := make([]int, 0, len(candidates))
	var cooling []int
	f.mu.Lock()
	for _, port := range candidates {
		if t, ok := f.failedAt[port]; ok && now.Sub(t) < f.cfg.Cooldown {
			cooling = append(cooling, port)
			continue
		}
		ordered = append(ordered, port)
	}
	f.mu.Unlock()
	ordered = append(ordered, cooling...)

	var lastErr error
	for _, port := range ordered {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), f.cfg.DialTimeout)
		if err == nil {
			f.mu.Lock()
			delete(f.failedAt, port)
			f.mu.Unlock()
			return conn, port, nil
		}
		lastErr = err
		f.mu.Lock()
		f.failedAt[port] = time.Now()
		f.mu.Unlock()
	}
	return nil, 0, fmt.Errorf("全部 %d 个候选节点都连不上（最后一个错误：%v）", len(ordered), lastErr)
}

func (f *Forwarder) track(port int, client, upstream net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
		return false
	default:
	}
	set := f.conns[port]
	if set == nil {
		set = make(map[net.Conn]net.Conn)
		f.conns[port] = set
	}
	set[client] = upstream
	return true
}

func (f *Forwarder) untrack(port int, client net.Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if set := f.conns[port]; set != nil {
		delete(set, client)
		if len(set) == 0 {
			delete(f.conns, port)
		}
	}
}

// pipe 双向复制，任一方向结束即收尾。
// 只等第一个方向结束的理由见 internal/relay 的同名实现：
// 客户端留着连接复用时，等两个方向会让连接迟迟不释放。
func (f *Forwarder) pipe(clientConn, upstreamConn net.Conn) {
	finished := make(chan struct{})
	go func() {
		select {
		case <-f.done:
		case <-finished:
		}
		clientConn.Close()
		upstreamConn.Close()
	}()

	done := make(chan struct{}, 2)

	go func() {
		n, _ := io.Copy(upstreamConn, clientConn)
		atomic.AddInt64(&f.bytesUp, n)
		closeWrite(upstreamConn)
		done <- struct{}{}
	}()

	go func() {
		n, _ := io.Copy(clientConn, upstreamConn)
		atomic.AddInt64(&f.bytesDown, n)
		closeWrite(clientConn)
		done <- struct{}{}
	}()

	<-done
	close(finished)
}

// closeWrite 半关闭写方向，让对端读到 EOF；不支持时退化为直接关闭。
func closeWrite(conn net.Conn) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = conn.Close()
}
