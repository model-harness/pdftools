package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// skillFiles are the agent skills that tell an agent to use pdfspec: Claude's and Codex's, which
// carry frontmatter, and the generic one without it, for an AGENTS.md or a system prompt.
var skillFiles = []string{
	"../../skills/claude/pdfspec/SKILL.md",
	"../../skills/codex/pdfspec/SKILL.md",
}

const genericSkill = "../../skills/generic/pdfspec.md"

// TestSkillsAreOneSkill: the three skills say the same thing, so an agent is told the same
// whichever it reads, and the frontmatter names the skill pdfspec.
func TestSkillsAreOneSkill(t *testing.T) {
	generic, err := os.ReadFile(genericSkill)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range skillFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ReplaceAll(string(b), "\r\n", "\n")
		rest, ok := strings.CutPrefix(s, "---\n")
		front, body, ok2 := strings.Cut(rest, "\n---\n\n")
		if !ok || !ok2 || !strings.Contains(front, "name: pdfspec\n") || !strings.Contains(front, "description: ") {
			t.Errorf("%s: frontmatter %q", f, front)
			continue
		}
		if body != strings.ReplaceAll(string(generic), "\r\n", "\n") {
			t.Errorf("%s: its body is not %s", filepath.Base(filepath.Dir(f)), genericSkill)
		}
	}
}

// TestSkillCommandsExist: every pdfspec command the skill shows an agent is a real command, and
// every flag on it is one that command takes, so no agent is told to run something that fails.
func TestSkillCommandsExist(t *testing.T) {
	verbs := map[string]bool{"md": true, "okf": true, "images": true, "render": true, "ocr": true, "probe": true}
	b, err := os.ReadFile(genericSkill)
	if err != nil {
		t.Fatal(err)
	}
	flagWord := regexp.MustCompile(`^-([a-z][a-z-]*)`)
	usage := map[string]string{}
	seen := 0
	inCode := false
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if !inCode || !strings.HasPrefix(line, "pdfspec ") {
			continue
		}
		cmd, _, _ := strings.Cut(line, "#")
		f := strings.Fields(cmd)
		if f[1] == "version" {
			seen++
			continue
		}
		if !verbs[f[1]] {
			t.Errorf("%q: no command %q", line, f[1])
			continue
		}
		if _, ok := usage[f[1]]; !ok {
			usage[f[1]] = pdfspecMain(t, f[1]+" -h")
		}
		for _, w := range f[2:] {
			m := flagWord.FindStringSubmatch(w)
			if m == nil {
				continue
			}
			if !strings.Contains(usage[f[1]], "\n  -"+m[1]+"\n") && !strings.Contains(usage[f[1]], "\n  -"+m[1]+" ") {
				t.Errorf("%q: %s has no flag -%s", line, f[1], m[1])
			}
		}
		seen++
	}
	if seen < 10 {
		t.Errorf("read %d pdfspec commands from the skill, want its examples", seen)
	}
}

// pdfspecMain runs pdfspec with args, as the command, and returns what it wrote: the test binary
// again, which TestMain turns into the command (PDFSPEC_TEST_AS_MAIN).
func pdfspecMain(t *testing.T, args string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PDFSPEC_TEST_AS_MAIN="+args)
	out, _ := cmd.CombinedOutput()
	return string(out)
}
