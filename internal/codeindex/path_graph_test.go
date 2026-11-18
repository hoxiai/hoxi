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
