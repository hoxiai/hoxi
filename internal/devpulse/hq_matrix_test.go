package devpulse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectHQMatrixSummaryParsesDashboardAndOpsPulse(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".hq"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".hq", "topology.env"), []byte("export HOXI_SITE_DOMAIN=hoxi.ai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dash := "# Hoxi 全局无人值守总控仪表盘 (DASHBOARD.md)\n" +
		"> **自动生成时间**：`2026-11-08 14:00:00` | **刷新看板**：`./.hq/sync-dashboard.sh`\n\n" +
		"## 🏃 活跃并发沙箱池\n" +
		"- **`hoxi/.hoxi/worktrees/ses_1`** (Hoxi 桌面端沙箱)\n" +
		"- **`ainode/.hoxi/worktrees/ses_2`** (Hoxi 桌面端沙箱)\n\n" +
		"## 📊 全矩阵项目状态总览\n" +
		"| 项目 | 母体/分类 | 状态 (Status) | 待办/已完成 | Dirty Git 仓 | 最近一次交接摘要 | 更新时间 |\n" +
		"| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n" +
		"| [**ainode**](ainode/.ai/STATE.md) | `go-service` | `IDLE` | 0 | 0/2 | ok | 2026-11-08 |\n" +
		"| [**hoxi**](hoxi/.ai/STATE.md) | `go-wails` | `READY` | 0 | 1/2 | ok | 2026-11-08 |\n" +
		"| [**qingpu-ai**](qingpu-ai/.ai/STATE.md) | `node-ts` | `BLOCKED` | 1 | 0/1 | blocked | 2026-11-08 |\n"
	if err := os.WriteFile(filepath.Join(ws, "DASHBOARD.md"), []byte(dash), 0o644); err != nil {
		t.Fatal(err)
	}
	ops := "# Hoxi 全矩阵智能运营日报 (OPS_PULSE.md)\n" +
		"> **巡检时间**：`2026-11-08 14:05:00`\n\n" +
		"## 1. 🌐 核心生产站点与 API 实时探活\n" +
		"| ✅ **Hoxi 官网** | `https://hoxi.ai/` | `HTTP 200` | `0.12s` |\n" +
		"| 🚨 **轻铺 AI 官网** | `https://qingpu.ai/` | `HTTP 502` | `1.02s` |\n"
	if err := os.WriteFile(filepath.Join(ws, "OPS_PULSE.md"), []byte(ops), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(&stubPulseSource{candidates: []string{filepath.Join(ws, "hoxi")}})
	got := svc.CollectHQMatrixSummary(nil)
	if got == nil || !got.Enabled {
		t.Fatalf("expected enabled HQMatrixSummary, got %+v", got)
	}
	if got.TotalProjects != 3 || got.IdleProjects != 1 || got.ReadyProjects != 1 || got.BlockedProjects != 1 {
		t.Fatalf("unexpected project counts: %+v", got)
	}
	if len(got.BlockedNames) != 1 || got.BlockedNames[0] != "qingpu-ai" {
		t.Fatalf("unexpected blocked names: %+v", got.BlockedNames)
	}
	if got.ActiveSandboxes != 2 || got.OpsHealthyCount != 1 || got.OpsAnomalyCount != 1 {
		t.Fatalf("unexpected sandbox/ops counts: %+v", got)
	}
}
