package terminal

import (
	"errors"
	"fmt"
	"strings"
)

// ValidateReadOnlyCommand enforces a lexical and operator-level read-only gate
// for resident bot precheck and Look-around inspection commands.
//
// It rejects empty/multiline input, shell redirections, command substitutions,
// subshells/brace groups, backgrounding, env assignments, path-qualified
// executables, mutating shell/git/network flags, and any command verb outside
// the explicit read-only allowlist — across every segment of |, &&, ||, and ;.
func ValidateReadOnlyCommand(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return errors.New("command is empty")
	}
	if strings.ContainsAny(cmd, "\n\r\x00") {
		return errors.New("multiline or control-character commands are not permitted")
	}

	segments, err := tokenizeReadOnlyShell(cmd)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return errors.New("command is empty")
	}
	for _, seg := range segments {
		if len(seg) == 0 {
			return errors.New("empty command segment is not permitted")
		}
		if err := validateReadOnlySegment(seg); err != nil {
			return err
		}
	}
	return nil
}

// tokenizeReadOnlyShell performs a strict POSIX-like lexical split into command
// segments separated by |, &&, ||, or ; while rejecting redirections, command
// substitution, subshells, brace groups, backgrounding, and unclosed quotes.
func tokenizeReadOnlyShell(cmd string) ([][]string, error) {
	var segments [][]string
	var curSeg []string
	var curTok strings.Builder
	inTok := false

	flushTok := func() {
		if inTok {
			curSeg = append(curSeg, curTok.String())
			curTok.Reset()
			inTok = false
		}
	}
	flushSeg := func(op string) error {
		flushTok()
		if len(curSeg) == 0 {
			return fmt.Errorf("unexpected operator %q with empty command segment", op)
		}
		segments = append(segments, curSeg)
		curSeg = nil
		return nil
	}

	n := len(cmd)
	for i := 0; i < n; {
		ch := cmd[i]

		switch ch {
		case ' ', '\t':
			flushTok()
			i++
		case '\'':
			// Single-quoted string: literal until closing single quote.
			inTok = true
			i++
			closed := false
			for i < n {
				if cmd[i] == '\'' {
					closed = true
					i++
					break
				}
				curTok.WriteByte(cmd[i])
				i++
			}
			if !closed {
				return nil, errors.New("unclosed single quote")
			}
		case '"':
			// Double-quoted string: POSIX shell still expands $() and backticks here.
			inTok = true
			i++
			closed := false
			for i < n {
				c := cmd[i]
				if c == '\\' {
					if i+1 >= n {
						return nil, errors.New("unterminated escape in double quotes")
					}
					next := cmd[i+1]
					curTok.WriteByte(next)
					i += 2
					continue
				}
				if c == '`' {
					return nil, errors.New("command substitution using backticks is not permitted")
				}
				if c == '$' && i+1 < n && cmd[i+1] == '(' {
					return nil, errors.New("command substitution \"$(\" is not permitted")
				}
				if c == '"' {
					closed = true
					i++
					break
				}
				curTok.WriteByte(c)
				i++
			}
			if !closed {
				return nil, errors.New("unclosed double quote")
			}
		case '\\':
			if i+1 >= n {
				return nil, errors.New("trailing backslash is not permitted")
			}
			inTok = true
			curTok.WriteByte(cmd[i+1])
			i += 2
		case '`':
			return nil, errors.New("command substitution using backticks is not permitted")
		case '$':
			if i+1 < n && cmd[i+1] == '(' {
				return nil, errors.New("command substitution \"$(\" is not permitted")
			}
			inTok = true
			curTok.WriteByte(ch)
			i++
		case '>':
			if i+1 < n && cmd[i+1] == '>' {
				return nil, errors.New("redirection \">>\" is not permitted")
			}
			if i+1 < n && cmd[i+1] == '(' {
				return nil, errors.New("process substitution \">(\" is not permitted")
			}
			return nil, errors.New("redirection \">\" is not permitted")
		case '<':
			if i+1 < n && cmd[i+1] == '<' {
				return nil, errors.New("heredoc redirection \"<<\" is not permitted")
			}
			if i+1 < n && cmd[i+1] == '(' {
				return nil, errors.New("process substitution \"<(\" is not permitted")
			}
			return nil, errors.New("redirection \"<\" is not permitted")
		case '(', ')':
			return nil, fmt.Errorf("subshell operator %q is not permitted", string(ch))
		case '{', '}':
			return nil, fmt.Errorf("brace group operator %q is not permitted", string(ch))
		case ';':
			if err := flushSeg(";"); err != nil {
				return nil, err
			}
			i++
		case '|':
			if i+1 < n && cmd[i+1] == '|' {
				if err := flushSeg("||"); err != nil {
					return nil, err
				}
				i += 2
			} else {
				if err := flushSeg("|"); err != nil {
					return nil, err
				}
				i++
			}
		case '&':
			if i+1 < n && cmd[i+1] == '&' {
				if err := flushSeg("&&"); err != nil {
					return nil, err
				}
				i += 2
			} else if i+1 < n && cmd[i+1] == '>' {
				return nil, errors.New("redirection \"&>\" is not permitted")
			} else {
				return nil, errors.New("background execution \"&\" is not permitted")
			}
		case '#':
			if !inTok {
				return nil, errors.New("shell comments are not permitted")
			}
			curTok.WriteByte(ch)
			i++
		default:
			inTok = true
			curTok.WriteByte(ch)
			i++
		}
	}

	flushTok()
	if len(curSeg) == 0 {
		return nil, errors.New("trailing operator with empty command segment is not permitted")
	}
	segments = append(segments, curSeg)
	return segments, nil
}

