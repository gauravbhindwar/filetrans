//go:build linux

package detector

import (
	"fmt"
	"net"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SizeofIfInfomsg and SizeofIfAddrmsg sizes per linux/rtnetlink.h
const (
	sizeofIfInfomsg = 16
	sizeofIfAddrmsg = 8
)

type linuxDetector struct{}

// New returns the Linux udev-netlink detector.
func New() Detector { return &linuxDetector{} }

func (d *linuxDetector) Start(cfg Config) (<-chan InterfaceEvent, func(), error) {
	sock, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, nil, fmt.Errorf("netlink socket: %w", err)
	}

	addr := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: unix.RTNLGRP_LINK | unix.RTNLGRP_IPV4_IFADDR,
	}
	if err := unix.Bind(sock, addr); err != nil {
		unix.Close(sock)
		return nil, nil, fmt.Errorf("netlink bind: %w", err)
	}

	events := make(chan InterfaceEvent, 16)
	stop := make(chan struct{})

	cancel := func() {
		close(stop)
		unix.Close(sock)
	}

	go func() {
		defer close(events)
		buf := make([]byte, 4096)
		for {
			select {
			case <-stop:
				return
			default:
			}

			n, _, err := unix.Recvfrom(sock, buf, 0)
			if err != nil {
				return
			}

			msgs, err := syscall.ParseNetlinkMessage(buf[:n])
			if err != nil {
				continue
			}

			for _, msg := range msgs {
				ev, ok := parseMessage(msg, cfg)
				if !ok {
					continue
				}
				select {
				case events <- ev:
				case <-stop:
					return
				}
			}
		}
	}()

	return events, cancel, nil
}

func parseMessage(msg syscall.NetlinkMessage, cfg Config) (InterfaceEvent, bool) {
	switch msg.Header.Type {
	case unix.RTM_NEWLINK:
		return parseLinkMsg(msg.Data, true, cfg)
	case unix.RTM_DELLINK:
		return parseLinkMsg(msg.Data, false, cfg)
	case unix.RTM_NEWADDR:
		return parseAddrMsg(msg.Data, cfg)
	}
	return InterfaceEvent{}, false
}

func parseLinkMsg(data []byte, added bool, cfg Config) (InterfaceEvent, bool) {
	if len(data) < sizeofIfInfomsg {
		return InterfaceEvent{}, false
	}

	// Suppress unused variable warning — info is read but index used only.
	_ = (*[sizeofIfInfomsg]byte)(unsafe.Pointer(&data[0]))

	attrs, err := syscall.ParseNetlinkRouteAttr(&syscall.NetlinkMessage{
		Data: data[sizeofIfInfomsg:],
	})
	if err != nil {
		return InterfaceEvent{}, false
	}

	var name string
	for _, attr := range attrs {
		if attr.Attr.Type == unix.IFLA_IFNAME {
			name = strings.TrimRight(string(attr.Value), "\x00")
		}
	}

	if !isUSBInterface(name, cfg) {
		return InterfaceEvent{}, false
	}

	return InterfaceEvent{Name: name, Added: added}, true
}

func parseAddrMsg(data []byte, cfg Config) (InterfaceEvent, bool) {
	if len(data) < sizeofIfAddrmsg {
		return InterfaceEvent{}, false
	}

	attrs, err := syscall.ParseNetlinkRouteAttr(&syscall.NetlinkMessage{
		Data: data[sizeofIfAddrmsg:],
	})
	if err != nil {
		return InterfaceEvent{}, false
	}

	var ifName, ip string
	for _, attr := range attrs {
		switch attr.Attr.Type {
		case unix.IFA_LABEL:
			ifName = strings.TrimRight(string(attr.Value), "\x00")
		case unix.IFA_LOCAL:
			if len(attr.Value) == 4 {
				ip = net.IP(attr.Value).String()
			}
		}
	}

	if !isUSBInterface(ifName, cfg) {
		return InterfaceEvent{}, false
	}

	return InterfaceEvent{Name: ifName, Added: true, LinkLocal: ip}, true
}

func isUSBInterface(name string, cfg Config) bool {
	for _, w := range cfg.WatchNames {
		if name == w {
			return true
		}
	}
	return strings.HasPrefix(name, "usb") || strings.HasPrefix(name, "rndis")
}
