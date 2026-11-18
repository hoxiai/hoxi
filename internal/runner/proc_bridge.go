package runner

import (
	"os"
	"os/exec"

	"github.com/hoxiai/hoxi/internal/runner/procgroup"
)

type (
	procSnapshot       = procgroup.Snapshot
	ownedProcessLedger = procgroup.OwnedLedger
	ownedProcessWatch  = procgroup.OwnedWatch
)

func newOwnedProcessLedger() *ownedProcessLedger          { return procgroup.NewOwnedLedger() }
func setProcessGroup(cmd *exec.Cmd)                       { procgroup.SetProcessGroup(cmd) }
func signalProcessGroup(p *os.Process, s os.Signal) error { return procgroup.SignalProcessGroup(p, s) }
func processGroupAlive(p *os.Process) bool                { return procgroup.ProcessGroupAlive(p) }
func selfProcessGroupID() int                             { return procgroup.SelfProcessGroupID() }
func descendantPIDs(pid int) []int                        { return procgroup.DescendantPIDs(pid) }
func killPIDs(pids []int)                                 { procgroup.KillPIDs(pids) }
func readProcessSnapshots() (map[int]procSnapshot, map[int][]int) {
	return procgroup.ReadProcessSnapshots()
}
func processAlive(pid int) bool { return procgroup.ProcessAlive(pid) }
func collectSubtreePIDs(root int, children map[int][]int) []int {
	return procgroup.CollectSubtreePIDs(root, children)
}
func isAgentRuntimeProcessArgs(args string) bool { return procgroup.IsAgentRuntimeProcessArgs(args) }
func isNonToolHelperArgs(args string) bool       { return procgroup.IsNonToolHelperArgs(args) }
func stopChildToolProcesses(cliPID int, command string) int {
	return procgroup.StopChildToolProcesses(cliPID, command)
}
func selectOpenCodeDaemonPID(byPID map[int]procSnapshot, children map[int][]int, port string) int {
	return procgroup.SelectOpenCodeDaemonPID(byPID, children, port)
}
func openCodeDaemonPID(baseURL string) int { return procgroup.OpenCodeDaemonPID(baseURL) }
func stopOpenCodeToolProcessByMarker(daemonPID int, marker string) int {
	return procgroup.StopOpenCodeToolProcessByMarker(daemonPID, marker)
}
func argsCarryStopMarker(args, marker string) bool {
	return procgroup.ArgsCarryStopMarker(args, marker)
}
func hasLiveChildToolProcess(rootPID int, byPID map[int]procSnapshot, children map[int][]int, commands ...string) bool {
	return procgroup.HasLiveChildToolProcess(rootPID, byPID, children, commands...)
}
func findOpenCodeShellGroup(byPID map[int]procSnapshot, children map[int][]int, port, marker string) int {
	return procgroup.FindOpenCodeShellGroup(byPID, children, port, marker)
}
func runtimeShellGroups(byPID map[int]procSnapshot, children map[int][]int, root, protected int) []int {
	return procgroup.RuntimeShellGroups(byPID, children, root, protected)
}
func pgrepPIDs(args []string) ([]int, bool) { return procgroup.PgrepPIDs(args) }
