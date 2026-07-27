package relay

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// SOCKS5 入站：客户端以 SOCKS5 协议接入，会话标识同样放在用户名里
// （RFC 1929 用户名/密码认证）。中继取出用户名后仍按模板改写，再以
// HTTP CONNECT 连上游住宅网关——上游协议不变，只是客户端侧多了一种接法。

// SOCKS5 回复码（RFC 1928 第 6 节）
const (
	socks5ReplySuccess          = 0x00
	socks5ReplyGeneralFail      = 0x01
	socks5ReplyHostUnreach      = 0x04
	socks5ReplyCmdNotSupported  = 0x07
	socks5ReplyAddrNotSupported = 0x08
)

// handleSOCKS5Inbound 处理一个 SOCKS5 客户端连接。
// 调用方只 Peek 了版本字节，此处从头开始完整解析握手。
func (r *Relay) handleSOCKS5Inbound(clientConn net.Conn, clientReader *bufio.Reader) {
	_ = clientConn.SetDeadline(time.Now().Add(handshakeTimeout))

	session, ok := r.socks5Negotiate(clientConn, clientReader)
	if !ok {
		return
	}
	r.recordSession(session)

	target, ok := r.socks5ReadConnectRequest(clientConn, clientReader)
	if !ok {
		return
	}

	upstreamConn, err := r.dialUpstream()
	if err != nil {
		atomic.AddInt64(&r.failedConns, 1)
		r.logf("连接上游 %s 失败 (SOCKS5 会话 %s): %v", r.cfg.UpstreamAddr, session, err)
		writeSOCKS5Reply(clientConn, socks5ReplyHostUnreach)
		return
	}
	defer upstreamConn.Close()

	// 上游是 HTTP 代理，用 CONNECT 打通到目标
	upstreamAuth := basicAuthHeader(r.upstreamUsername(session), r.cfg.UpstreamPassword)
	upstreamReader, err := openUpstreamTunnel(upstreamConn, target, upstreamAuth)
	if err != nil {
		atomic.AddInt64(&r.failedConns, 1)
		r.logf("上游隧道建立失败 %s (SOCKS5 会话 %s): %v", target, session, err)
		writeSOCKS5Reply(clientConn, socks5ReplyGeneralFail)
		return
	}

	if !writeSOCKS5Reply(clientConn, socks5ReplySuccess) {
		return
	}
	_ = clientConn.SetDeadline(time.Time{})

	// 上游握手可能预读了隧道数据，补发后再进入裸转发
	if n := upstreamReader.Buffered(); n > 0 {
		buffered, _ := upstreamReader.Peek(n)
		if _, err := clientConn.Write(buffered); err != nil {
			return
		}
		atomic.AddInt64(&r.bytesDown, int64(n))
		_, _ = upstreamReader.Discard(n)
	}

	r.pipe(clientConn, clientReader, upstreamConn)
}

// socks5Negotiate 完成方法协商与认证，返回客户端用户名（会话标识）。
//
// 会话标识必须来自用户名，所以总是要求用户名/密码认证；
// 客户端只报"无需认证"时无法携带会话，直接拒绝。
func (r *Relay) socks5Negotiate(clientConn net.Conn, reader *bufio.Reader) (string, bool) {
	version, err := reader.ReadByte()
	if err != nil || version != socks5Version {
		return "", false
	}

	methodCount, err := reader.ReadByte()
	if err != nil {
		return "", false
	}
	methods := make([]byte, methodCount)
	if _, err := io.ReadFull(reader, methods); err != nil {
		return "", false
	}

	supportsUserAuth := false
	for _, m := range methods {
		if m == socks5AuthUser {
			supportsUserAuth = true
			break
		}
	}
	if !supportsUserAuth {
		// 0xFF = 无可接受的方法
		_, _ = clientConn.Write([]byte{socks5Version, 0xFF})
		r.logf("SOCKS5 客户端不支持用户名认证，无法携带会话标识")
		return "", false
	}
	if _, err := clientConn.Write([]byte{socks5Version, socks5AuthUser}); err != nil {
		return "", false
	}

	session, password, ok := readSOCKS5UserAuth(reader)
	if !ok {
		_, _ = clientConn.Write([]byte{0x01, 0x01})
		return "", false
	}
	if session == "" {
		_, _ = clientConn.Write([]byte{0x01, 0x01})
		r.logf("SOCKS5 客户端用户名为空，无法确定会话")
		return "", false
	}
	if r.cfg.LocalPassword != "" && password != r.cfg.LocalPassword {
		_, _ = clientConn.Write([]byte{0x01, 0x01})
		r.logf("SOCKS5 客户端密码错误 (会话 %s)", session)
		return "", false
	}

	// 0x00 = 认证成功
	if _, err := clientConn.Write([]byte{0x01, 0x00}); err != nil {
		return "", false
	}
	return session, true
}

