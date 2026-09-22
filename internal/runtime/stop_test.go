package runtime

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHelperServer is not a real test: it is re-executed by the stop tests
// as a stand-in for a Minecraft server. It exits on "stop" unless told to
// ignore it.
func TestHelperServer(t *testing.T) {
	if os.Getenv("MCPACKCTL_HELPER_SERVER") == "" {
		t.Skip("helper process")
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "stop" && os.Getenv("MCPACKCTL_HELPER_IGNORE_STOP") == "" {
			fmt.Println("Stopping the server")
			os.Exit(0)
		}
	}
	// Keep running after stdin closes, like a server that hangs.
	time.Sleep(time.Minute)
}

func startHelperServer(t *testing.T, ignoreStop bool) (*exec.Cmd, *os.File, chan error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperServer$")
	cmd.Env = append(os.Environ(), "MCPACKCTL_HELPER_SERVER=1")
	if ignoreStop {
		cmd.Env = append(cmd.Env, "MCPACKCTL_HELPER_IGNORE_STOP=1")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = r
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.Close()
	t.Cleanup(func() { w.Close(); _ = cmd.Process.Kill() })

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, w, done
}

func TestStopServerSendsStop(t *testing.T) {
	cmd, console, done := startHelperServer(t, false)

	start := time.Now()
	err := stopServer(cmd.Process, console, done, make(chan os.Signal), 30*time.Second)
	if err != nil {
		t.Fatalf("stopServer = %v, want a clean stop", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("clean stop took %s", elapsed)
	}
}

func TestStopServerKillsAfterTimeout(t *testing.T) {
	cmd, console, done := startHelperServer(t, true)

	err := stopServer(cmd.Process, console, done, make(chan os.Signal), 500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not stop") {
		t.Fatalf("stopServer = %v, want a timeout kill", err)
	}
}

func TestStopServerKillsOnSecondInterrupt(t *testing.T) {
	cmd, console, done := startHelperServer(t, true)

	again := make(chan os.Signal, 1)
	again <- os.Interrupt
	err := stopServer(cmd.Process, console, done, again, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "second interrupt") {
		t.Fatalf("stopServer = %v, want a kill on the second interrupt", err)
	}
}
