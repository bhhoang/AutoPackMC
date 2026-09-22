package runtime

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer collects a process's output while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startHelper runs the helper server through the same path as Launch.
func startHelper(t *testing.T, env ...string) (*Server, *syncBuffer) {
	t.Helper()
	out := &syncBuffer{}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperServer$")
	cmd.Env = append(append(os.Environ(), "MCPACKCTL_HELPER_SERVER=1"), env...)
	cmd.Stdout, cmd.Stderr = out, out
	s, err := start(cmd, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Kill() })
	return s, out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerSendAndStop(t *testing.T) {
	s, out := startHelper(t)

	if err := s.Send("say hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return strings.Contains(out.String(), "> say hello") })

	if err := s.Stop(20 * time.Second); err != nil {
		t.Fatalf("Stop = %v, want a clean stop", err)
	}
	if !strings.Contains(out.String(), "Stopping the server") {
		t.Errorf("server did not receive stop; output %q", out.String())
	}
	if err := s.Wait(); err != nil {
		t.Errorf("Wait = %v, want nil after a clean stop", err)
	}
	if err := s.Send("list"); err == nil {
		t.Error("Send after exit succeeded, want an error")
	}
}

func TestServerNamesClientOnlyCrash(t *testing.T) {
	s, _ := startHelper(t, "MCPACKCTL_HELPER_CRASH=1")

	if err := s.Wait(); !errors.Is(err, ErrClientOnlyCrash) {
		t.Fatalf("Wait = %v, want ErrClientOnlyCrash", err)
	}
	mods := s.ClientOnlyMods()
	if len(mods) != 1 || mods[0].ModID == "" {
		t.Fatalf("ClientOnlyMods = %+v, want the one mod from the report", mods)
	}
}

func TestServerKill(t *testing.T) {
	s, _ := startHelper(t, "MCPACKCTL_HELPER_IGNORE_STOP=1")
	if err := s.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done not closed after Kill")
	}
}
