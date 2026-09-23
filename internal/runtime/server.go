package runtime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bhhoang/Maple/pkg/logger"
	"github.com/bhhoang/Maple/pkg/utils"
)

// ErrClientOnlyCrash is returned by Server.Wait when client-only mods crashed
// the server. Server.ClientOnlyMods names them.
var ErrClientOnlyCrash = errors.New("client-only mods crashed the server")

// Server is a running Minecraft server process.
type Server struct {
	dir     string
	cmd     *exec.Cmd
	started time.Time

	consoleMu sync.Mutex
	console   io.WriteCloser

	done    chan struct{} // closed once the process has exited
	err     error         // exit error, set before done is closed
	crashed []ClientOnlyMod
}

// Launch starts the Minecraft server located in serverDir, with its console
// output written to stdout and stderr. ram is a JVM heap size such as "4G";
// empty means a -Xmx in user_jvm_args.txt or defaultRAM applies. javaPath
// may be "java" to use PATH.
func Launch(serverDir, ram, javaPath string, stdout, stderr io.Writer) (*Server, error) {
	if javaPath == "" {
		javaPath = "java"
	}
	serverJAR, args, err := buildLaunchArgs(serverDir, ram)
	if err != nil {
		return nil, err
	}
	logger.Get().Info().
		Str("serverDir", serverDir).
		Str("jar", serverJAR).
		Strs("args", args).
		Msg("starting Minecraft server")

	cmd := exec.Command(javaPath, args...) // #nosec G204
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	utils.HideWindow(cmd)
	return start(cmd, serverDir)
}

// start runs cmd as the server in serverDir.
func start(cmd *exec.Cmd, serverDir string) (*Server, error) {
	cmd.Dir = serverDir
	// The server console is fed through a pipe so that commands, including
	// "stop" on shutdown, can be sent to it.
	console, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open server console: %w", err)
	}

	s := &Server{
		dir:     serverDir,
		cmd:     cmd,
		console: console,
		// Crash reports written after this are from this run (with slack for
		// coarse file timestamps).
		started: time.Now().Add(-2 * time.Second),
		done:    make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start server process: %w", err)
	}
	go s.wait()
	return s, nil
}

func (s *Server) wait() {
	err := s.cmd.Wait()
	s.crashed = reportClientOnlyCrash(s.dir, s.started)
	if len(s.crashed) > 0 && err == nil {
		// Forge can exit 0 after a mod loading failure.
		err = ErrClientOnlyCrash
	} else if len(s.crashed) > 0 {
		err = fmt.Errorf("%w: %v", ErrClientOnlyCrash, err)
	}
	s.err = err
	close(s.done)
}

// Done is closed once the server process has exited.
func (s *Server) Done() <-chan struct{} { return s.done }

// Wait blocks until the server exits and returns why it stopped: nil for a
// clean exit, ErrClientOnlyCrash (possibly wrapped) when client-only mods
// crashed it, or the process error.
func (s *Server) Wait() error {
	<-s.done
	return s.err
}

// ClientOnlyMods returns the client-only mods named in the crash reports
// this run wrote. It is empty until the server has exited.
func (s *Server) ClientOnlyMods() []ClientOnlyMod {
	select {
	case <-s.done:
		return s.crashed
	default:
		return nil
	}
}

// Send writes one command to the server console, as if typed there.
func (s *Server) Send(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	select {
	case <-s.done:
		return errors.New("the server is not running")
	default:
	}
	s.consoleMu.Lock()
	defer s.consoleMu.Unlock()
	_, err := io.WriteString(s.console, command+"\n")
	return err
}

// Stop asks the server to save its worlds and exit, and kills it if it has
// not exited after timeout.
func (s *Server) Stop(timeout time.Duration) error {
	return stopServer(s.cmd.Process, &lockedWriter{s: s}, s.exitCh(), nil, timeout)
}

// Kill ends the server process at once, without saving.
func (s *Server) Kill() error {
	select {
	case <-s.done:
		return nil
	default:
	}
	if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	<-s.done
	return nil
}

// exitCh adapts Done to the channel stopServer waits on.
func (s *Server) exitCh() <-chan error {
	ch := make(chan error, 1)
	go func() {
		<-s.done
		ch <- s.err
	}()
	return ch
}

// lockedWriter serialises console writes from Stop with those from Send.
type lockedWriter struct{ s *Server }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.s.consoleMu.Lock()
	defer w.s.consoleMu.Unlock()
	return w.s.console.Write(p)
}
