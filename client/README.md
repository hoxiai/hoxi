<p align="center">
  <a href="https://hoxi.ai"><img src="../docs/assets/hoxi-banner.svg" alt="Hoxi" width="260" /></a>
</p>

<p align="center">
  <b>Hoxi Mobile (`client/`)</b> — Mobile AI Engineering Command Center &amp; <a href="https://github.com/hoxiai/gopanel">GoPanel</a> Server Manager for iOS &amp; Android.
</p>

<p align="center">
  <kbd><a href="README.md">English</a></kbd>
  <kbd><a href="README.zh-CN.md">中文(简体)</a></kbd>
</p>

---

**Hoxi Mobile** is a dual-purpose Flutter client designed for solo builders and engineering teams. Rather than cramming a desktop code editor onto a 6-inch screen, it focuses on the two jobs you actually need away from your desk:
1. **Commanding running AI coding sessions** on **Hoxi Desktop/Daemon** (`nodeType: 'hoxi'`) and **GoPanel Code** (`nodeType: 'gopanel'`).
2. **Managing production Linux servers** powered by [**GoPanel (`hoxiai/gopanel`)**](https://github.com/hoxiai/gopanel).

---

## Core Capabilities

### 1. Mobile AI Engineering Command Center (`ai_workspace` & `task_center`)
- **1-Tap High-Risk Approval (`Attention Hub`)**: When an agent triggers a PreToolUse safety gate (`rm`, `sudo`, `git push --force`) or asks a clarifying question, approve (`Once` / `Always`) or reject with feedback from your phone.
- **Live Stream & Mid-Stream Steer**: Watch real-time SSE token streams, reasoning blocks, and tool cards; inject **Steer** instructions mid-turn or abort runaway tasks.
- **Worktree Diff & Delivery Review**: Inspect session Git worktree diffs, changed files, 8-dimension memory summaries, and live preview links before merging.

### 2. Mobile Server Management for [GoPanel](https://github.com/hoxiai/gopanel) (`server`, `container`, `website`, `database`, `ssl`, `apps`)
When connected to a [GoPanel](https://github.com/hoxiai/gopanel) node (`/api/v1/*`), the app acts as a full pocket server ops console:
- **Server Telemetry (`server/`)**: Live CPU, memory, disk, and network traffic metrics.
- **Docker & Containers (`container/` & `apps/`)**: Inspect container states, view logs, start/stop/restart services, and manage one-click app deployments.
- **Websites & SSL (`website/` & `ssl/`)**: Manage Nginx sites, domains, reverse proxies, and SSL certificate issuance/renewal.
- **Databases (`database/`)**: Monitor and manage MySQL, PostgreSQL, and Redis instances on the go.

---

## Tech Stack & Verification

- **Stack**: Flutter + Riverpod + GoRouter + Dio
- **Verify**:
  ```bash
  flutter analyze
  flutter test
  ```