var explicitMutatingVerbs = map[string]bool{
	"rm": true, "mv": true, "cp": true, "chmod": true, "chown": true, "chgrp": true,
	"dd": true, "tee": true, "sed": true, "touch": true, "mkdir": true, "rmdir": true,
	"ln": true, "truncate": true, "kill": true, "pkill": true, "killall": true,
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"eval": true, "exec": true, "source": true, ".": true, "sudo": true, "su": true,
	"xargs": true, "awk": true, "gawk": true, "python": true, "python3": true,
	"node": true, "perl": true, "ruby": true, "env": true,
}

var readOnlySimpleVerbs = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true,
	"cat": true, "head": true, "tail": true,
	"ls": true, "stat": true, "wc": true,
	"test": true, "[": true,
	"pwd": true, "which": true, "file": true,
	"realpath": true, "dirname": true, "basename": true,
	"uniq": true, "cut": true, "tr": true,
	"diff": true, "echo": true, "true": true, "false": true,
	"uname": true, "whoami": true, "id": true,
	"jq": true,
}

func validateReadOnlySegment(tokens []string) error {
	verb := tokens[0]
	args := tokens[1:]

	if verb == "" {
		return errors.New("empty command verb")
	}
	if strings.Contains(verb, "=") {
		return fmt.Errorf("environment assignment %q is not permitted", verb)
	}
	if strings.ContainsAny(verb, "/\\") {
		return fmt.Errorf("path-qualified executable %q is not permitted", verb)
	}

	if explicitMutatingVerbs[verb] {
		return fmt.Errorf("mutating or shell-escape command %q is not permitted in read-only mode", verb)
	}

	if readOnlySimpleVerbs[verb] {
		return nil
	}

	switch verb {
	case "sort":
		return rejectReadOnlyOption(verb, args, 'o', "--output")
	case "date":
		return rejectReadOnlyOption(verb, args, 's', "--set")
	case "printf":
		for _, a := range args {
			if a == "-v" || strings.HasPrefix(a, "-v") {
				return errors.New("printf -v is not permitted in read-only mode")
			}
		}
		return nil
	case "rg":
		return validateReadOnlyRG(args)
	case "git":
		return validateReadOnlyGit(args)
	case "curl":
		return validateReadOnlyCurl(args)
	case "wget":
		return validateReadOnlyWget(args)
	case "gh":
		return validateReadOnlyGH(args)
	case "find":
		return validateReadOnlyFind(args)
	default:
		return fmt.Errorf("command %q is not in the read-only allowlist", verb)
	}
}

func rejectReadOnlyOption(command string, args []string, short rune, long string) error {
	for _, arg := range args {
		var blocked bool
		if strings.HasPrefix(arg, "--") {
			flag, _, _ := strings.Cut(arg, "=")
			blocked = len(flag) > 2 && strings.HasPrefix(long, flag)
		} else if strings.HasPrefix(arg, "-") {
			blocked = strings.ContainsRune(arg[1:], short)
		}
		if blocked {
			return fmt.Errorf("%s flag %q is not permitted in read-only mode", command, arg)
		}
	}
	return nil
}

