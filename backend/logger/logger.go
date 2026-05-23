package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Level string

const (
	INFO  Level = "INFO"
	WARN  Level = "WARN"
	ERROR Level = "ERROR"
	DEBUG Level = "DEBUG"
)

type Event struct {
	Timestamp string `json:"timestamp"`
	Level     Level  `json:"level"`
	OS        string `json:"os"`
	Interface string `json:"interface,omitempty"`
	IP        string `json:"ip,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

var output = os.Stdout

func log(level Level, osName, iface, ip, status, msg string) {
	e := Event{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		OS:        osName,
		Interface: iface,
		IP:        ip,
		Status:    status,
		Message:   msg,
	}
	b, _ := json.Marshal(e)
	fmt.Fprintln(output, string(b))
}

func Info(osName, iface, ip, status, msg string) {
	log(INFO, osName, iface, ip, status, msg)
}

func Warn(osName, iface, ip, status, msg string) {
	log(WARN, osName, iface, ip, status, msg)
}

func Error(osName, iface, ip, status, msg string) {
	log(ERROR, osName, iface, ip, status, msg)
}

func Debug(osName, iface, ip, status, msg string) {
	log(DEBUG, osName, iface, ip, status, msg)
}
