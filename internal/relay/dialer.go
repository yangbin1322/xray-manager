package relay

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// dialViaHTTPProxy 经 HTTP 前置代理 CONNECT 到目标地址。
func dialViaHTTPProxy(ctx context.Context, proxyURL *url.URL, target string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("连接前置代理 %s 失败: %v", proxyURL.Host, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	var authHeader string
	if proxyURL.User != nil {
		pass, _ := proxyURL.User.Password()
		authHeader = fmt.Sprintf("Proxy-Authorization: %s\r\n", basicAuthHeader(proxyURL.User.Username(), pass))
	}
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n%sProxy-Connection: Keep-Alive\r\n\r\n",
		target, target, authHeader)
	if _, err := io.WriteString(conn, connectReq); err != nil {
		conn.Close()
		return nil, fmt.Errorf("向前置代理发送 CONNECT 失败: %v", err)
	}

	// 用 bufio 读响应后必须确认无残留，否则隧道数据会丢在缓冲里
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取前置代理响应失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		conn.Close()
		return nil, fmt.Errorf("前置代理拒绝 CONNECT: %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})

	if n := reader.Buffered(); n > 0 {
		buffered, _ := reader.Peek(n)
		return &prefixedConn{Conn: conn, prefix: append([]byte(nil), buffered...)}, nil
	}
	return conn, nil
}

// prefixedConn 把握手时预读进缓冲的数据补回读取流。
type prefixedConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// SOCKS5 协议常量（RFC 1928 / RFC 1929）
const (
	socks5Version    = 0x05
	socks5AuthNone   = 0x00
	socks5AuthUser   = 0x02
	socks5CmdConnect = 0x01
	socks5AddrIPv4   = 0x01
	socks5AddrDomain = 0x03
	socks5AddrIPv6   = 0x04
)

var socks5Errors = map[byte]string{
	0x01: "常规 SOCKS 服务器故障",
	0x02: "规则不允许的连接",
	0x03: "网络不可达",
	0x04: "主机不可达",
	0x05: "连接被拒绝",
	0x06: "TTL 超时",
	0x07: "不支持的命令",
	0x08: "不支持的地址类型",
}

// dialViaSOCKS5 经 SOCKS5 前置代理连接目标地址。
// 目标域名交由代理解析（socks5h 语义），避免本地 DNS 污染影响解析结果。
func dialViaSOCKS5(ctx context.Context, proxyURL *url.URL, target string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("目标地址无效: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("目标端口无效: %s", portStr)
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("连接前置代理 %s 失败: %v", proxyURL.Host, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := socks5Handshake(conn, proxyURL, host, port); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func socks5Handshake(conn net.Conn, proxyURL *url.URL, host string, port int) error {
	var username, password string
	if proxyURL.User != nil {
		username = proxyURL.User.Username()
		password, _ = proxyURL.User.Password()
	}

	// 方法协商
	methods := []byte{socks5AuthNone}
	if username != "" {
		methods = []byte{socks5AuthUser, socks5AuthNone}
	}
	greeting := append([]byte{socks5Version, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("SOCKS5 握手写入失败: %v", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("SOCKS5 握手响应读取失败: %v", err)
	}
	if reply[0] != socks5Version {
		return fmt.Errorf("SOCKS5 版本不匹配: %d", reply[0])
	}

	switch reply[1] {
	case socks5AuthNone:
	case socks5AuthUser:
		if username == "" {
			return errors.New("前置代理要求用户名认证，但未配置凭证")
		}
		if err := socks5UserAuth(conn, username, password); err != nil {
			return err
		}
	case 0xFF:
		return errors.New("前置代理拒绝所有认证方式")
	default:
		return fmt.Errorf("前置代理要求不支持的认证方式: %d", reply[1])
	}

	// CONNECT 请求
	request := []byte{socks5Version, socks5CmdConnect, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, socks5AddrIPv4)
			request = append(request, ip4...)
		} else {
			request = append(request, socks5AddrIPv6)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("目标域名过长: %d 字节", len(host))
		}
		request = append(request, socks5AddrDomain, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("SOCKS5 CONNECT 写入失败: %v", err)
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fmt.Errorf("SOCKS5 CONNECT 响应读取失败: %v", err)
	}
	if head[1] != 0x00 {
		if msg, ok := socks5Errors[head[1]]; ok {
			return fmt.Errorf("前置代理连接目标失败: %s", msg)
		}
		return fmt.Errorf("前置代理连接目标失败，错误码 %d", head[1])
	}

	// 读掉响应中的绑定地址，避免残留字节混进隧道数据
	var addrLen int
	switch head[3] {
	case socks5AddrIPv4:
		addrLen = net.IPv4len
	case socks5AddrIPv6:
		addrLen = net.IPv6len
	case socks5AddrDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return fmt.Errorf("SOCKS5 响应地址读取失败: %v", err)
		}
		addrLen = int(length[0])
	default:
		return fmt.Errorf("SOCKS5 响应地址类型无效: %d", head[3])
	}
	if _, err := io.ReadFull(conn, make([]byte, addrLen+2)); err != nil {
		return fmt.Errorf("SOCKS5 响应地址读取失败: %v", err)
	}
	return nil
}

// socks5UserAuth 执行 RFC 1929 用户名/密码认证。
func socks5UserAuth(conn net.Conn, username, password string) error {
	if len(username) > 255 || len(password) > 255 {
		return errors.New("前置代理用户名或密码过长")
	}
	auth := []byte{0x01, byte(len(username))}
	auth = append(auth, username...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, password...)
	if _, err := conn.Write(auth); err != nil {
		return fmt.Errorf("SOCKS5 认证写入失败: %v", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("SOCKS5 认证响应读取失败: %v", err)
	}
	if reply[1] != 0x00 {
		return errors.New("前置代理用户名或密码错误")
	}
	return nil
}

// normalizePreProxyURL 补全缺少 scheme 的前置代理地址（默认 socks5）。
func normalizePreProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		return "socks5://" + raw
	}
	return raw
}
