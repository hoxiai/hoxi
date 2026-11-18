# Hoxi Remote (Open-Source Mobile Edition)

Lightweight, zero-account Flutter mobile controller (Android / iOS / macOS) for **Hoxi Desktop (`Code` mode)** and self-hosted **[GoPanel](https://github.com/hoxiai/gopanel)** nodes.

## Dual-Edition Packaging (`Hoxi` vs `Hoxi Remote`)

Hoxi ships two distinct mobile packages with isolated bundle identifiers so both can coexist side-by-side on the same phone without signature or secure-storage collisions:

- **`Hoxi Remote` (`ai.hoxi.remote`, this open-source `mobile/` package)**: Zero-account, zero-telemetry remote controller focused purely on LAN/Cloudflare/GoPanel E2EE pairing, live coding sessions, PreToolUse approvals, Git diff review, and project terminals. Pre-built signed Android APKs (`Hoxi-Remote-<version>-android.apk`) are attached to every [GitHub Release](https://github.com/hoxiai/hoxi/releases) (Obtainium auto-update compatible).
- **`Hoxi` (`ai.hoxi.app`, official full edition at [hoxi.ai](https://hoxi.ai))**: Full mobile AI workspace distributed via `hoxi.ai` / App Store / TestFlight / Google Play, adding cloud connection sync, continuous voice calls, iOS APNs approval push notifications, and ecosystem integrations.

## Capabilities

- **Zero-Trust LAN & Relay Pairing**: Scan the QR code in Hoxi Desktop (`Settings -> Remote Access`) or discover LAN nodes on `:5417`, with automatic 1-RTT X25519 zero-knowledge key exchange and `hoxi-e2ee-v1` directional AES-256-GCM encryption.
- **Live Multi-Agent Coding Sessions**: Stream native agent turns over SSE with `after_seq` catch-up, attach workspace `@files` and skills, inspect subagents, and queue follow-up instructions.
- **Remote Approvals, Diff Review & Terminal**: Approve or reject gated tool executions, inspect unified Git diffs, and attach to project terminals from your phone.

## Quick Start & Local Build

```bash
cd mobile
flutter pub get
flutter gen-l10n
flutter analyze
flutter test
flutter run

# Build Android APK from source
flutter build apk --release
```

## Architecture & Boundary Gates

```bash
python3 scripts/check_source_lines.py
python3 scripts/check_network_boundary.py
python3 scripts/check_feature_boundaries.py
```
