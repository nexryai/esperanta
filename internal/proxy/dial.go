package proxy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

var deniedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
}

func publicIP(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.Zone() != "" {
		return false
	}
	for _, n := range deniedNetworks {
		if n.Contains(a) {
			return false
		}
	}
	return true
}

func (p *Proxy) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !p.c.AllowsPort(port) {
		return nil, errors.New("destination port forbidden")
	}
	ctx, cancel := context.WithTimeout(ctx, p.c.DialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("no destination addresses")
	}
	for _, a := range ips {
		if p.c.DenyPrivate && !publicIP(a) {
			return nil, errors.New("non-public destination forbidden")
		}
	}
	d := net.Dialer{KeepAlive: 30 * time.Second}
	for _, a := range ips {
		// Dial the validated literal address: never resolve the hostname a second time.
		c, e := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if e == nil {
			return c, nil
		}
		err = e
	}
	return nil, err
}
