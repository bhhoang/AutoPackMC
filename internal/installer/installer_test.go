package installer

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestJavaStartError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-java")
	err := exec.Command(missing, "-version").Run()
	if err == nil {
		t.Fatal("expected running a missing binary to fail")
	}
	startErr := javaStartError(missing, err)
	if startErr == nil || !strings.Contains(startErr.Error(), "--java-path") {
		t.Errorf("javaStartError = %v, want a hint about --java-path", startErr)
	}

	// A process that ran and exited non-zero is an installer failure, which
	// is worth retrying, not a java start failure.
	exitErr := &exec.ExitError{}
	if got := javaStartError("java", exitErr); got != nil {
		t.Errorf("javaStartError(exit error) = %v, want nil", got)
	}
}
