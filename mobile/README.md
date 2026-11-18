# Hoxi Mobile (Open-Source Edition)

Lightweight Flutter mobile controller (iOS / Android / macOS) for **Hoxi Desktop (`Code` mode)**.

## Capabilities

- **Zero-Trust LAN & Relay Pairing**: Scan the QR code in Hoxi Desktop (`Settings -> Remote Access`) or discover LAN nodes on `:5417`, with automatic 1-RTT X25519 zero-knowledge key exchange and `hoxi-e2ee-v1` directional AES-256-GCM encryption.
- **Live Multi-Agent Coding Sessions**: Stream native agent turns over SSE with `after_seq` catch-up, attach workspace `@files` and skills, inspect subagents, and queue follow-up instructions.
- **Remote Approvals, Diff Review & Terminal**: Approve or reject gated tool executions, inspect unified Git diffs, and attach to project terminals from your phone.

## Quick Start

```bash
cd mobile
flutter pub get
flutter gen-l10n
flutter analyze
flutter test
flutter run
```

## Architecture & Boundary Gates

```bash
python3 scripts/check_source_lines.py
python3 scripts/check_network_boundary.py
python3 scripts/check_feature_boundaries.py
```
