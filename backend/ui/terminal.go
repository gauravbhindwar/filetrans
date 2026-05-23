// Package ui provides terminal I/O helpers: prompts, progress bars, styled output.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var stdin = bufio.NewReader(os.Stdin)

// Banner prints the app header line.
func Banner(version string) {
	fmt.Printf("\n  filetrans %s  —  USB-C direct file transfer\n\n", version)
}

// SelectRole prompts the user to choose Sender or Receiver.
// Returns "sender" or "receiver".
func SelectRole(ifaceName, peerIP string) string {
	fmt.Printf("  USB connection on %s  (peer: %s)\n\n", ifaceName, peerIP)
	fmt.Println("  Select role:")
	fmt.Println("    1) Sender    — push files to peer")
	fmt.Println("    2) Receiver  — receive files from peer")
	fmt.Println()
	for {
		fmt.Print("  Choice [1/2]: ")
		line := readLine()
		switch line {
		case "1", "s", "S", "sender":
			return "sender"
		case "2", "r", "R", "receiver":
			return "receiver"
		default:
			fmt.Println("  Enter 1 or 2.")
		}
	}
}

// SelectFiles interactively collects paths from the user.
// Handles directory expansion, quote stripping (drag & drop), and validation.
func SelectFiles() []string {
	fmt.Println()
	fmt.Println("  Enter file or folder paths, one per line.")
	fmt.Println("  Drag & drop into the terminal to paste paths.")
	fmt.Println("  Empty line when done.")
	fmt.Println()

	var paths []string
	for {
		fmt.Print("  > ")
		line := strings.Trim(readLine(), `"' `)
		if line == "" {
			if len(paths) > 0 {
				break
			}
			continue
		}
		info, err := os.Stat(line)
		if err != nil {
			Errorf("not found: %v", err)
			continue
		}
		if info.IsDir() {
			files, err := walkDir(line)
			if err != nil {
				Errorf("walk error: %v", err)
				continue
			}
			paths = append(paths, files...)
			Infof("Added directory: %s (%d files)", line, len(files))
		} else {
			paths = append(paths, line)
			Infof("Added: %s  (%s)", info.Name(), FormatBytes(info.Size()))
		}
	}
	return paths
}

// SelectDownloadDir prompts the Receiver for a download directory.
func SelectDownloadDir(defaultDir string) string {
	fmt.Printf("\n  Download directory [%s]: ", defaultDir)
	line := strings.Trim(readLine(), `"' `)
	if line == "" {
		return defaultDir
	}
	if err := os.MkdirAll(line, 0o755); err != nil {
		Warnf("cannot create %s: %v — using default", line, err)
		return defaultDir
	}
	return line
}

// AskManualIP prompts for a peer IP address (fallback / no-USB mode).
func AskManualIP() string {
	for {
		fmt.Print("\n  Enter peer IP address: ")
		ip := readLine()
		if ip != "" {
			return ip
		}
	}
}

// Confirm asks a yes/no question; returns true only for "y" or "yes".
func Confirm(msg string) bool {
	fmt.Printf("  %s [y/N]: ", msg)
	line := strings.ToLower(readLine())
	return line == "y" || line == "yes"
}

// FileOfferPrompt shows incoming file details and asks the receiver to accept.
func FileOfferPrompt(name string, size int64) bool {
	fmt.Printf("\n  Incoming: %s  (%s)\n", name, FormatBytes(size))
	return Confirm("Accept?")
}

// Infof prints an informational message.
func Infof(format string, args ...interface{}) {
	fmt.Printf("  "+format+"\n", args...)
}

// Warnf prints a warning message.
func Warnf(format string, args ...interface{}) {
	fmt.Printf("  [!] "+format+"\n", args...)
}

// Errorf prints an error message.
func Errorf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "  [ERROR] "+format+"\n", args...)
}

// Successf prints a success message.
func Successf(format string, args ...interface{}) {
	fmt.Printf("  [OK] "+format+"\n", args...)
}

// Progress tracks and renders a single-file transfer progress bar.
type Progress struct {
	mu          sync.Mutex
	filename    string
	total       int64
	transferred int64
	startTime   time.Time
	done        bool
}

// NewProgress creates a progress tracker for a file.
func NewProgress(filename string, total int64) *Progress {
	return &Progress{
		filename:  filename,
		total:     total,
		startTime: time.Now(),
	}
}

// Add records n additional bytes transferred and redraws the bar.
func (p *Progress) Add(n int64) {
	p.mu.Lock()
	p.transferred += n
	p.mu.Unlock()
	p.render()
}

// Done marks the transfer complete and advances to the next line.
func (p *Progress) Done() {
	p.mu.Lock()
	p.transferred = p.total
	p.done = true
	p.mu.Unlock()
	p.render()
	fmt.Println()
}

func (p *Progress) render() {
	p.mu.Lock()
	transferred := p.transferred
	total := p.total
	startTime := p.startTime
	p.mu.Unlock()

	if total <= 0 {
		return
	}

	pct := float64(transferred) / float64(total) * 100
	const barW = 30
	filled := int(float64(barW) * pct / 100)
	if filled > barW {
		filled = barW
	}
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", barW-filled)

	elapsed := time.Since(startTime).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(transferred) / elapsed
	}

	var eta string
	if speed > 0 && transferred < total {
		remaining := float64(total-transferred) / speed
		eta = formatDuration(time.Duration(remaining) * time.Second)
	} else {
		eta = "--"
	}

	name := truncate(filepath.Base(p.filename), 22)
	fmt.Printf("\r  %-22s [%s] %5.1f%%  %-10s  ETA %-6s",
		name, bar, pct, FormatBytes(int64(speed))+"/s", eta)
}

// FormatBytes returns a human-readable byte count string.
func FormatBytes(b int64) string {
	if b < 0 {
		b = -b
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return "--"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func readLine() string {
	line, _ := stdin.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func walkDir(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