func validateReadOnlyRG(args []string) error {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--pre" || strings.HasPrefix(a, "--pre=") ||
			a == "--pre-glob" || strings.HasPrefix(a, "--pre-glob=") {
			return fmt.Errorf("rg flag %q executes external commands and is not permitted", a)
		}
	}
	return nil
}

var allowedGitReadOnlySubcmds = map[string]bool{
	"status":    true,
	"diff":      true,
	"log":       true,
	"show":      true,
	"branch":    true,
	"rev-parse": true,
	"describe":  true,
	"shortlog":  true,
	"ls-files":  true,
	"ls-tree":   true,
	"blame":     true,
	"grep":      true,
	"tag":       true,
}

func validateReadOnlyGit(args []string) error {
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break
		}
		switch {
		case a == "-c" || strings.HasPrefix(a, "-c") ||
			a == "--config-env" || strings.HasPrefix(a, "--config-env=") ||
			a == "--exec-path" || strings.HasPrefix(a, "--exec-path="):
			return fmt.Errorf("git global flag %q is not permitted in read-only mode", a)
		case a == "-C" || a == "--git-dir" || a == "--work-tree" || a == "--namespace" || a == "--super-prefix":
			if i+1 >= len(args) {
				return fmt.Errorf("git flag %q missing argument", a)
			}
			i += 2
		case strings.HasPrefix(a, "--git-dir=") ||
			strings.HasPrefix(a, "--work-tree=") ||
			strings.HasPrefix(a, "--namespace=") ||
			strings.HasPrefix(a, "--super-prefix=") ||
			a == "--no-pager" || a == "-p" || a == "--paginate" ||
			a == "--bare" || a == "--no-replace-objects" ||
			a == "--literal-pathspecs" || a == "--glob-pathspecs" ||
			a == "--noglob-pathspecs" || a == "--icase-pathspecs" ||
			a == "--no-optional-locks":
			i++
		default:
			return fmt.Errorf("git global flag %q is not permitted in read-only mode", a)
		}
	}

	if i >= len(args) {
		return errors.New("git requires a read-only subcommand")
	}
	sub := args[i]
	subArgs := args[i+1:]
	if !allowedGitReadOnlySubcmds[sub] {
		return fmt.Errorf("git subcommand %q is not permitted in read-only mode", sub)
	}

	for _, a := range subArgs {
		if a == "--" {
			break
		}
		if a == "--output" || strings.HasPrefix(a, "--output=") ||
			a == "--ext-diff" || a == "--textconv" {
			return fmt.Errorf("git %s flag %q is not permitted in read-only mode", sub, a)
		}
	}

	switch sub {
	case "branch":
		return validateReadOnlyGitBranch(subArgs)
	case "tag":
		return validateReadOnlyGitTag(subArgs)
	case "grep":
		return rejectReadOnlyOption("git grep", subArgs, 'O', "--open-files-in-pager")
	}
	return nil
}

func validateReadOnlyGitBranch(args []string) error {
	listMode := false
	expectValue := false
	for _, a := range args {
		if expectValue {
			expectValue = false
			continue
		}
		if !strings.HasPrefix(a, "-") {
			if !listMode {
				return fmt.Errorf("git branch positional argument %q is not permitted without --list", a)
			}
			continue
		}
		switch {
		case a == "-a" || a == "--all" || a == "-r" || a == "--remotes" ||
			a == "-v" || a == "-vv" || a == "--verbose" ||
			a == "--show-current" || a == "--color" || a == "--no-color" ||
			a == "--abbrev" || a == "--no-abbrev" ||
			strings.HasPrefix(a, "--color=") ||
			strings.HasPrefix(a, "--abbrev=") ||
			strings.HasPrefix(a, "--format=") ||
			strings.HasPrefix(a, "--sort=") ||
			strings.HasPrefix(a, "--contains=") ||
			strings.HasPrefix(a, "--no-contains=") ||
			strings.HasPrefix(a, "--merged=") ||
			strings.HasPrefix(a, "--no-merged=") ||
			strings.HasPrefix(a, "--points-at="):
			// Safe read-only listing flags.
		case a == "-l" || a == "--list":
			listMode = true
		case a == "--format" || a == "--sort" ||
			a == "--contains" || a == "--no-contains" ||
			a == "--merged" || a == "--no-merged" ||
			a == "--points-at":
			expectValue = true
		default:
			return fmt.Errorf("git branch flag %q is not permitted in read-only mode", a)
		}
	}
	return nil
}

