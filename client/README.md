# Hoxi Mobile (`client/`)

[English](README.md) | [简体中文](README.zh-CN.md)

**Hoxi Mobile** is the iOS & Android command center for Hoxi Desktop and GoPanel (`gopanel_v2`) nodes.

It is intentionally **not** a cramped mobile code editor or remote desktop. It solves one problem: **letting you step away from your desk while long-running AI tasks execute, without losing visibility or control.**

---

## What It Solves

1. **Unattended Agent Anxiety**: Approving a single destructive command (`rm`, `git push --force`) or answering an agent's clarifying question shouldn't require sprinting back to your laptop. The **Attention Hub** surfaces pending permission gates for 1-tap `Once` / `Always` / `Reject` responses.
2. **Mid-Stream Course Correction**: Watch live SSE token streams and tool calls on the go. If an agent heads down the wrong path, send a **Steer** instruction or abort the turn immediately.
3. **Mobile Diff & Artifact Review**: Inspect session Git worktree diffs, changed files, 8-dimension memory summaries, and live preview links before merging.
4. **Dual-Node Native Protocol (`hoxi` + `gopanel`)**: Automatically detects and connects to both **Hoxi Desktop/Daemon nodes** (`/api/hoxi/*`, QR pairing or local discovery) and **GoPanel server nodes** (`/api/v1/*` & `/api/code/*`, container/website ops + remote AI workspaces).

---

## Tech Stack & Verification

- **Stack**: Flutter + Riverpod + GoRouter + Dio
- **Verify**:
  ```bash
  flutter analyze
  flutter test
  ```
