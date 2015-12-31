package proxyclient

import (
	"bytes"
	"io"
	"net"
	"testing"
)

var B1 = []byte{15, 63, 25, 41, 63, 48, 45, 32, 14}
var B2 = []byte{41, 68, 23, 94, 14, 86, 42, 56}
var B3 = []byte{47, 65, 36, 14, 89, 96, 32, 14, 56}

func testDirectProxyTCP1(t *testing.T) {

	p, err := newDriectProxyClient("",make(map[string][]string))
	if err != nil {
		t.Fatalf("启动直连代理失败：%s", err)
		return
	}

	b := make([]byte, 30)
	c, err := p.Dial("tcp", "127.0.0.1:13336")
	if err != nil {
		t.Fatalf("代理连接失败：%v", err)
	}
	if _, err := c.Read(b); err != nil {
		t.Fatalf("代理读错误：%v", err)
	}
	if bytes.Equal(b[:len(B1)], B1) != true {
		t.Fatalf("数据不匹配：%v", err)
	}

	if _, err := c.Write(B2); err != nil {
		t.Fatalf("写数据失败：%v", err)
	}

	if _, err := c.Read(b); err != nil {
		t.Fatalf("代理读错误：%v", err)
	}

	if bytes.Equal(b[:len(B3)], B3) != true {
		t.Fatalf("数据不匹配：%v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("关闭连接错误。")
	}
}

func TestDirectProxyTCP(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:13336")
	if err != nil {
		t.Fatalf("错误,%v", err)
	}

	go testDirectProxyTCP1(t)

	b := make([]byte, 30)

	c, err := l.Accept()
	if err != nil {
		t.Fatalf("接受连接错误：%v", err)
	}

	if _, err := c.Write(B1); err != nil {
		t.Fatalf("写数据失败：%v", err)
	}

	if _, err := c.Read(b); err != nil {
		t.Fatalf("读错误：%v", err)
	}
	if bytes.Equal(b[:len(B2)], B2) != true {
		t.Fatalf("数据不匹配：%v", err)
	}

	if _, err := c.Write(B3); err != nil {
		t.Fatalf("写数据失败：%v", err)
	}

	if _, err := c.Read(b); err != io.EOF {
		t.Fatalf("读结尾错误：%v", err)
	}

}
