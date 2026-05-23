//go:build linux || darwin

package discovery

import (
	"net"
	"syscall"
)

func joinMulticast(conn *net.UDPConn) {
	// Join 224.0.0.251 on all eligible interfaces via raw sysctl.
	// Failures are non-fatal — UDP still works on most local networks.
	ifaces, _ := net.Interfaces()
	mcastIP := [4]byte{224, 0, 0, 251}

	rawConn, err := conn.SyscallConn()
	if err != nil {
		return
	}
	rawConn.Control(func(fd uintptr) {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
				continue
			}
			mreq := &syscall.IPMreq{Multiaddr: mcastIP}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				ip4 := ipNet.IP.To4()
				if ip4 == nil {
					continue
				}
				copy(mreq.Interface[:], ip4)
				syscall.SetsockoptIPMreq(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
				break
			}
		}
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	})
}
