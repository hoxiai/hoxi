package codeindex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFilePathInputsNeverTruncateExtensionAndResolveFileGraph(t *testing.T) {
	dir := fixtureWorkspace(t)
	writeFile(t, filepath.Join(dir, "billing", "ledger.go"), strings.Join([]string{
		"package billing",
		"",
		"func RecordInvoice(amount int) bool {",
		"\treturn CommitInvoiceRow(amount)",
		"}",
		"",
		"func CommitInvoiceRow(amount int) bool {",
		"\treturn amount > 0",
		"}",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(dir, "checkout", "handler.go"), strings.Join([]string{
		"package checkout",
		"",
		"import \"demo/billing\"",
		"",
		"func RunCheckoutFlow(amount int) bool {",
		"\treturn billing.CommitInvoiceRow(amount)",
		"}",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(dir, "entry", "boot.go"), strings.Join([]string{
		"package entry",
		"",
		"import \"demo/checkout\"",
		"",
		"func BootApp() bool {",
		"\treturn checkout.RunCheckoutFlow(100)",
		"}",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(dir, "billing", "ledger_test.go"), strings.Join([]string{
		"package billing",
		"",
		"import \"testing\"",
		"",
		"func TestRecordInvoice(t *testing.T) {",
		"\t_ = RecordInvoice(10)",
		"}",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(dir, "ui", "nav_shell.dart"), strings.Join([]string{
		"void openBoard(dynamic router) {",
		"  router.go('home');",
		"}",
	}, "\n")+"\n")
	writeFile(t, filepath.Join(dir, "worker", "group.go"), "package worker\n\nfunc Go(fn func()) { fn() }\n")
	writeFile(t, filepath.Join(dir, "worker", "runner.go"), "package worker\n\nfunc Dispatch() { Go(func() {}) }\n")

	svc := NewService(t.TempDir())

	impact, err := svc.Impact(dir, "billing/ledger.go", "", 2, 12)
	if err != nil {
		t.Fatalf("Impact on file path failed: %v", err)
	}
	if impact.Path != "billing/ledger.go" {
		t.Fatalf("expected Path=billing/ledger.go, got %+v", impact)
	}
	for _, c := range impact.DirectCallers {
		if strings.HasSuffix(c.Path, ".dart") {
			t.Fatalf("file-path impact falsely matched Dart router.go call: %+v", c)
		}
	}
	if len(impact.DirectCallers) != 1 || impact.DirectCallers[0].Path != "checkout/handler.go" || impact.DirectCallers[0].Symbol != "RunCheckoutFlow" {
		t.Fatalf("expected RunCheckoutFlow as direct caller of ledger.go, got: %+v", impact.DirectCallers)
	}
	if len(impact.TransitiveCallers) != 1 || impact.TransitiveCallers[0].Path != "entry/boot.go" || impact.TransitiveCallers[0].Symbol != "BootApp" {
		t.Fatalf("expected BootApp as transitive caller of ledger.go, got: %+v", impact.TransitiveCallers)
	}
	if len(impact.Tests) != 1 || impact.Tests[0].Path != "billing/ledger_test.go" {
		t.Fatalf("expected ledger_test.go in file impact Tests, got: %+v", impact.Tests)
	}

	callers, err := svc.Callers(dir, "billing/ledger.go", "", 12)
	if err != nil || len(callers.Callers) != 1 || callers.Callers[0].Symbol != "RunCheckoutFlow" {
		t.Fatalf("unexpected Callers on file path: %+v (%v)", callers, err)
	}
	callees, err := svc.Callees(dir, "checkout/handler.go", "", 12)
	if err != nil || len(callees.Callees) != 1 || callees.Callees[0].Symbol != "CommitInvoiceRow" {
		t.Fatalf("unexpected Callees on file path: %+v (%v)", callees, err)
	}
	inspected, err := svc.Inspect(dir, "", "billing/ledger.go", 0, 0, 2)
	if err != nil || inspected.Path != "billing/ledger.go" {
		t.Fatalf("unexpected Inspect on file path in symbol: %+v (%v)", inspected, err)
	}
	ctxRes, err := svc.Context(dir, "", "billing/ledger.go", "", 2, 12)
	if err != nil || len(ctxRes.Callers) != 1 || ctxRes.Callers[0].Symbol != "RunCheckoutFlow" {
		t.Fatalf("unexpected Context on file path: %+v (%v)", ctxRes, err)
	}

	goSymCallers, err := svc.Callers(dir, "worker.Go", "", 10)
	if err != nil || len(goSymCallers.Callers) != 1 || goSymCallers.Callers[0].Symbol != "Dispatch" {
		t.Fatalf("expected worker.Go qualified symbol to resolve Dispatch caller, got: %+v (%v)", goSymCallers, err)
	}

	for _, bad := range []string{"billing/missing.go", "nonexistent.ts", "logo.png"} {
		if _, err := svc.Callers(dir, bad, "", 10); err == nil {
			t.Fatalf("expected Callers(%q) to fail", bad)
		}
		if _, err := svc.Impact(dir, bad, "", 2, 10); err == nil {
			t.Fatalf("expected Impact(%q) to fail", bad)
		}
	}
}

func TestCallerTotalsAreIndependentOfDisplayLimit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "target.go"), "package demo\nfunc Alpha() {}\nfunc Beta() {}\n")
	writeFile(t, filepath.Join(dir, "use.go"), "package demo\nfunc First() {\n Alpha(); Beta()\n}\nfunc Second() {\n Alpha()\n}\n")
	writeFile(t, filepath.Join(dir, "entry.go"), "package demo\nfunc Entry() {\n First()\n Second()\n}\n")
	writeFile(t, filepath.Join(dir, "use_test.go"), "package demo\nfunc TestCalls() {\n Alpha(); Beta()\n Alpha()\n Second()\n Entry()\n}\n")
	svc := NewService(t.TempDir())

	for _, symbol := range []string{"Alpha", "target.go"} {
		for _, limit := range []int{1, 2, 10} {
			callers, err := svc.Callers(dir, symbol, "", limit)
			if err != nil || callers.TotalCallers != 2 || callers.TotalTests != 2 ||
				len(callers.Callers) != min(limit, 2) || len(callers.Tests) != min(limit, 2) {
				t.Fatalf("Callers(%s, %d) = %+v, %v", symbol, limit, callers, err)
			}
			context, err := svc.Context(dir, "", symbol, "", 0, limit)
			if err != nil || context.TotalCallers != 2 || context.TotalTransitiveCallers != 2 || context.TotalTests != 4 ||
				len(context.Callers) != min(limit, 2) || len(context.TransitiveCallers) != min(limit, 2) || len(context.Tests) != min(limit, 4) {
				t.Fatalf("Context(%s, %d) = %+v, %v", symbol, limit, context, err)
			}
		}
	}
	context, err := svc.Context(dir, "entry.go", "Entry", "", 0, 1)
	if err != nil || context.TotalCallers != 0 || context.TotalCallees != 2 || len(context.Callees) != 1 {
		t.Fatalf("Entry context = %+v, %v", context, err)
	}
}

func TestCallerTotalsBeyondMaximumDisplayLimit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "target.go"), "package demo\nfunc Target() {}\n")
	writeFile(t, filepath.Join(dir, "use.go"), "package demo\nfunc Use() {\n"+strings.Repeat(" Target()\n", 45)+"}\n")
	svc := NewService(t.TempDir())
	for _, tc := range []struct{ limit, shown int }{{0, 16}, {40, 40}} {
		got, err := svc.Callers(dir, "Target", "", tc.limit)
		if err != nil || len(got.Callers) != tc.shown || got.TotalCallers != 45 || got.TotalTests != 0 {
			t.Fatalf("limit %d: got %d of %d callers and %d tests, err=%v", tc.limit, len(got.Callers), got.TotalCallers, got.TotalTests, err)
		}
	}
	got, err := svc.Context(dir, "", "Target", "", 0, 0)
	if err != nil || len(got.Callers) != 12 || got.TotalCallers != 45 {
		t.Fatalf("default context: got %d of %d callers, err=%v", len(got.Callers), got.TotalCallers, err)
	}
}
