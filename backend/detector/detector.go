// Package detector watches for USB-tethering network interfaces.
// Platform-specific implementations live in detector_linux.go / detector_windows.go.
package detector

import "time"

// InterfaceEvent describes a network interface state change.
type InterfaceEvent struct {
	Name      string // OS interface name (e.g. "usb0", "Ethernet 3")
	Added     bool   // true = appeared, false = removed
	LinkLocal string // detected IP on the interface, may be empty
}

// Config tunes the detector. Populate from config.Config fields.
type Config struct {
	// WatchNames lists exact interface names to always report, in addition to
	// the heuristic matching in each platform implementation.
	WatchNames []string
	// PollInterval controls how often Windows polls net.Interfaces().
	PollInterval time.Duration
}

// DefaultConfig returns sensible defaults (used if no config.Config is available).
func DefaultConfig() Config {
	return Config{
		WatchNames:   []string{"usb0", "rndis0", "usb1"},
		PollInterval: 2 * time.Second,
	}
}

// Detector watches for USB-tethering network interfaces.
type Detector interface {
	// Start begins watching. Events are sent on the returned channel.
	// Call the returned cancel function to stop watching and close the channel.
	Start(cfg Config) (<-chan InterfaceEvent, func(), error)
}
