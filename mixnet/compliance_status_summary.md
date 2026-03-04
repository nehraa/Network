# Mixnet Compliance Status Summary Report (March 2026)

## Executive Summary

This report provides a status update on the issues identified in the compliance reports from March 3rd, 2026. While the codebase has transitioned from a placeholder state to a functional prototype with proper resource management and streaming capabilities, significant **security and privacy vulnerabilities** remain.

| Category | Total Issues | Fixed | Unfixed | % Fixed |
|----------|--------------|-------|---------|---------|
| **Critical (Security/Privacy)** | 11 | 3 | 8 | 27% |
| **High (Functional)** | 9 | 6 | 3 | 66% |
| **Medium/Low (Quality)** | 5 | 2 | 3 | 40% |
| **Total** | **25** | **11** | **14** | **44%** |

---

## 1. Fixed Issues ✅

The following architectural and functional issues have been resolved:

| ID | Issue | Status | Explanation |
|----|-------|--------|-------------|
| REQ 8.4 | Encryption Order | Fixed | Onion encryption now correctly wraps data from the inside out (exit to entry). |
| REQ 8.2 | Transmission Timeout | Fixed | 30s deadlines are now applied to shard transmission streams. |
| REQ 20.1 | Max Circuits | Fixed | Relay handler now correctly enforces the maximum concurrent circuit limit. |
| REQ 20.2 | Bandwidth Limits | Fixed | Implemented `rateLimitedWriter` to enforce bandwidth caps per circuit. |
| REQ 20.4 | No Backpressure | Fixed | Switched to `io.Copy` for direct streaming, preventing memory exhaustion. |
| REQ 11.2 | Transport Agnosticism | Fixed | RTT measurement now uses the transport-agnostic libp2p ping protocol. |
| REQ 10.3 | Stale Relay Pool | Fixed | Recovery now triggers fresh relay discovery instead of reusing failed nodes. |
| REQ 10.4 | Peer Re-use in Rebuild | Fixed | `RebuildCircuit` now explicitly excludes peers from the failed circuit. |
| REQ 16.3 | Secure Key Erasure | Fixed | Implemented `SecureEraseBytes` and incorporated it into the `Send` pipeline. |
| REQ 3.1 | Shard Padding Bug | Fixed | Added 8-byte length prefix to ensure correct reconstruction of padded data. |
| REQ 9.4 | Compression Detection | Fixed | Prepend algorithm ID so the receiver can detect the compression method. |

---

## 2. Unfixed Issues ❌

These critical issues persist and must be addressed before the mixnet is considered secure or compliant:

### **Critical Security & Privacy**
*   **Broken Privacy Model (REQ 14.1):** Every onion layer currently uses the **final destination ID** as the "next hop". This means every relay in the circuit knows the final recipient, completely defeating the mixnet's purpose.
*   **Relay Handler Not Registered (REQ 7.1):** The `relay.Handler` code exists but is **never registered** with the libp2p host in `NewMixnet`. Nodes cannot act as relays.
*   **Keys Not Transmitted (REQ 9.1):** Ephemeral session keys are generated but never sent to the destination node. Decryption will fail at the destination.
*   **Wrong Crypto Protocol (REQ 16.1):** Still uses raw `chacha20poly1305` instead of the required **Noise (XX handshake)** protocol.
*   **No Peer Authentication:** Circuit construction lacks authentication, leaving the system vulnerable to MITM attacks.

### **Functional & Stability**
*   **Broken Session Management (REQ 9.2):** All transmissions use the hardcoded session ID `"default"`. Concurrent messages will corrupt each other.
*   **No Active Monitoring (REQ 10.1):** Failure detection relies on a state flag; there is no heartbeat mechanism to detect silent circuit failures.
*   **Fake Protocol Verification (REQ 12.1):** `CanUpgrade` blindly returns `true` without checking remote peer capabilities.
*   **Missing Shard Validation (REQ 9.1):** Shard parsing ignores the CRC32 checksum field.
*   **Reconstruction Timeout Leak (REQ 9.6):** No mechanism exists to clean up incomplete sessions from memory after a timeout.

---

## Conclusion

The "plumbing" of the mixnet (limits, direct streaming, transport agnosticism) is now in good shape. However, the "core architecture" (Onion privacy, Handshakes, Key Delivery) is still fundamentally broken. The current implementation functions more like a multi-path proxy than a secure mixnet.

**Immediate Recommendations:**
1. Fix onion headers in `upgrader.go:Send()` to only include the *next hop*.
2. Call `SetStreamHandler` for the relay protocol in `NewMixnet`.
3. Implement a proper handshake for ephemeral key delivery.
