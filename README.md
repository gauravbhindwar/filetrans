# filetrans

Fast, direct file transfer between two laptops via a USB-C cable — no cloud, no Wi-Fi required.

```
┌────────────────┐   USB-C   ┌─────────────────┐
│ Windows / Linux│◄─────────►│ Linux (gadget)  │
│  192.168.7.2   │  virtual  │  192.168.7.1    │
│  (Receiver)    │  Ethernet │  (Sender)       │
└────────────────┘           └─────────────────┘
         WebSocket / chunked binary transfer
```

## Features

- Automatic USB interface detection (Linux `netlink`, Windows WMI polling)
- Role negotiation: choose **Sender** or **Receiver** on each end
- Chunked transfer with **SHA-256 integrity verification**
- **Resume** interrupted transfers from last chunk boundary
- Real-time **progress bar** (speed, ETA, percentage)
- Directory transfer with relative path preservation
- **Network fallback**: LAN peer discovery + manual IP entry when USB unavailable
- All parameters configurable via flags or environment variables — nothing hardcoded
- Single static binary for each platform (~2.3 MB, no runtime dependencies)

## Quickstart

### Linux side (USB gadget / sender)

```bash
# 1. Enable USB gadget mode (requires root, run once per boot)
sudo ./scripts/linux/setup_gadget.sh

# 2. Run filetrans as sender
sudo ./filetrans_linux_amd64 --role=sender file1.zip folder/
```

### Windows side (receiver)

```powershell
# 1. (Optional) verify RNDIS driver and IP assignment
.\scripts\windows\check_rndis.ps1

# 2. Run filetrans as receiver
.\filetrans_windows_amd64.exe --role=receiver --download-dir=C:\Downloads\filetrans
```

Both sides auto-detect the USB interface. If neither `--role` flag is set, each side prompts interactively.

## Download

Pre-built binaries are available on the [Releases](../../releases) page:

| File | Platform |
|------|----------|
| `filetrans_windows_amd64.exe` | Windows 10/11 x64 |
| `filetrans_linux_amd64` | Linux x86-64 |
| `filetrans_linux_arm64` | Linux ARM64 (Raspberry Pi 4+) |

`checksums.txt` contains SHA-256 hashes for every binary.

## Usage

```
filetrans [flags] [files...]

Flags:
  --role          Role: auto | sender | receiver  (default: auto — prompts)
  --peer          Peer IP, skips USB detection entirely
  --no-usb        Skip USB, go straight to network scan / manual IP
  --linux-ip      IP for the Linux side  (default: 192.168.7.1, env: FILETRANS_LINUX_IP)
  --windows-ip    IP for the Windows side (default: 192.168.7.2, env: FILETRANS_WINDOWS_IP)
  --subnet        Subnet prefix length   (default: 24,  env: FILETRANS_SUBNET)
  --port          WebSocket port          (default: 7070, env: FILETRANS_PORT)
  --chunk-size    Transfer chunk in bytes (default: 1048576, env: FILETRANS_CHUNK_SIZE)
  --download-dir  Receive directory       (default: ~/Downloads/filetrans, env: FILETRANS_DOWNLOAD_DIR)
  --json-logs     Emit structured JSON log lines
  --log-level     debug | info | warn | error  (default: info)
  --version       Print version and exit
```

### Network (non-USB) mode

```bash
# Sender — specify receiver's IP directly
filetrans --peer=192.168.1.42 --role=sender bigfile.iso

# Receiver
filetrans --peer=192.168.1.100 --role=receiver

# Auto-scan LAN (both sides must be on the same subnet)
filetrans --no-usb
```

### Environment variables

All flags have an `FILETRANS_*` equivalent:

```bash
export FILETRANS_PORT=8080
export FILETRANS_LINUX_IP=10.0.0.1
export FILETRANS_WINDOWS_IP=10.0.0.2
export FILETRANS_DOWNLOAD_DIR=/mnt/data/transfers
```

## Hardware requirements

| Scenario | Works? | Notes |
|----------|--------|-------|
| Linux ↔ Windows via USB-C | ✅ | Linux uses `g_ether` gadget mode |
| Windows ↔ Windows via USB-C | ❌ | No USB gadget support on Windows |
| Linux ↔ Linux via USB-C | ⚠️ | One Linux must support gadget mode (`/sys/class/udc/` non-empty) |
| Any two machines on LAN | ✅ | Use `--no-usb` mode |

### Check gadget support (Linux)

```bash
ls /sys/class/udc/
# If empty → your USB-C port is host-only → use --no-usb / LAN mode
```

### Windows RNDIS driver

Windows 10/11 auto-installs the RNDIS driver when it detects the Linux gadget.
If the adapter doesn't appear: Device Manager → Unknown Device → Update Driver →
Browse → Let me pick → Network Adapters → Microsoft → "Remote NDIS Compatible Device".

## Building from source

```bash
git clone https://github.com/YOUR_USERNAME/filetrans
cd filetrans
go mod download

# Build for current platform
make dev

# Cross-compile all targets
make all

# Specific targets
make windows     # dist/filetrans_windows_amd64.exe
make linux       # dist/filetrans_linux_amd64
make linux-arm   # dist/filetrans_linux_arm64
```

Requires Go 1.22+.

## Architecture

```
filetrans/
├── cmd/filetrans/          Entry point, CLI flags, session orchestration
├── backend/
│   ├── config/             Config struct: flags → env → defaults
│   ├── detector/           USB interface detection (netlink / polling)
│   ├── netconfig/          Static IP assignment (ip addr / netsh)
│   ├── handshake/          WebSocket server+client, role negotiation
│   ├── protocol/           Wire message types (JSON control + binary data)
│   ├── transfer/           Chunked sender and receiver, SHA-256, resume
│   ├── fallback/           LAN peer discovery (concurrent TCP scan)
│   ├── logger/             Structured JSON event logger
│   └── ui/                 Terminal prompts, progress bar
└── scripts/
    ├── linux/setup_gadget.sh       USB gadget (configfs) setup
    └── windows/check_rndis.ps1     RNDIS adapter check + IP assignment
```

### Transfer protocol

```
Sender (client)                    Receiver (server)
───────────────                    ─────────────────
HELLO {role: sender}        ──►
                            ◄──    ROLE_OK {peer_role: sender}
FILE_OFFER {name,size,...}  ──►
                            ◄──    FILE_ACCEPT {resume_from: 0}
CHUNK_HEADER {index, size}  ──►
<binary chunk data>         ──►
                            ◄──    CHUNK_ACK {index}
... repeat for each chunk ...
COMPLETE {sha256}           ──►
                            ◄──    COMPLETE_ACK {ok: true}
SESSION_DONE                ──►
```

## Contributing

Pull requests welcome. Please open an issue first for larger changes.

## License

MIT — see [LICENSE](LICENSE).