// readSOCKS5UserAuth 解析 RFC 1929 的用户名/密码认证请求。
func readSOCKS5UserAuth(reader *bufio.Reader) (username, password string, ok bool) {
	authVersion, err := reader.ReadByte()
	if err != nil || authVersion != 0x01 {
		return "", "", false
	}

	userLen, err := reader.ReadByte()
	if err != nil {
		return "", "", false
	}
	userBuf := make([]byte, userLen)
	if _, err := io.ReadFull(reader, userBuf); err != nil {
		return "", "", false
	}

	passLen, err := reader.ReadByte()
	if err != nil {
		return "", "", false
	}
	passBuf := make([]byte, passLen)
	if _, err := io.ReadFull(reader, passBuf); err != nil {
		return "", "", false
	}

	return string(userBuf), string(passBuf), true
}

// socks5ReadConnectRequest 解析 CONNECT 请求，返回 host:port。
// 域名保持原样交给上游解析，避免本地 DNS 影响住宅出口的地域一致性。
func (r *Relay) socks5ReadConnectRequest(clientConn net.Conn, reader *bufio.Reader) (string, bool) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(reader, head); err != nil {
		return "", false
	}
	if head[0] != socks5Version {
		writeSOCKS5Reply(clientConn, socks5ReplyGeneralFail)
		return "", false
	}
	if head[1] != socks5CmdConnect {
		// 住宅代理网关只提供 TCP CONNECT，BIND/UDP 无法支持
		writeSOCKS5Reply(clientConn, socks5ReplyCmdNotSupported)
		r.logf("SOCKS5 客户端请求了不支持的命令: %d", head[1])
		return "", false
	}

	var host string
	switch head[3] {
	case socks5AddrIPv4:
		buf := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", false
		}
		host = net.IP(buf).String()
	case socks5AddrIPv6:
		buf := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", false
		}
		host = net.IP(buf).String()
	case socks5AddrDomain:
		length, err := reader.ReadByte()
		if err != nil {
			return "", false
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", false
		}
		host = string(buf)
	default:
		writeSOCKS5Reply(clientConn, socks5ReplyAddrNotSupported)
		return "", false
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBuf); err != nil {
		return "", false
	}
	port := binary.BigEndian.Uint16(portBuf)

	return net.JoinHostPort(host, strconv.Itoa(int(port))), true
}

// writeSOCKS5Reply 回写 SOCKS5 应答。绑定地址填 0.0.0.0:0——
// 真实出口在住宅网关侧，中继无从得知，客户端也不需要。
func writeSOCKS5Reply(conn net.Conn, code byte) bool {
	reply := []byte{socks5Version, code, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err == nil
}

// openUpstreamTunnel 向上游 HTTP 代理发 CONNECT 并校验响应，
// 返回已读到响应尾部的 reader（可能含预读的隧道数据）。
func openUpstreamTunnel(upstreamConn net.Conn, target, upstreamAuth string) (*bufio.Reader, error) {
	_ = upstreamConn.SetDeadline(time.Now().Add(handshakeTimeout))
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nProxy-Connection: Keep-Alive\r\n\r\n",
		target, target, upstreamAuth)
	if _, err := io.WriteString(upstreamConn, connectReq); err != nil {
		return nil, fmt.Errorf("写入上游 CONNECT 失败: %v", err)
	}

	reader := bufio.NewReader(upstreamConn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, fmt.Errorf("读取上游 CONNECT 响应失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("上游拒绝 CONNECT: %s", resp.Status)
	}
	_ = upstreamConn.SetDeadline(time.Time{})
	return reader, nil
}
