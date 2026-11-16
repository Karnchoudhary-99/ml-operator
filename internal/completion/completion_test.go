package completion

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptShells(t *testing.T) {
	for _, shell := range Shells() {
		out, err := Script(shell)
		if err != nil {
			t.Fatalf("Script(%s): %v", shell, err)
		}
		if out == "" {
			t.Fatalf("Script(%s) is empty", shell)
		}
		// Every command must be completable, and every flag of every
		// command must appear in every script.
		for _, c := range Commands() {
			if !strings.Contains(out, c.Name) {
				t.Fatalf("Script(%s) never mentions command %q", shell, c.Name)
			}
			for _, f := range c.Flags {
				if !strings.Contains(out, f) {
					t.Fatalf("Script(%s) never mentions flag %q of %q", shell, f, c.Name)
				}
			}
		}
		if !strings.Contains(out, "__complete-names") {
			t.Fatalf("Script(%s) does not complete deployment names", shell)
		}
	}
}

func TestScriptUnknownShell(t *testing.T) {
	if _, err := Script("powershell"); err == nil {
		t.Fatal("Script(powershell) should fail")
	} else if !strings.Contains(err.Error(), "powershell") || !strings.Contains(err.Error(), "bash") {
		t.Fatalf("unknown-shell error %q should name the shell and the valid ones", err)
	}
}

func TestBashScriptSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	out, err := Script("bash")
	if err != nil {
		t.Fatalf("Script(bash): %v", err)
	}
	path := filepath.Join(t.TempDir(), "mlop.bash")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
	// Sourcing it must register the completion without error.
	if out, err := exec.Command(bash, "-c", "source "+path+" && complete -p mlop").CombinedOutput(); err != nil {
		t.Fatalf("source + complete -p: %v\n%s", err, out)
	} else if !strings.Contains(string(out), "_mlop") {
		t.Fatalf("completion not registered: %q", out)
	}
}
