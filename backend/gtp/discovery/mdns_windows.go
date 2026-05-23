//go:build windows

package discovery

import (
	"net"
)

func joinMulticast(conn *net.UDPConn) {
	// On Windows, multicast group join requires the ipv4 package (CGO-free but
	// adds a dependency). Skip it — UDP datagrams still reach peers on the same
	// subnet when the OS has a multicast route, which it does by default on LAN.
	_ = conn
}
