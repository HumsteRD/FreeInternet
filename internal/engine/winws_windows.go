// Package engine запускает движок обхода DPI.
package engine

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// readyTimeout — сколько ждать сообщения о старте перехвата. В канал winws пишет
// с буферизацией, и сообщение может не прийти; тогда живой процесс считаем готовым.
const readyTimeout = 3 * time.Second

// Process — запущенный winws.
type Process struct {
	cmd     *exec.Cmd
	out     *outputWatcher
	done    chan struct{}
	waitErr error
}

// Start запускает winws и ждёт, пока он начнёт перехват.
func Start(ctx context.Context, exe string, args []string) (*Process, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out := &outputWatcher{marker: "capture is started", ready: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &Process{cmd: cmd, out: out, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()

	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-out.ready:
		return p, nil
	case <-p.done:
		return nil, &RunError{AtStart: true, Exit: p.waitErr, Output: out.tail()}
	case <-ctx.Done():
		p.Stop()
		return nil, ctx.Err()
	case <-timer.C:
		select {
		case <-p.done:
			return nil, &RunError{AtStart: true, Exit: p.waitErr, Output: out.tail()}
		default:
			return p, nil
		}
	}
}

// Stop завершает winws и дожидается выхода.
func (p *Process) Stop() error {
	select {
	case <-p.done:
		return nil
	default:
	}
	if err := p.cmd.Process.Kill(); err != nil {
		return err
	}
	<-p.done
	return nil
}

// PID — номер процесса winws: чужие winws.exe закрываются, а этот нет.
func (p *Process) PID() uint32 { return uint32(p.cmd.Process.Pid) }

// Done закрывается, когда winws завершился — в том числе сам, аварийно.
func (p *Process) Done() <-chan struct{} {
	return p.done
}

// Err — причина завершения и хвост вывода winws; пока процесс работает — nil.
func (p *Process) Err() error {
	select {
	case <-p.done:
		return &RunError{Exit: p.waitErr, Output: p.out.tail()}
	default:
		return nil
	}
}

// outputWatcher хранит хвост вывода и сигналит, когда встретился маркер готовности.
type outputWatcher struct {
	mu     sync.Mutex
	buf    []byte
	marker string
	ready  chan struct{}
	seen   bool
}

func (w *outputWatcher) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	if len(w.buf) > 8<<10 {
		w.buf = w.buf[len(w.buf)-4<<10:]
	}
	if !w.seen && strings.Contains(strings.ToLower(string(w.buf)), w.marker) {
		w.seen = true
		close(w.ready)
	}
	return len(b), nil
}

func (w *outputWatcher) tail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(string(w.buf))
}

// IsElevated сообщает, запущен ли процесс с правами администратора (нужны WinDivert).
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

var (
	conflictProcesses = []string{"winws.exe", "winws2.exe", "goodbyedpi.exe"}
	conflictServices  = []string{"zapret", "winws1", "winws2", "GoodbyeDPI", "discordfix_zapret"}
)

// Conflicts возвращает запущенные процессы и службы других обходов DPI:
// одновременно с ними результаты проверок неверны.
func Conflicts() ([]string, error) {
	var found []string

	names, err := processNames()
	if err != nil {
		return nil, err
	}
	for _, c := range conflictProcesses {
		if names[c] {
			found = append(found, "процесс "+c)
		}
	}

	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return found, fmt.Errorf("службы не проверены: %w", err)
	}
	defer windows.CloseServiceHandle(scm)
	for _, name := range conflictServices {
		if serviceRunning(scm, name) {
			found = append(found, "служба "+name)
		}
	}
	return found, nil
}

func processNames() (map[string]bool, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	names := make(map[string]bool)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		names[strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))] = true
	}
	return names, nil
}

func serviceRunning(scm windows.Handle, name string) bool {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	h, err := windows.OpenService(scm, n, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(h)
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return false
	}
	return st.CurrentState != windows.SERVICE_STOPPED
}
