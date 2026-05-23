//go:build windows

package detector

import (
	"net"
	"strings"
	"time"
)

type windowsDetector struct{}

// New returns the Windows polling-based detector.
// WMI event subscriptions require COM/DCOM setup; polling is simpler and reliable.
func New() Detector { return &windowsDetector{} }

func (d *windowsDetector) Start(cfg Config) (<-chan InterfaceEvent, func(), error) {
	events := make(chan InterfaceEvent, 16)
	stop := make(chan struct{})

	cancel := func() { close(stop) }

	go func() {
		defer close(events)

		known := snapshotInterfaces()
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()

		// Emit events for USB interfaces already connected at startup.
		for name, ip := range known {
			if isUSBAdapterName(name) || isWatchedName(name, cfg.WatchNames) {
				events <- InterfaceEvent{Name: name, Added: true, LinkLocal: ip}
			}
		}

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				current := snapshotInterfaces()

				// Detect added interfaces
				for name, ip := range current {
					if _, exists := known[name]; !exists {
						if isUSBAdapterName(name) || isWatchedName(name, cfg.WatchNames) {
							events <- InterfaceEvent{Name: name, Added: true, LinkLocal: ip}
						}
					}
				}

				// Detect removed interfaces
				for name := range known {
					if _, exists := current[name]; !exists {
						if isUSBAdapterName(name) || isWatchedName(name, cfg.WatchNames) {
							events <- InterfaceEvent{Name: name, Added: false}
						}
					}
				}

				known = current
			}
		}
	}()

	return events, cancel, nil
}

// snapshotInterfaces returns a map of interface name → first IPv4 address.
func snapshotInterfaces() map[string]string {
	result := make(map[string]string)
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip string
			switch v := addr.(type) {
			case *net.IPNet:
				if v.IP.To4() != nil {
					ip = v.IP.String()
				}
			case *net.IPAddr:
				if v.IP.To4() != nil {
					ip = v.IP.String()
				}
			}
			if ip != "" {
				result[iface.Name] = ip
				break
			}
		}
	}
	return result
}

// isUSBAdapterName heuristically identifies RNDIS/USB Ethernet adapters on Windows.
// Windows names them things like "Ethernet 3", "USB Ethernet", "RNDIS Gadget".
func isUSBAdapterName(name string) bool {
	lower := strings.ToLower(name)
	keywords := []string{"rndis", "usb", "gadget", "linux"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isWatchedName(name string, watchNames []string) bool {
	for _, wn := range watchNames {
		if strings.EqualFold(name, wn) {
			return true
		}
	}
	return false
}
