package subproc

import "os/exec"

// StartTied starts cmd so that it does not outlive Hoxi.
//
// On Windows a child keeps running when its parent dies, so a Hoxi that
// crashes or is ended with `taskkill /F` (the installer and uninstaller do
// this) leaves the child behind, and a child started from the install
// directory keeps its .exe locked. StartTied starts the child suspended, puts
// it in a job object that Windows kills when Hoxi's handle to it closes, and
// then lets it run, so the child and everything it starts later end with
// Hoxi. Children may still leave the job by asking for breakaway explicitly.
//
// If the job cannot be set up the child still starts, untied, and onUntied
// (when non-nil) receives the reason; the returned error is only for a child
// that could not be started. Elsewhere StartTied is cmd.Start.
func StartTied(cmd *exec.Cmd, onUntied func(error)) error {
	return startTied(cmd, onUntied)
}

func reportUntied(onUntied func(error), err error) {
	if onUntied != nil && err != nil {
		onUntied(err)
	}
}
