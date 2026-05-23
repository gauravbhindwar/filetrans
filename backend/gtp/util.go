package gtp

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

var (
	deviceIDOnce  sync.Once
	cachedDeviceID string
)

// deviceID returns a stable random identifier for this machine.
// Stored in ~/.config/filetrans/device_id.
func deviceID() string {
	deviceIDOnce.Do(func() {
		path := deviceIDPath()
		if data, err := os.ReadFile(path); err == nil && len(data) == 32 {
			cachedDeviceID = string(data)
			return
		}
		// Generate new ID.
		b := make([]byte, 16)
		rand.Read(b)
		cachedDeviceID = hex.EncodeToString(b)
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, []byte(cachedDeviceID), 0o600)
	})
	return cachedDeviceID
}

func deviceIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "filetrans", "device_id")
}

// sendControl is a package-level helper for use before a Conn is fully built
// (during handshake, using raw net.Conn).
func sendControl(raw net.Conn, ft FrameType, v interface{}) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("gtp marshal %s: %w", ft, err)
	}
	bw := bufio.NewWriter(raw)
	if err := WriteFrame(bw, ft, payload); err != nil {
		return err
	}
	return bw.Flush()
}

// decodeJSON unmarshals payload into v.
func decodeJSON(payload []byte, v interface{}) error {
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("gtp decode: %w", err)
	}
	return nil
}