func validateReadOnlyGitTag(args []string) error {
	listMode := len(args) == 0
	expectValue := false
	for _, a := range args {
		if expectValue {
			expectValue = false
			continue
		}
		if !strings.HasPrefix(a, "-") {
			if !listMode {
				return fmt.Errorf("git tag creation argument %q is not permitted in read-only mode", a)
			}
			continue
		}
		switch {
		case a == "-l" || a == "--list":
			listMode = true
		case a == "-n" || strings.HasPrefix(a, "-n") ||
			strings.HasPrefix(a, "--sort=") ||
			strings.HasPrefix(a, "--format=") ||
			strings.HasPrefix(a, "--contains=") ||
			strings.HasPrefix(a, "--no-contains=") ||
			strings.HasPrefix(a, "--merged=") ||
			strings.HasPrefix(a, "--no-merged=") ||
			strings.HasPrefix(a, "--points-at="):
			listMode = true
		case a == "--sort" || a == "--format" ||
			a == "--contains" || a == "--no-contains" ||
			a == "--merged" || a == "--no-merged" ||
			a == "--points-at":
			listMode = true
			expectValue = true
		default:
			return fmt.Errorf("git tag flag %q is not permitted in read-only mode", a)
		}
	}
	return nil
}

func validateReadOnlyCurl(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		// Long flags that mutate remote state, write files, or load config files.
		if strings.HasPrefix(a, "--") {
			lower := strings.ToLower(a)
			switch {
			case lower == "--request":
				if i+1 >= len(args) {
					return errors.New("curl --request missing method")
				}
				m := strings.ToUpper(args[i+1])
				if m != "GET" && m != "HEAD" {
					return fmt.Errorf("curl write HTTP method %q is not permitted", m)
				}
				i++
			case strings.HasPrefix(lower, "--request="):
				m := strings.ToUpper(strings.TrimPrefix(a, a[:len("--request=")]))
				if m != "GET" && m != "HEAD" {
					return fmt.Errorf("curl write HTTP method %q is not permitted", m)
				}
			case lower == "--data" || strings.HasPrefix(lower, "--data-") || strings.HasPrefix(lower, "--data=") ||
				lower == "--json" || strings.HasPrefix(lower, "--json=") ||
				lower == "--form" || strings.HasPrefix(lower, "--form-") || strings.HasPrefix(lower, "--form=") ||
				lower == "--upload-file" || strings.HasPrefix(lower, "--upload-file=") ||
				lower == "--output" || strings.HasPrefix(lower, "--output=") ||
				lower == "--remote-name" || strings.HasPrefix(lower, "--remote-name") ||
				lower == "--dump-header" || strings.HasPrefix(lower, "--dump-header=") ||
				lower == "--cookie-jar" || strings.HasPrefix(lower, "--cookie-jar=") ||
				lower == "--trace" || strings.HasPrefix(lower, "--trace") ||
				lower == "--config" || strings.HasPrefix(lower, "--config="):
				return fmt.Errorf("curl write flag %q is not permitted in read-only mode", a)
			}
			continue
		}

		// Short flags (including bundled forms like -sXPOST, -fsSo, -dfoo).
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags := a[1:]
			for j := 0; j < len(flags); j++ {
				f := flags[j]
				switch f {
				case 'X':
					method := flags[j+1:]
					if method == "" {
						if i+1 >= len(args) {
							return errors.New("curl -X missing method")
						}
						i++
						method = args[i]
					}
					m := strings.ToUpper(method)
					if m != "GET" && m != "HEAD" {
						return fmt.Errorf("curl write HTTP method %q is not permitted", m)
					}
					j = len(flags) // rest of token was the method
				case 'd', 'F', 'T', 'o', 'O', 'D', 'c', 'K':
					return fmt.Errorf("curl write flag -%c is not permitted in read-only mode", f)
				}
			}
		}
	}
	return nil
}

