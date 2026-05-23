// Package netconfig configures static IP addresses on USB-tethering interfaces.
// All IP values come from config.Config — nothing is hardcoded here.
package netconfig

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"

	"filetrans/backend/config"
)

// HasTargetIP reports whether ifaceName already has cfg.LocalIP() assigned.
func HasTargetIP(ifaceName string, cfg *config.Config) (bool, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return false, fmt.Errorf("interface %q not found: %w", ifaceName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false, err
	}
	target := cfg.LocalIP()
	for _, addr := range addrs {
		if strings.HasPrefix(addr.String(), target) {
			return true, nil
		}
	}
	return false, nil
}

// AssignIP configures a static IP on ifaceName using cfg.LocalIP()/cfg.SubnetPrefix.
// Requires elevated privileges (sudo on Linux, Administrator on Windows).
func AssignIP(ifaceName string, cfg *config.Config) error {
	switch runtime.GOOS {
	case "linux":
		return assignLinux(ifaceName, cfg.LocalCIDR())
	case "windows":
		return assignWindows(ifaceName, cfg.LocalIP(), cfg.SubnetMask())
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// RemoveIP removes the static IP assigned by AssignIP.
// Errors are intentionally ignored by callers on interface removal.
func RemoveIP(ifaceName string, cfg *config.Config) error {
	switch runtime.GOOS {
	case "linux":
		return run("ip", "addr", "del", cfg.LocalCIDR(), "dev", ifaceName)
	case "windows":
		return run("netsh", "interface", "ip", "set", "address",
			"name="+ifaceName, "dhcp")
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func assignLinux(iface, cidr string) error {
	if err := run("ip", "addr", "flush", "dev", iface); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	if err := run("ip", "addr", "add", cidr, "dev", iface); err != nil {
		return fmt.Errorf("addr add: %w", err)
	}
	if err := run("ip", "link", "set", iface, "up"); err != nil {
		return fmt.Errorf("link up: %w", err)
	}
	return nil
}

func assignWindows(iface, ip, mask string) error {
	if err := run("netsh", "interface", "ip", "set", "address",
		"name="+iface, "static", ip, mask, "none"); err != nil {
		return fmt.Errorf("netsh: %w", err)
	}
	return nil
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
