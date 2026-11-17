//go:build windows

package termhost

import (
	"io"
	"os"
	"os/exec"
)

func startPlatformPTY(cmd *exec.Cmd, _, _ uint16) (io.ReadWriteCloser, func(uint16, uint16) error, func() error, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, nil, nil, err
	}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, nil, nil, err
	}
	_ = inR.Close()
	_ = outW.Close()

	rw := &pipeReadWriteCloser{r: outR, w: inW}
	resizeFn := func(uint16, uint16) error { return nil }
	killFn := func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return rw.Close()
	}
	return rw, resizeFn, killFn, nil
}

type pipeReadWriteCloser struct {
	r *os.File
	w *os.File
}

func (p *pipeReadWriteCloser) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeReadWriteCloser) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeReadWriteCloser) Close() error {
	_ = p.w.Close()
	return p.r.Close()
}
