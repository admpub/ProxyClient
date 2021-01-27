package proxyclient

import (
	"errors"
	"fmt"
	"github.com/sun8911879/shadowsocksR"
	"github.com/sun8911879/shadowsocksR/obfs"
	"github.com/sun8911879/shadowsocksR/protocol"
	"github.com/sun8911879/shadowsocksR/ssr"
	"github.com/sun8911879/shadowsocksR/tools/socks"
	"net"
	"strconv"
	"strings"
	"time"
)

type ssrTCPConn struct {
	TCPConn
	sc          Conn
	proxyClient ProxyClient
}

type ssrUDPConn struct {
	net.UDPConn
	proxyClient ProxyClient
}

// SSInfo fields that shadowsocks/shadowsocksr used only
type SSInfo struct {
	SSRInfo
	EncryptMethod   string
	EncryptPassword string
}

// SSRInfo fields that shadowsocksr used only
type SSRInfo struct {
	Obfs          string
	ObfsParam     string
	ObfsData      interface{}
	Protocol      string
	ProtocolParam string
	ProtocolData  interface{}
}

// BackendInfo all fields that a backend used
type BackendInfo struct {
	SSInfo
	Address string
	Type    string
}

type ssrProxyClient struct {
	proxyAddr string
	upProxy   ProxyClient
	backend   BackendInfo
	query     map[string][]string
}

func newSsrProxyClient(proxyAddr, method, password string, upProxy ProxyClient, query map[string][]string) (ProxyClient, error) {
	
	if upProxy == nil {
		nUpProxy, err := newDriectProxyClient("", false, 0, make(map[string][]string))
		if err != nil {
			return nil, fmt.Errorf("创建直连代理错误：%v", err)
		}
		upProxy = nUpProxy
	}

	queryGet := func(key string) string {
		if query == nil {
			return ""
		}
		v, ok := query[key]
		if !ok || len(v) == 0 {
			return ""
		}
		return v[0]
	}

	p := ssrProxyClient{}

	bi := BackendInfo{
	Address: proxyAddr,
	Type:    "ssr",
	SSInfo: SSInfo{
		EncryptMethod:   method,
		EncryptPassword: password,
		SSRInfo: 
			SSRInfo{
				Protocol:      queryGet("protocol"),
				ProtocolParam: queryGet("protoparam"),
				Obfs:          queryGet("obfs"),
				ObfsParam:     queryGet("obfsparam"),
			},
		},
	}

	p.proxyAddr = proxyAddr
	p.backend = bi
	p.upProxy = upProxy
	p.query = query

	return &p, nil
}

func (p *ssrProxyClient) Dial(network, address string) (net.Conn, error) {
	if strings.HasPrefix(strings.ToLower(network), "tcp") {
		return p.DialTCPSAddr(network, address)
	} else if strings.HasPrefix(strings.ToLower(network), "udp") {
		addr, err := net.ResolveUDPAddr(network, address)
		if err != nil {
			return nil, fmt.Errorf("地址解析错误:%v", err)
		}
		return p.DialUDP(network, nil, addr)
	} else {
		return nil, errors.New("未知的 network 类型。")
	}
}

func (p *ssrProxyClient) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return p.DialTCPSAddrTimeout(network, address, timeout)
	case "udp", "udp4", "udp6":
		return nil, errors.New("暂不支持UDP协议")
	default:
		return nil, errors.New("未知的协议")
	}
}

func (p *ssrProxyClient) DialTCP(network string, laddr, raddr *net.TCPAddr) (net.Conn, error) {
	if laddr != nil || laddr.Port != 0 {
		return nil, errors.New("代理协议不支持指定本地地址。")
	}

	return p.DialTCPSAddr(network, raddr.String())
}

func (p *ssrProxyClient) DialTCPSAddr(network string, raddr string) (ProxyTCPConn, error) {
	return p.DialTCPSAddrTimeout(network, raddr, 0)
}

