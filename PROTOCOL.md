# GauravTransfer Protocol (GTP/1.0)

A binary-native, local-first file transfer protocol for direct links.
No cloud. No Wi-Fi required. No WebRTC complexity.

---

## Design goals

| Goal | How |
|------|-----|
| Zero configuration | mDNS peer discovery on LAN + USB auto-detect |
| Maximum throughput | Windowed chunk pipeline (N chunks in-flight) |
| Integrity | CRC32C per-chunk + SHA-256 per-file |
| Resume | Receiver tracks `.gtpart` partial files |
| No cloud dependency | Pure TCP, works over USB-C Ethernet, LAN, or any routable link |
| Cross-platform | Pure Go, no CGO, no OS-specific deps for core protocol |

---

## Comparison to WebRTC

```
WebRTC (Google)          GauravTransfer Protocol
─────────────────────    ──────────────────────────────────
ICE / STUN / TURN   →   mDNS multicast + USB interface detector
SDP negotiation     →   GTP HELLO / HELLO_ACK (one round-trip)
DTLS                →   Optional AES-256-GCM (local trust model)
SCTP                →   TCP (already reliable on local links)
RTP streams         →   GTP DATA frames (binary, zero-copy)
RTCP feedback       →   GTP DATA_ACK (per-chunk, windowed)
Browser JS API      →   Native Go library (any OS, any app)
```

---

## Wire format

Every GTP frame:

```
┌─────────────────────────────────────────────────────────┐
│ Byte 0-3 │ Byte 4  │ Bytes 5-8      │ Bytes 9+          │
│ "GTP1"   │ Type    │ PayloadLen LE  │ Payload           │
│ (magic)  │ (uint8) │ (uint32)       │ JSON or raw bytes │
└─────────────────────────────────────────────────────────┘
```

Total overhead: **9 bytes per frame**. No base64. No HTTP headers.

---

## DATA frame payload layout

DATA frames embed a JSON header + raw chunk bytes in one frame:

```
[4 bytes: JSON header length, LE uint32]
[N bytes: JSON header (DataMsg)]
[M bytes: raw chunk bytes]
```

The JSON header includes `crc32` (CRC32C of the chunk bytes) for fast
corruption detection before writing to disk.

---

## Session sequence

```
Sender                              Receiver
──────                              ────────
TCP connect ─────────────────────►
HELLO ───────────────────────────►
                                   HELLO_ACK ◄───────────
FILE_OFFER ──────────────────────►
                                   FILE_ACCEPT ◄──────────  (with resume_chunk)
DATA (chunk 0) ─────────────────►
                                   DATA_ACK ◄─────────────
DATA (chunk 1) ─────────────────►  (windowed: N in-flight)
DATA (chunk 2) ─────────────────►
                                   DATA_ACK ◄─────────────
                                   DATA_ACK ◄─────────────
...
COMPLETE (sha256) ───────────────►
                                   COMPLETE_ACK ◄──────────
FILE_OFFER (next file) ──────────► (repeat)
SESSION_DONE ────────────────────►
```

---

## mDNS peer discovery

GTP announces on `224.0.0.251:5354` (standard mDNS port) with a minimal
UDP datagram — no full DNS implementation needed:

```
GTP1 1.0 <port> <deviceID>
```

Peers hear this, extract the IP from the UDP source address + port from
the payload, and can connect immediately.

This works on:
- USB-C Ethernet (RNDIS / CDC-ECM)
- LAN (Ethernet / Wi-Fi)
- Bluetooth PAN
- Any routable local link

---

## Window size tuning

The sender proposes a window size in HELLO (`window` field).
The receiver confirms or reduces it in HELLO_ACK.

Default: **8 chunks in-flight**. Each chunk is 128 KiB–4 MiB.
At 8 × 4 MiB = 32 MiB in-flight, USB 3.0 links (~400 MB/s) stay saturated.

On high-latency links (Wi-Fi over 802.11ac at distance), a larger window
prevents stalls. The window can be tuned via `--chunk-window` flag.

---

## Frame type reference

| Value | Name          | Payload |
|-------|---------------|---------|
| 1     | HELLO         | HelloMsg JSON |
| 2     | HELLO_ACK     | HelloAckMsg JSON |
| 3     | FILE_OFFER    | FileOfferMsg JSON |
| 4     | FILE_ACCEPT   | FileAcceptMsg JSON |
| 5     | FILE_REJECT   | FileRejectMsg JSON |
| 6     | DATA          | [4B JSON len][JSON DataMsg][chunk bytes] |
| 7     | DATA_ACK      | DataAckMsg JSON |
| 8     | COMPLETE      | CompleteMsg JSON |
| 9     | COMPLETE_ACK  | CompleteAckMsg JSON |
| 10    | SESSION_DONE  | SessionDoneMsg JSON |
| 11    | ERROR         | ErrorMsg JSON |
| 12    | PING          | empty |
| 13    | PONG          | empty |

---

## Future: GTP/2.0 roadmap

- AES-256-GCM encryption (key exchange in HELLO via ECDH)
- Parallel multi-stream (multiple files simultaneously)
- BLAKE3 checksums (faster than SHA-256, pure Go impl)
- Compression hints (sender marks compressible files)
- Bandwidth throttling (max_bps field in HELLO)
