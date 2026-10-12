package proxy

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestListenerACLAndConnectionLimit(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := &Listener{
		Listener: raw,
		Config:   localConfig(t, raw.Addr().String()),
		Slots:    make(chan struct{}, 1),
	}
	defer listener.Close()

	// The accept loop must discard denied peers and continue accepting.
	accepted := make(chan net.Conn, 3)
	go func() {
		defer close(accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()

	deniedDialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}}
	denied, err := deniedDialer.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Close()
	assertClosed(t, denied)

	client, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var first net.Conn
	select {
	case first = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("allowed connection was not accepted")
	}
	defer first.Close()

	overflow, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer overflow.Close()
	assertClosed(t, overflow)

	// Closing a connection twice must release exactly one slot.
	first.Close()
	first.Close()
	next, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("connection slot was not released")
	}
}

func assertClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err := conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("rejected connection is still open")
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("rejected connection was not closed before timeout")
	}
	if err != io.EOF {
		t.Logf("connection rejected with: %v", err)
	}
}