func (p *ssrProxyClient) DialTCPSAddrTimeout(network string, raddr string, timeout time.Duration) (rconn ProxyTCPConn, rerr error) {
	// 截止时间
	finalDeadline := time.Time{}
	if timeout != 0 {
		finalDeadline = time.Now().Add(timeout)
	}

	rawaddr := socks.ParseAddr(raddr)

	c, err := p.upProxy.DialTCPSAddrTimeout(network, p.proxyAddr, timeout)
	if err != nil {
		return nil, fmt.Errorf("无法连接代理服务器 %v ，错误：%v", p.proxyAddr, err)
	}

	ch := make(chan int)
	defer close(ch)

	// 实际执行部分
	run := func() {		
		bi := p.backend

		encryptMethod := bi.EncryptMethod
		encryptKey := bi.EncryptPassword
		cipher, err := shadowsocksr.NewStreamCipher(encryptMethod, encryptKey)
		if err != nil {
			rerr = err
			return
		}
		sc := shadowsocksr.NewSSTCPConn(c, cipher)
		if sc.Conn == nil || sc.RemoteAddr() == nil {
			rerr = errors.New("nil connection")
			return
		}

		rs := strings.Split(sc.RemoteAddr().String(), ":")
		port, _ := strconv.Atoi(rs[1])

		sc.IObfs = obfs.NewObfs(bi.Obfs)
		obfsServerInfo := &ssr.ServerInfoForObfs{
			Host:   rs[0],
			Port:   uint16(port),
			TcpMss: 1460,
			Param:  bi.ObfsParam,
		}
		sc.IObfs.SetServerInfo(obfsServerInfo)
		sc.IProtocol = protocol.NewProtocol(bi.Protocol)
		protocolServerInfo := &ssr.ServerInfoForObfs{
			Host:   rs[0],
			Port:   uint16(port),
			TcpMss: 1460,
			Param:  bi.ProtocolParam,
		}
		sc.IProtocol.SetServerInfo(protocolServerInfo)

		if bi.ObfsData == nil {
			bi.ObfsData = sc.IObfs.GetData()
		}
		sc.IObfs.SetData(bi.ObfsData)

		if bi.ProtocolData == nil {
			bi.ProtocolData = sc.IProtocol.GetData()
		}
		sc.IProtocol.SetData(bi.ProtocolData)

		closed := false
		// 当连接不被使用时，ch<-1会引发异常，这时将关闭连接。
		defer func() {
			e := recover()
			if e != nil && closed == false {
				sc.Close()
			}
		}()

		if _, err := sc.Write(rawaddr); err != nil {
			closed = true
			sc.Close()
			rerr = err
			ch <- 0
			return
		}

		r := ssrTCPConn{TCPConn: c, sc: sc, proxyClient: p} //{c,net.ResolveTCPAddr("tcp","0.0.0.0:0"),net.ResolveTCPAddr("tcp","0.0.0.0:0"),"","",0,0  p}

		rconn = &r
		ch <- 1
	}

	if timeout == 0 {
		go run()

		select {
		case <-ch:
			return
		}
	} else {
		c.SetDeadline(finalDeadline)

		ntimeout := finalDeadline.Sub(time.Now())
		if ntimeout <= 0 {
			return nil, fmt.Errorf("timeout")
		}
		t := time.NewTimer(ntimeout)
		defer t.Stop()

		go run()

		select {
		case <-t.C:
			return nil, fmt.Errorf("连接超时。")
		case <-ch:
			if rerr == nil {
				c.SetDeadline(time.Time{})
			}
			return
		}
	}
}

func (p *ssrProxyClient) DialUDP(network string, laddr, raddr *net.UDPAddr) (conn net.Conn, err error) {
	return nil, errors.New("暂不支持 UDP 协议")
}

func (p *ssrProxyClient) UpProxy() ProxyClient {
	return nil
}

func (p *ssrProxyClient) SetUpProxy(upProxy ProxyClient) error {
	return nil
}

func (c *ssrTCPConn) ProxyClient() ProxyClient {
	return c.proxyClient
}
func (c *ssrTCPConn) Write(b []byte) (n int, err error) {
	return c.sc.Write(b)
}
func (c *ssrTCPConn) Read(b []byte) (n int, err error) {
	return c.sc.Read(b)
}

func (c *ssrTCPConn) Close() error {
	return c.sc.Close()
}

func (c *ssrUDPConn) ProxyClient() ProxyClient {
	return c.proxyClient
}

func (p *ssrProxyClient) GetProxyAddrQuery() map[string][]string {
	return p.query
}
