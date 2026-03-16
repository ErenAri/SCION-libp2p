# Wire Protocol Specification

This document specifies the four wire protocols used by pathaware-libp2p. All protocols run over libp2p streams (multiplexed over TCP or relay connections). Multi-byte integers use big-endian byte order.

## Protocol IDs

```
  /pathaware-libp2p/ping/1.0.0           Echo-based latency measurement
  /pathaware-libp2p/probe/1.0.0          Path quality probe with hop counting
  /pathaware-libp2p/block/1.0.0          Block fetch (request/response)
  /pathaware-libp2p/block-push/1.0.0     Push-based block replication
```

## 1. Ping Protocol

Purpose: Measure round-trip time to a peer.

### Message Format

```
  Request:   [8 bytes]  timestamp_ns   uint64, sender's time in nanoseconds

  Response:  [8 bytes]  timestamp_ns   echoed from request
```

### Sequence

```
  Sender                              Receiver
    |                                    |
    |-- stream open -------------------> |
    |-- 8B timestamp ------------------> |
    |                                    |-- read 8B
    |<-- 8B echo ----------------------- |
    |                                    |-- close stream
    |-- close stream                     |
```

RTT is computed as `time.Since(sent)` on the sender side. The timestamp payload is not used for RTT calculation; it serves as a correlation token and provides clock debugging information.

### Multi-Ping

`SendPings(ctx, host, target, count)` sends `count` sequential pings with a 500ms delay between each, returning all results.

## 2. Probe Protocol

Purpose: Measure RTT, hop count, jitter, and throughput estimate for a network path (direct or via relay).

### Message Format

Total payload: 53 bytes.

```
  Offset  Size  Field              Description
  ------  ----  -----              -----------
  0       8     timestamp_ns       uint64, sender's time (ns)
  8       4     path_id            uint32, identifies which path
  12      1     hop_count          uint8, incremented by each hop
  13      4     throughput_est     uint32, reserved for throughput (0 from sender)
  17      4     jitter_us          uint32, reserved for jitter (0 from sender)
  21      32    nonce              random bytes for integrity verification
```

### Sequence

```
  Sender                              Receiver (or relay hop)
    |                                    |
    |-- stream open -------------------> |
    |-- 53B payload -------------------> |
    |                                    |-- read 53B
    |                                    |-- increment hop_count (byte 12)
    |<-- 53B echo (hop incremented) ---- |
    |                                    |
    |-- verify:                          |
    |     nonce[21..52] matches          |
    |     path_id matches                |
    |   RTT = time.Since(sent)           |
    |   hopCount = echo[12]              |
```

### Relay Path Probing

For relay paths, the probe stream is opened to the first relay peer (not the target). The relay forwards the stream to the target, which echoes the payload back. Each relay hop increments the hop count, so the returned value reflects the actual number of hops traversed.

```
  Sender --> Relay A --> Target
                         hop_count: 0 -> 1 (relay) -> 2 (target)
                         returned hop_count = 2
```

### Parallel Probing

`SendProbes(ctx, host, paths)` probes all paths concurrently and returns results indexed by position. This is used by the path manager to probe direct and relay paths simultaneously.

## 3. Block Transfer Protocol

Purpose: Request and retrieve a content block by CID.

### Request Format

```
  Offset  Size    Field      Description
  ------  ----    -----      -----------
  0       2       cid_len    uint16, length of CID string
  2       cid_len cid        CID string (hex-encoded SHA-256)
```

After writing the CID, the sender calls `CloseWrite()` to signal end of request.

### Response Format (Found)

```
  Offset  Size      Field      Description
  ------  ----      -----      -----------
  0       1         status     0x01 = found
  1       4         data_len   uint32, length of block data
  5       data_len  data       raw block bytes
```

### Response Format (Not Found)

```
  Offset  Size      Field      Description
  ------  ----      -----      -----------
  0       1         status     0x00 = not found
  1       2         err_len    uint16, length of error message
  3       err_len   err_msg    human-readable error string
```

### Sequence

```
  Fetcher                              Provider
    |                                    |
    |-- stream open -------------------> |
    |-- [2B cidLen][cidBytes] ---------> |
    |-- CloseWrite                       |
    |                                    |-- lookup: cache -> disk
    |                                    |
    |   If found:                        |
    |<-- [0x01][4B len][data] ---------- |
    |                                    |-- cache block (NDN-style)
    |   If not found:                    |
    |<-- [0x00][2B len][err] ----------- |
    |                                    |
    |-- verify CID matches data          |
    |-- close stream                     |
```

### CID Verification

After receiving block data, the fetcher computes `SHA-256(data)` and verifies it matches the requested CID. If it does not match, the block is rejected. This provides end-to-end integrity regardless of how many relay hops the data traversed.

### Relay Caching (NDN-Style)

When the provider serves a block, it also puts the block into its in-memory LRU cache. If a relay node is forwarding the block transfer stream, the relay's block transfer handler will also cache the block. This means subsequent requests for the same block from other peers can be served from the relay's cache without going all the way to the original provider.

```
  Request 1:  Fetcher A --> Relay R --> Provider P
                                        (P has block on disk)
              Fetcher A <-- Relay R <-- Provider P
                            (R caches block)

  Request 2:  Fetcher B --> Relay R
                            (R has block in cache)
              Fetcher B <-- Relay R
                            (no roundtrip to P)
```

## 4. Block Push Protocol

Purpose: Proactively replicate blocks to peers for fault tolerance.

### Request Format

```
  Offset  Size      Field      Description
  ------  ----      -----      -----------
  0       2         cid_len    uint16, length of CID string
  2       cid_len   cid        CID string
  cid+2   4         data_len   uint32, length of block data
  cid+6   data_len  data       raw block bytes
```

After writing, the sender calls `CloseWrite()`.

### Response Format

```
  Offset  Size  Field      Description
  ------  ----  -----      -----------
  0       1     ack        0x01 = accepted, 0x00 = rejected
```

### Sequence

```
  Replicator                           Receiver
    |                                    |
    |-- stream open -------------------> |
    |-- [2B cidLen][cidBytes] ---------> |
    |-- [4B dataLen][data] ------------> |
    |-- CloseWrite                       |
    |                                    |-- verify: SHA-256(data) == cid
    |                                    |-- store to disk
    |                                    |-- cache in memory
    |<-- [0x01 ack] -------------------- |
    |                                    |
```

### Safety Limits

- Maximum CID length: 1024 bytes
- Maximum block data: 16 MB
- CID must match SHA-256 of data (rejected otherwise)

## Error Handling

All protocols handle errors by closing the stream. Common error cases:

- Stream open failure: peer unreachable or protocol not supported
- Read timeout: context deadline exceeded
- Nonce mismatch (probe): corrupted or tampered response
- CID mismatch (block): corrupted data, returns error to caller
- I/O errors: logged at debug level, stream closed

No retry logic is built into the protocol layer. Retries are handled at the application layer (e.g., trying the next provider in the sorted list).
