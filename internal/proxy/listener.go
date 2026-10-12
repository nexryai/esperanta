package proxy

import (
	"net"
	"sync"

	"github.com/nexryai/esperanta/internal/config"
)

// Listener checks the TCP peer before allocating HTTP parser state.
type Listener struct {
	net.Listener
	Config config.Config
	Slots  chan struct{}
}

func (l *Listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if !l.Config.AllowsSource(c.RemoteAddr().String()) {
			c.Close()
			continue
		}
		select {
		case l.Slots <- struct{}{}:
			return &limitedConn{
				Conn: c,
				release: func() {
					<-l.Slots
				},
			}, nil
		default:
			c.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
