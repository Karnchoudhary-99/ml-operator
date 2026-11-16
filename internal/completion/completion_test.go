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
		// command must appear in every script. Fish spells flags
		// as -l/--less forms (-l dry-run, -s f), so check the
		// spelling each shell actually emits.
		for _, c := range Commands() {
			if !strings.Contains(out, c.Name) {
				t.Fatalf("Script(%s) never mentions command %q", shell, c.Name)
			}
			for _, f := range c.Flags {
				if !flagAppears(out, shell, f) {
					t.Fatalf("Script(%s) never completes flag %q of %q", shell, f, c.Name)
				}
			}
		}
		if !strings.Contains(out, "__complete-names") {
			t.Fatalf("Script(%s) does not complete deployment names", shell)
		}
	}
}

// flagAppears checks that a flag is completable in a script. Bash
// and zsh carry the flag literally (--dry-run, -f); fish splits it
// into -l dry-run / -s f, so check that spelling there.
func flagAppears(script, shell, flag string) bool {
	if shell == "fish" {
		if strings.HasPrefix(flag, "--") {
			return strings.Contains(script, "-l "+strings.TrimLeft(flag, "-"))
		}
		return strings.Contains(script, "-s "+strings.TrimLeft(flag, "-"))
	}
	return strings.Contains(script, flag)
}

func TestScriptUnknownShell(t *testing.T) {
	if _, err := Script("powershell"); err == nil {
		t.Fatal("Script(powershell) should fail")
	} else if !strings.Contains(err.Error(), "powershell") || !strings.Contains(err.Error(), "bash, zsh, fish") {
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

func TestZshScriptSyntax(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	out, err := Script("zsh")
	if err != nil {
		t.Fatalf("Script(zsh): %v", err)
	}
	path := filepath.Join(t.TempDir(), "mlop.zsh")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if out, err := exec.Command(zsh, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("zsh -n: %v\n%s", err, out)
	}
}

func TestFishScriptShape(t *testing.T) {
	// No fish binary on the dev VM (or in CI), so this checks the
	// shape instead of running it: one `complete -c mlop` line per
	// command, a name-completion line driven by __complete-names,
	// and the rollout subcommand line.
	out, err := Script("fish")
	if err != nil {
		t.Fatalf("Script(fish): %v", err)
	}
	if got := strings.Count(out, "complete -c mlop"); got < len(Commands()) {
		t.Fatalf("fish script has %d complete lines, want >= %d", got, len(Commands()))
	}
	if !strings.Contains(out, "__fish_seen_subcommand_from rollout") {
		t.Fatal("fish script does not complete the rollout subcommand")
	}
	if !strings.Contains(out, "(mlop __complete-names)") {
		t.Fatal("fish script does not complete deployment names from __complete-names")
	}
}