func validateReadOnlyWget(args []string) error {
	readOnlyTarget := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		lower := strings.ToLower(a)
		switch {
		case lower == "--spider":
			readOnlyTarget = true
		case a == "-O":
			if i+1 >= len(args) || args[i+1] != "-" {
				return errors.New("wget -O to a file is not permitted in read-only mode")
			}
			readOnlyTarget = true
			i++
		case strings.HasPrefix(a, "-O"):
			if a != "-O-" {
				return fmt.Errorf("wget write flag %q is not permitted in read-only mode", a)
			}
			readOnlyTarget = true
		case lower == "--output-document=-":
			readOnlyTarget = true
		case lower == "--output-document":
			if i+1 >= len(args) || args[i+1] != "-" {
				return errors.New("wget --output-document to a file is not permitted in read-only mode")
			}
			readOnlyTarget = true
			i++
		case strings.HasPrefix(lower, "--output-document="):
			return fmt.Errorf("wget write flag %q is not permitted in read-only mode", a)
		case strings.HasPrefix(lower, "--post-") ||
			strings.HasPrefix(lower, "--body-") ||
			strings.HasPrefix(lower, "--directory-prefix") ||
			a == "-P" || strings.HasPrefix(a, "-P") ||
			lower == "--save-cookies" || strings.HasPrefix(lower, "--save-cookies=") ||
			lower == "--output-file" || strings.HasPrefix(lower, "--output-file=") ||
			lower == "--append-output" || strings.HasPrefix(lower, "--append-output=") ||
			a == "-o" || a == "-a":
			return fmt.Errorf("wget write flag %q is not permitted in read-only mode", a)
		case lower == "--method":
			if i+1 >= len(args) {
				return errors.New("wget --method missing value")
			}
			m := strings.ToUpper(args[i+1])
			if m != "GET" && m != "HEAD" {
				return fmt.Errorf("wget write HTTP method %q is not permitted", m)
			}
			i++
		case strings.HasPrefix(lower, "--method="):
			m := strings.ToUpper(strings.TrimPrefix(a, a[:len("--method=")]))
			if m != "GET" && m != "HEAD" {
				return fmt.Errorf("wget write HTTP method %q is not permitted", m)
			}
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1:
			// Check bundled short flags such as -qO- vs -qOfile.
			flags := a[1:]
			for j := 0; j < len(flags); j++ {
				switch flags[j] {
				case 'O':
					rest := flags[j+1:]
					if rest == "-" {
						readOnlyTarget = true
					} else if rest == "" && i+1 < len(args) && args[i+1] == "-" {
						readOnlyTarget = true
						i++
					} else {
						return fmt.Errorf("wget write flag %q is not permitted in read-only mode", a)
					}
					j = len(flags)
				case 'P', 'o', 'a':
					return fmt.Errorf("wget write flag -%c is not permitted in read-only mode", flags[j])
				}
			}
		}
	}
	if !readOnlyTarget {
		return errors.New("wget writes downloaded files to disk unless --spider or -O - is specified")
	}
	return nil
}

func validateReadOnlyGH(args []string) error {
	if len(args) == 0 {
		return errors.New("gh requires a read-only subcommand")
	}
	switch args[0] {
	case "status":
		return nil
	case "pr":
		if len(args) < 2 {
			return errors.New("gh pr requires a read-only subcommand")
		}
		switch args[1] {
		case "checks", "list", "view", "status", "diff":
			return nil
		default:
			return fmt.Errorf("gh pr %q is not permitted in read-only mode", args[1])
		}
	case "issue":
		if len(args) < 2 {
			return errors.New("gh issue requires a read-only subcommand")
		}
		switch args[1] {
		case "list", "view", "status":
			return nil
		default:
			return fmt.Errorf("gh issue %q is not permitted in read-only mode", args[1])
		}
	case "run":
		if len(args) < 2 {
			return errors.New("gh run requires a read-only subcommand")
		}
		switch args[1] {
		case "list", "view":
			return nil
		default:
			return fmt.Errorf("gh run %q is not permitted in read-only mode", args[1])
		}
	default:
		return fmt.Errorf("gh subcommand %q is not permitted in read-only mode", args[0])
	}
}

func validateReadOnlyFind(args []string) error {
	for _, a := range args {
		switch a {
		case "-exec", "-execdir", "-ok", "-okdir", "-delete",
			"-fprint", "-fprint0", "-fprintf", "-fls":
			return fmt.Errorf("find action %q is not permitted in read-only mode", a)
		}
	}
	return nil
}
