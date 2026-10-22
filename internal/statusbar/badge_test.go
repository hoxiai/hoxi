package statusbar

import "testing"

func TestDockBadgeLabel(t *testing.T) {
	cases := []struct {
		name    string
		summary Summary
		want    string
	}{
		{name: "available but idle", summary: Summary{Available: true}, want: ""},
		{name: "unavailable engine", summary: Summary{Available: false, Attention: 3}, want: ""},
		{name: "single pending approval", summary: Summary{Available: true, Attention: 1}, want: "1"},
		{name: "running only stays off the dock", summary: Summary{Available: true, Running: 4}, want: ""},
		{name: "ninety nine", summary: Summary{Available: true, Attention: 99}, want: "99"},
		{name: "overflow caps", summary: Summary{Available: true, Attention: 100}, want: "99+"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.summary.DockBadgeLabel(); got != tc.want {
				t.Fatalf("DockBadgeLabel(%+v) = %q, want %q", tc.summary, got, tc.want)
			}
		})
	}
}

func TestDockBadgeTracksPendingApprovals(t *testing.T) {
	tracker := NewTracker()
	if label := tracker.Snapshot().DockBadgeLabel(); label != "" {
		t.Fatalf("fresh tracker showed dock badge %q", label)
	}

	tracker.SetPendingApproval("per_1", "ses_1", true)
	tracker.SetPendingApproval("per_2", "ses_2", true)
	if label := tracker.Snapshot().DockBadgeLabel(); label != "2" {
		t.Fatalf("expected dock badge 2 after two approvals, got %q", label)
	}

	tracker.SetPendingApproval("per_1", "ses_1", false)
	if label := tracker.Snapshot().DockBadgeLabel(); label != "1" {
		t.Fatalf("expected dock badge 1 after one reply, got %q", label)
	}

	tracker.SetPendingApproval("per_2", "ses_2", false)
	if label := tracker.Snapshot().DockBadgeLabel(); label != "" {
		t.Fatalf("expected cleared dock badge, got %q", label)
	}
}
