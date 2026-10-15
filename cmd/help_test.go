package cmd

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

func runHelp(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	if code := Execute(args, Dependencies{Out: &out, Err: &diagnostics, Home: t.TempDir(), Cwd: t.TempDir()}); code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, &diagnostics)
	}
	return out.String(), diagnostics.String()
}

func TestRootHelpGroupsCommandsAndStatesAgentContract(t *testing.T) {
	out, diagnostics := runHelp(t, "--help")
	if diagnostics != "" {
		t.Fatalf("help wrote to stderr: %q", diagnostics)
	}
	for _, want := range []string{
		"Usage:\n  dotllm [flags]\n  dotllm [command]\n",
		"\nContext:\n  init ",
		"\n  status ",
		"\nStore:\n  archive ",
		"\n  restore ",
		"\nAdditional Commands:\n  completion ",
		"--json prints one {\"version\":1,...} object on stdout",
		"2 usage, 3 missing project, 4 conflict, 5 I/O or unavailable, 130 cancelled",
		"  dotllm archive code/foo --days 90   # preview stale entries; nothing moves\n",
		"llm runs this same program; existing llm commands and scripts keep working.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("root help lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("help to a non-terminal must be plain:\n%q", out)
	}
	if again, _ := runHelp(t, "help"); again != out {
		t.Fatalf("`help` and `--help` differ")
	}
}

func TestHelpColourFollowsTheCommandPolicy(t *testing.T) {
	colored, _ := runHelp(t, "--help", "--color", "always")
	for _, want := range []string{"\x1b[1;36mContext:\x1b[0m", "\x1b[1;36mFlags:\x1b[0m", "  \x1b[32mstatus\x1b[0m ", "\x1b[2mUse \"dotllm [command] --help\""} {
		if !strings.Contains(colored, want) {
			t.Errorf("coloured help lacks %q", want)
		}
	}
	if plain, _ := runHelp(t, "--help", "--color", "always", "--json"); strings.Contains(plain, "\x1b[") {
		t.Fatalf("--json must keep help plain")
	}
	if plain, _ := runHelp(t, "status", "--help", "--color", "never"); strings.Contains(plain, "\x1b[") {
		t.Fatalf("--color never must keep help plain")
	}
}

func TestHelpUnderCompatibilityNameIsUnchangedForScripts(t *testing.T) {
	previous := os.Args[0]
	os.Args[0] = "/Users/me/go/bin/llm"
	t.Cleanup(func() { os.Args[0] = previous })

	out, diagnostics := runHelp(t, "--help")
	if diagnostics != "" || !strings.Contains(out, "Usage:\n  llm [flags]\n  llm [command]\n") || !strings.Contains(out, "  llm link --auto ") {
		t.Fatalf("llm help should use the invoked name:\n%s\nstderr=%q", out, diagnostics)
	}
	if strings.Contains(out, "dotllm [command]") || strings.Contains(out, "runs this same program") {
		t.Fatalf("llm help must not mention the rename:\n%s", out)
	}
}

// Every example must be copyable against the real command tree: the command
// it names exists and each long or short flag it passes is defined there.
func TestEveryExampleUsesRealCommandsAndFlags(t *testing.T) {
	root := NewRoot(Dependencies{})
	invocation := regexp.MustCompile(`dotllm((?: [^#|")]*)?)`)
	flag := regexp.MustCompile(`(?:^|\s)(--?[A-Za-z][A-Za-z-]*)`)
	commands := append(root.Commands(), root)
	for _, owner := range commands {
		if owner.Name() != "help" && owner.Name() != "completion" && owner.Example == "" {
			t.Errorf("%s has no examples", owner.Name())
		}
		for _, match := range invocation.FindAllStringSubmatch(owner.Example, -1) {
			args := strings.Fields(match[1])
			target, rest, err := root.Find(args)
			if err != nil {
				t.Errorf("%s example %q: %v", owner.Name(), match[0], err)
				continue
			}
			if len(rest) > 0 && rest[0] != "" && !strings.HasPrefix(rest[0], "-") && target == root {
				t.Errorf("%s example %q names unknown command %q", owner.Name(), match[0], rest[0])
			}
			for _, f := range flag.FindAllStringSubmatch(" "+strings.Join(args, " "), -1) {
				name := strings.TrimLeft(f[1], "-")
				defined := target.Flags().Lookup(name) != nil || target.InheritedFlags().Lookup(name) != nil
				if len(name) == 1 {
					defined = target.Flags().ShorthandLookup(name) != nil || target.InheritedFlags().ShorthandLookup(name) != nil
				}
				if !defined {
					t.Errorf("%s example %q uses undefined flag %s for %s", owner.Name(), match[0], f[1], target.Name())
				}
			}
		}
	}
}
