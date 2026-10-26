package guard

import (
	"path/filepath"
	"slices"
	"strings"
)

// Bash rule identifiers. Each names one consequence a user confirms.
const (
	RuleBashPrivileged  = "bash.privileged"
	RuleBashDisk        = "bash.disk"
	RuleBashFetchExec   = "bash.fetchExec"
	RuleBashDynamic     = "bash.dynamic"
	RuleBashPermissions = "bash.permissions"
	RuleBashDelete      = "bash.delete"
	RuleBashLoseWork    = "bash.loseWork"
	RuleBashOutbound    = "bash.outbound"
)

// bashRuleOrder ranks bash rules by severity for the combined approval.
var bashRuleOrder = []string{
	RuleBashPrivileged, RuleBashDisk, RuleBashFetchExec, RuleBashDynamic, RuleBashPermissions,
	RuleBashDelete, RuleBashLoseWork, RuleBashOutbound,
}

// IsBashRule reports whether ruleID is a bash command consequence.
func IsBashRule(ruleID string) bool { return slices.Contains(bashRuleOrder, ruleID) }

// finding is one consequence of a bash command that requires confirmation.
// Delete findings carry their targets; the Guard drops them when every target
// is a concrete path strictly inside the workspace.
type finding struct {
	rule    string
	desc    string
	pattern string
	// words is the command as rules match it; empty for pipeline findings.
	words []string

	scoped        bool // a recursive delete limited to deleteTargets
	deleteTargets []shellWord
	base          string
	extraArgs     bool // targets also come from xargs or find -exec
	filtered      bool // find -delete limited by a test such as -name
}

const (
	opRead  = "read"
	opWrite = "write"
)

// interpreters execute code read from stdin or an argument.
var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"python": true, "python3": true, "node": true, "perl": true, "ruby": true, "php": true,
}

// shells accept a script with -c.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// launcher describes a command that runs its remaining words as a command.
type launcher struct {
	valueFlags string // short flags that take a separate value
	privileged string // non-empty when the launcher elevates privileges
	positional int    // leading operands before the command (timeout DURATION)
	assigns    bool   // NAME=value words precede the command (env)
	extraArgs  bool   // appends unseen arguments (xargs)
}

var launchers = map[string]launcher{
	"env":     {valueFlags: "uSC", assigns: true},
	"time":    {},
	"nice":    {valueFlags: "n"},
	"nohup":   {},
	"command": {},
	"exec":    {valueFlags: "a"},
	"timeout": {valueFlags: "sk", positional: 1},
	"stdbuf":  {valueFlags: "ioe"},
	"xargs":   {valueFlags: "IinPLsdEa", extraArgs: true},
	"sudo":    {valueFlags: "ugpChDTUrt", privileged: "superuser command"},
	"doas":    {valueFlags: "uC", privileged: "privileged command execution"},
	"pkexec":  {valueFlags: "u", privileged: "privileged command execution"},
	"run0":    {valueFlags: "uD", privileged: "privileged command execution"},
}

// unwrap strips one launcher. It returns the privilege description even when
// no inner command remains (for example a bare `sudo -s`).
func unwrap(cmd shellCommand) (shellCommand, string, bool) {
	name := cmd.name()
	if name == "su" {
		return cmd, "superuser command", false
	}
	spec, ok := launchers[name]
	if !ok {
		return cmd, "", false
	}
	if name == "command" && slices.ContainsFunc(cmd.words[1:], func(w shellWord) bool { return w.text == "-v" || w.text == "-V" }) {
		return cmd, "", false
	}
	words := cmd.words[1:]
	positional := spec.positional
	for len(words) > 0 {
		text := words[0].text
		switch {
		case text == "--":
			words = words[1:]
		case strings.HasPrefix(text, "--"):
			words = words[1:]
			continue
		case strings.HasPrefix(text, "-") && len(text) > 1:
			words = words[1:]
			if len(text) == 2 && strings.ContainsRune(spec.valueFlags, rune(text[1])) && len(words) > 0 {
				words = words[1:]
			}
			continue
		case spec.assigns && strings.Contains(text, "=") && !strings.HasPrefix(text, "="):
			words = words[1:]
			continue
		case positional > 0:
			positional--
			words = words[1:]
			continue
		}
		break
	}
	if len(words) == 0 {
		return cmd, spec.privileged, false
	}
	return shellCommand{words: words, base: cmd.base, extraArgs: cmd.extraArgs || spec.extraArgs}, spec.privileged, true
}

// shellScript returns the -c script of a shell invocation.
func shellScript(cmd shellCommand) (shellWord, bool) {
	if !shells[cmd.name()] {
		return shellWord{}, false
	}
	words := cmd.words[1:]
	for i := 0; i < len(words); i++ {
		text := words[i].text
		switch {
		case text == "-o" || text == "+o" || text == "-O" || text == "+O":
			i++
		case strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") && strings.Contains(text, "c"):
			if i+1 < len(words) {
				return words[i+1], true
			}
			return shellWord{}, false
		case !strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "+"):
			return shellWord{}, false
		}
	}
	return shellWord{}, false
}

// findExec returns the command run by find -exec/-execdir/-ok/-okdir.
func findExec(cmd shellCommand) (shellCommand, bool) {
	if cmd.name() != "find" {
		return shellCommand{}, false
	}
	for i, w := range cmd.words {
		switch w.text {
		case "-exec", "-execdir", "-ok", "-okdir":
			end := len(cmd.words)
			for j := i + 1; j < len(cmd.words); j++ {
				if t := cmd.words[j].text; t == ";" || t == `\;` || t == "+" {
					end = j
					break
				}
			}
			if i+1 < end {
				return shellCommand{words: cmd.words[i+1 : end], base: cmd.base, extraArgs: true}, true
			}
		}
	}
	return shellCommand{}, false
}

// argSpec describes which operands of a command are paths and how they are
// used. Commands without a spec treat every path-like operand as a write.
type argSpec struct {
	op         string
	noPaths    bool              // operands are never paths (echo, printf)
	skip       int               // leading operands that are patterns or modes
	unskip     []string          // flags that supply the pattern instead
	dstLast    bool              // last operand is written, others read
	valueFlags string            // short flags that take a separate value
	longValues []string          // long options that take a separate value
	flagPaths  map[string]string // flag → op of its value when it is a path
}

var grepLongValues = []string{
	"--regexp", "--file", "--context", "--after-context", "--before-context", "--max-count",
	"--include", "--exclude", "--exclude-dir", "--label", "--color", "--colour",
}

var readSpec = argSpec{op: opRead}

var argSpecs = map[string]argSpec{
	"cat": readSpec, "less": readSpec, "more": readSpec, "wc": readSpec, "ls": {op: opRead, valueFlags: "IwT"},
	"stat": {op: opRead, valueFlags: "cf"}, "file": readSpec, "tree": {op: opRead, valueFlags: "LIP"}, "du": {op: opRead, valueFlags: "dB"},
	"df": readSpec, "nl": readSpec, "od": readSpec, "xxd": readSpec, "hexdump": readSpec, "strings": readSpec,
	"md5": readSpec, "md5sum": readSpec, "shasum": {op: opRead, valueFlags: "a"}, "sha256sum": readSpec,
	"cmp": readSpec, "diff": {op: opRead, valueFlags: "UCLIx"}, "uniq": readSpec, "paste": {op: opRead, valueFlags: "d"},
	"column": {op: opRead, valueFlags: "st"}, "fold": {op: opRead, valueFlags: "w"}, "fmt": readSpec, "rev": readSpec, "tac": readSpec,
	"realpath": readSpec, "readlink": readSpec, "cd": readSpec, "pushd": readSpec, "source": readSpec, ".": readSpec, "bat": readSpec,
	"head": {op: opRead, valueFlags: "nc"}, "tail": {op: opRead, valueFlags: "nc"},
	"cut":  {op: opRead, valueFlags: "dfcb"},
	"sort": {op: opRead, valueFlags: "ktST", flagPaths: map[string]string{"-o": opWrite, "--output": opWrite}},
	"echo": {noPaths: true}, "printf": {noPaths: true}, "date": {noPaths: true}, "true": {noPaths: true}, "false": {noPaths: true},
	"which": {noPaths: true}, "type": {noPaths: true}, "whereis": {noPaths: true}, "command": {noPaths: true}, "basename": {noPaths: true},
	"dirname": {noPaths: true}, "seq": {noPaths: true}, "sleep": {noPaths: true}, "expr": {noPaths: true}, "test": {noPaths: true},
	"[": {noPaths: true}, "printenv": {noPaths: true}, "export": {noPaths: true}, "unset": {noPaths: true}, "pwd": {noPaths: true},
	"id": {noPaths: true}, "whoami": {noPaths: true}, "uname": {noPaths: true}, "hostname": {noPaths: true}, "tr": {noPaths: true},
	"grep":    {op: opRead, skip: 1, unskip: []string{"-e", "-f", "--regexp", "--file"}, valueFlags: "ABCmefdD", longValues: grepLongValues},
	"egrep":   {op: opRead, skip: 1, unskip: []string{"-e", "-f"}, valueFlags: "ABCmefdD", longValues: grepLongValues},
	"fgrep":   {op: opRead, skip: 1, unskip: []string{"-e", "-f"}, valueFlags: "ABCmefdD", longValues: grepLongValues},
	"rg":      {op: opRead, skip: 1, unskip: []string{"-e", "-f", "--regexp", "--file", "--files", "--type-list"}, valueFlags: "ABCmefgtTMjEr", longValues: []string{"--regexp", "--file", "--type", "--type-not", "--glob", "--iglob", "--max-count", "--context", "--after-context", "--before-context", "--max-depth", "--threads", "--sort", "--sortr", "--type-add", "--color", "--colors", "--encoding", "--max-filesize", "--engine", "--replace"}},
	"ag":      {op: opRead, skip: 1, valueFlags: "ABCmGg"},
	"sed":     {op: opRead, skip: 1, unskip: []string{"-e", "-f", "--expression", "--file"}, valueFlags: "efl", longValues: []string{"--expression", "--file"}},
	"awk":     {op: opRead, skip: 1, unskip: []string{"-f", "--file"}, valueFlags: "fvF"},
	"gawk":    {op: opRead, skip: 1, unskip: []string{"-f", "--file"}, valueFlags: "fvF"},
	"jq":      {op: opRead, skip: 1, unskip: []string{"-f", "--from-file"}, valueFlags: "fL", longValues: []string{"--from-file", "--indent", "--arg", "--argjson", "--slurpfile", "--rawfile"}},
	"yq":      {op: opRead, skip: 1, valueFlags: "o"},
	"cp":      {op: opRead, dstLast: true, valueFlags: "tS", flagPaths: map[string]string{"-t": opWrite, "--target-directory": opWrite}},
	"mv":      {op: opWrite, dstLast: true, valueFlags: "tS", flagPaths: map[string]string{"-t": opWrite, "--target-directory": opWrite}},
	"ln":      {op: opRead, dstLast: true, valueFlags: "tS", flagPaths: map[string]string{"-t": opWrite, "--target-directory": opWrite}},
	"install": {op: opRead, dstLast: true, valueFlags: "tmogS", flagPaths: map[string]string{"-t": opWrite}},
	"chmod":   {op: opWrite, skip: 1}, "chown": {op: opWrite, skip: 1}, "chgrp": {op: opWrite, skip: 1},
	"truncate": {op: opWrite, valueFlags: "sr"}, "tee": {op: opWrite},
	"rm": {op: opWrite}, "rmdir": {op: opWrite}, "unlink": {op: opWrite}, "touch": {op: opWrite, valueFlags: "rdt"},
	"mkdir": {op: opWrite, valueFlags: "m"}, "shred": {op: opWrite, valueFlags: "nsu"},
	"curl": {noPaths: true, valueFlags: "oTKcDEHXuAexwmbrdF", flagPaths: map[string]string{
		"-o": opWrite, "--output": opWrite, "-T": opRead, "--upload-file": opRead, "-K": opRead, "--config": opRead,
		"-c": opWrite, "--cookie-jar": opWrite, "-D": opWrite, "--dump-header": opWrite, "--cacert": opRead, "-E": opRead, "--cert": opRead, "--key": opRead,
	}},
	"wget": {noPaths: true, valueFlags: "OPoiaU", flagPaths: map[string]string{
		"-O": opWrite, "--output-document": opWrite, "-P": opWrite, "--directory-prefix": opWrite, "-o": opWrite, "--output-file": opWrite,
		"-a": opWrite, "-i": opRead, "--input-file": opRead, "--post-file": opRead, "--body-file": opRead,
	}},
	"go": {op: opRead, valueFlags: "oC", flagPaths: map[string]string{"-o": opWrite, "-C": opRead}},
}

// curlDataFlags carry request bodies; a value starting with @ names a file.
var curlDataFlags = map[string]bool{
	"-d": true, "--data": true, "--data-binary": true, "--data-raw": true, "--data-urlencode": true,
	"--data-ascii": true, "-F": true, "--form": true, "--json": true,
}

type pathArg struct {
	word  shellWord
	op    string
	force bool
}

// commandPaths lists path operands of cmd with their operation. When
// movedBase is set, a preceding cd left the workspace, so every operand of a
// known path command names a file there even without a path-like spelling.
func commandPaths(cmd shellCommand, movedBase bool) []pathArg {
	name := cmd.name()
	if name == "git" {
		return gitPaths(cmd)
	}
	if name == "find" {
		return findPaths(cmd)
	}
	if name == "dd" {
		return ddPaths(cmd)
	}
	if name == "docker" || name == "podman" {
		return containerPaths(cmd)
	}
	spec, known := argSpecs[name]
	if !known {
		spec = argSpec{op: opWrite}
	}
	if name == "sed" && (hasShortFlag(cmd.texts()[1:], "i") || hasLongOption(cmd.texts(), "in-place")) {
		spec.op = opWrite
	}
	skip := spec.skip
	var out []pathArg
	var operands []shellWord
	words := cmd.words[1:]
	for i := 0; i < len(words); i++ {
		w := words[i]
		text := w.text
		if text == "--" {
			operands = append(operands, words[i+1:]...)
			break
		}
		if !strings.HasPrefix(text, "-") || text == "-" {
			operands = append(operands, w)
			continue
		}
		if slices.Contains(spec.unskip, text) || slices.ContainsFunc(spec.unskip, func(f string) bool { return strings.HasPrefix(text, f+"=") }) {
			skip = 0
		}
		flag, value, attached := splitFlag(text, spec.valueFlags)
		if name == "curl" && curlDataFlags[flag] {
			if !attached && i+1 < len(words) {
				i++
				value, attached = words[i].text, true
			}
			if file, ok := curlDataFile(value); ok {
				out = append(out, pathArg{word: shellWord{text: file, unresolved: w.unresolved || containsExpansion(file)}, op: opRead, force: true})
			}
			continue
		}
		op, isPath := spec.flagPaths[flag]
		takesValue := isPath || slices.Contains(spec.longValues, flag) || len(flag) == 2 && strings.ContainsRune(spec.valueFlags, rune(flag[1]))
		if takesValue && !attached && i+1 < len(words) {
			i++
			value, attached = words[i].text, true
			w = words[i]
		} else if attached {
			w = shellWord{text: value, unresolved: w.unresolved}
		}
		switch {
		case isPath && attached:
			out = append(out, pathArg{word: shellWord{text: value, unresolved: w.unresolved}, op: op, force: true})
		case !known && attached && isRootedWord(value):
			out = append(out, pathArg{word: shellWord{text: value, unresolved: w.unresolved}, op: spec.op})
		}
	}
	if spec.noPaths {
		return out
	}
	if skip > len(operands) {
		skip = len(operands)
	}
	operands = operands[skip:]
	for i, w := range operands {
		op := spec.op
		if spec.dstLast && i == len(operands)-1 && len(operands) > 1 {
			op = opWrite
		}
		out = append(out, pathArg{word: w, op: op, force: known && movedBase})
	}
	return out
}

// splitFlag separates an attached value: --out=x, or -ox for a value flag.
func splitFlag(text, valueFlags string) (flag, value string, attached bool) {
	if strings.HasPrefix(text, "--") {
		if name, value, ok := strings.Cut(text, "="); ok {
			return name, value, true
		}
		return text, "", false
	}
	if len(text) > 2 && strings.ContainsRune(valueFlags, rune(text[1])) {
		return text[:2], text[2:], true
	}
	return text, "", false
}

func curlDataFile(value string) (string, bool) {
	if _, after, ok := strings.Cut(value, "@"); ok && after != "" {
		file, _, _ := strings.Cut(after, ";")
		return file, file != "-"
	}
	return "", false
}

// commandWords is cmd as rules match it: the command's base name, and for
// git the subcommand directly after it, without global options.
func commandWords(cmd shellCommand) []string {
	words := cmd.texts()
	words[0] = cmd.name()
	if words[0] != "git" {
		return words
	}
	_, sub, args := gitInvocation(cmd)
	if sub == "" {
		return words[:1]
	}
	out := []string{"git", sub}
	for _, w := range args {
		out = append(out, w.text)
	}
	return out
}

// gitReadSubcommands only inspect the repository.
var gitReadSubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "blame": true, "grep": true, "ls-files": true,
	"rev-parse": true, "describe": true, "shortlog": true, "reflog": true, "remote": true, "config": true, "fetch": true,
}

// gitInvocation splits global options from the subcommand.
func gitInvocation(cmd shellCommand) (dir *shellWord, sub string, args []shellWord) {
	words := cmd.words[1:]
	for len(words) > 0 {
		text := words[0].text
		switch {
		case text == "-C" && len(words) > 1:
			w := words[1]
			dir = &w
			words = words[2:]
		case text == "-c" && len(words) > 1:
			words = words[2:]
		case strings.HasPrefix(text, "-"):
			words = words[1:]
		default:
			return dir, text, words[1:]
		}
	}
	return dir, "", nil
}

func gitPaths(cmd shellCommand) []pathArg {
	dir, sub, args := gitInvocation(cmd)
	op := opWrite
	if gitReadSubcommands[sub] {
		op = opRead
	}
	var out []pathArg
	if dir != nil {
		out = append(out, pathArg{word: *dir, op: op, force: true})
	}
	for _, w := range args {
		if !strings.HasPrefix(w.text, "-") && isRootedWord(w.text) {
			out = append(out, pathArg{word: w, op: op})
		}
	}
	return out
}

// findPaths returns find's start points; -delete makes them delete targets.
func findPaths(cmd shellCommand) []pathArg {
	op := opRead
	if slices.ContainsFunc(cmd.words, func(w shellWord) bool { return w.text == "-delete" }) {
		op = opWrite
	}
	var out []pathArg
	for _, w := range findStarts(cmd) {
		out = append(out, pathArg{word: w, op: op, force: true})
	}
	return out
}

func findStarts(cmd shellCommand) []shellWord {
	var starts []shellWord
	for _, w := range cmd.words[1:] {
		if strings.HasPrefix(w.text, "-") || w.text == "(" || w.text == "!" || w.text == `\(` {
			if len(starts) > 0 || !slices.Contains([]string{"-H", "-L", "-P"}, w.text) {
				break
			}
			continue
		}
		starts = append(starts, w)
	}
	return starts
}

func ddPaths(cmd shellCommand) []pathArg {
	var out []pathArg
	for _, w := range cmd.words[1:] {
		if value, ok := strings.CutPrefix(w.text, "if="); ok {
			out = append(out, pathArg{word: shellWord{text: value, unresolved: w.unresolved}, op: opRead, force: true})
		}
		if value, ok := strings.CutPrefix(w.text, "of="); ok && !isBlockDevice(value) {
			out = append(out, pathArg{word: shellWord{text: value, unresolved: w.unresolved}, op: opWrite, force: true})
		}
	}
	return out
}

// containerPaths returns host sources of bind mounts (-v SRC:DST,
// --mount source=SRC). A mounted directory is readable and writable.
func containerPaths(cmd shellCommand) []pathArg {
	var out []pathArg
	words := cmd.words[1:]
	for i := 0; i < len(words); i++ {
		flag, value, attached := splitFlag(words[i].text, "v")
		if flag != "-v" && flag != "--volume" && flag != "--mount" {
			continue
		}
		unresolved := words[i].unresolved
		if !attached && i+1 < len(words) {
			i++
			value, unresolved = words[i].text, words[i].unresolved
		}
		source := ""
		if flag == "--mount" {
			for _, field := range strings.Split(value, ",") {
				if key, v, ok := strings.Cut(field, "="); ok && (key == "source" || key == "src") {
					source = v
				}
			}
		} else {
			source, _, _ = strings.Cut(value, ":")
		}
		if isRootedWord(source) || strings.HasPrefix(source, ".") {
			out = append(out, pathArg{word: shellWord{text: source, unresolved: unresolved}, op: opWrite, force: true})
		}
	}
	return out
}

func isBlockDevice(path string) bool {
	return strings.HasPrefix(path, "/dev/") && !neutralDevice(path)
}

// classifyCommand returns the confirmations one unwrapped command needs.
// Path operands are checked separately against the workspace boundary.
func classifyCommand(cmd shellCommand) []finding {
	words := cmd.texts()
	add := func(rule, desc string) []finding {
		return []finding{{rule: rule, desc: desc, pattern: cmd.display(), words: commandWords(cmd)}}
	}
	name := cmd.name()
	if name == "mkfs" || strings.HasPrefix(name, "mkfs.") {
		return add(RuleBashDisk, "filesystem format")
	}
	if runsAICE(words) && !aiceReadOnly(words) {
		return add(RuleControlFile, "runs AICE, which can change its own configuration and permissions")
	}
	switch name {
	case "dd":
		for _, w := range words[1:] {
			if value, ok := strings.CutPrefix(w, "of="); ok && isBlockDevice(value) {
				return add(RuleBashDisk, "raw device write")
			}
		}
	case "wipefs", "blkdiscard", "fdisk", "sfdisk", "cfdisk", "parted", "sgdisk":
		return add(RuleBashDisk, "disk partitioning or wipe")
	case "diskutil":
		if len(words) > 1 && (strings.HasPrefix(words[1], "erase") || strings.HasPrefix(words[1], "partition") || strings.HasSuffix(words[1], "Disk") || words[1] == "secureErase") {
			return add(RuleBashDisk, "disk erase or partitioning")
		}
	case "docker", "podman":
		if len(words) > 1 && (words[1] == "run" || words[1] == "create") && hasLongOption(words, "privileged") {
			return add(RuleBashPrivileged, "container with privileged mode")
		}
		if len(words) > 1 && words[1] == "push" {
			return add(RuleBashOutbound, "image push")
		}
	case "chmod":
		recursive := hasShortFlag(words[1:], "R") || hasLongOption(words, "recursive")
		if recursive && slices.ContainsFunc(words, func(w string) bool {
			return slices.Contains([]string{"777", "0777", "a+rwx", "ugo+rwx", "7777", "1777"}, w)
		}) {
			return add(RuleBashPermissions, "insecure recursive permissions")
		}
	case "chown", "chgrp":
		if hasShortFlag(words[1:], "R") || hasLongOption(words, "recursive") {
			return add(RuleBashPermissions, "recursive ownership change")
		}
	case "shred":
		return add(RuleBashDelete, "secure file overwrite")
	case "rm":
		if hasShortFlag(words[1:], "r") || hasShortFlag(words[1:], "R") || hasLongOption(words, "recursive") || hasLongOption(words, "dir") {
			return []finding{{rule: RuleBashDelete, desc: "recursive delete", pattern: cmd.display(), words: commandWords(cmd), scoped: true, deleteTargets: operandsOf(cmd), base: cmd.base, extraArgs: cmd.extraArgs}}
		}
	case "find":
		if slices.Contains(words, "-delete") {
			filtered := slices.ContainsFunc(words, func(w string) bool {
				return slices.Contains([]string{"-name", "-iname", "-path", "-ipath", "-regex", "-iregex", "-type", "-newer", "-mtime", "-mmin", "-size", "-empty"}, w)
			})
			return []finding{{rule: RuleBashDelete, desc: "find delete", pattern: cmd.display(), words: commandWords(cmd), scoped: true, deleteTargets: findStarts(cmd), base: cmd.base, filtered: filtered}}
		}
	case "git":
		return gitFindings(cmd, add)
	case "curl":
		if curlUploads(words) {
			return add(RuleBashOutbound, "HTTP upload")
		}
	case "wget":
		if slices.ContainsFunc(words, func(w string) bool {
			return strings.HasPrefix(w, "--post-") || strings.HasPrefix(w, "--body-") || strings.HasPrefix(w, "--method=") && !strings.EqualFold(w, "--method=GET") && !strings.EqualFold(w, "--method=HEAD")
		}) {
			return add(RuleBashOutbound, "HTTP upload")
		}
	case "scp", "sftp", "ssh", "nc", "ncat", "netcat", "telnet", "socat", "ftp":
		return add(RuleBashOutbound, "remote connection")
	case "rsync":
		if slices.ContainsFunc(words[1:], isRemoteSpec) {
			return add(RuleBashOutbound, "remote sync")
		}
	case "npm", "pnpm", "yarn", "bun", "cargo":
		if slices.Contains(words[1:], "publish") {
			return add(RuleBashOutbound, "package publish")
		}
	case "gem":
		if len(words) > 1 && words[1] == "push" {
			return add(RuleBashOutbound, "package publish")
		}
	case "twine":
		if len(words) > 1 && words[1] == "upload" {
			return add(RuleBashOutbound, "package publish")
		}
	case "gh":
		if ghMutates(words) {
			return add(RuleBashOutbound, "GitHub change")
		}
	}
	return nil
}

func operandsOf(cmd shellCommand) []shellWord {
	var out []shellWord
	afterDashes := false
	for _, w := range cmd.words[1:] {
		if !afterDashes && w.text == "--" {
			afterDashes = true
			continue
		}
		if afterDashes || !strings.HasPrefix(w.text, "-") {
			out = append(out, w)
		}
	}
	return out
}

func gitFindings(cmd shellCommand, add func(rule, desc string) []finding) []finding {
	_, sub, args := gitInvocation(cmd)
	texts := make([]string, len(args))
	for i, w := range args {
		texts[i] = w.text
	}
	// An alias can run any git subcommand or shell command under a new name.
	for i, w := range cmd.words[:len(cmd.words)-len(args)] {
		if w.text == "-c" && i+1 < len(cmd.words) && strings.HasPrefix(cmd.words[i+1].text, "alias.") {
			return add(RuleBashDynamic, "inline git alias")
		}
	}
	if sub == "config" && slices.ContainsFunc(texts, func(w string) bool { return strings.HasPrefix(w, "alias.") }) {
		return add(RuleBashDynamic, "defines a git alias")
	}
	switch sub {
	case "push":
		if hasShortFlag(texts, "f") || hasLongOption(texts, "force") || hasLongOption(texts, "force-with-lease") ||
			slices.ContainsFunc(texts, func(w string) bool { return strings.HasPrefix(w, "+") }) {
			return add(RuleBashOutbound, "force push")
		}
		return add(RuleBashOutbound, "git push")
	case "reset":
		if slices.Contains(texts, "--hard") {
			return add(RuleBashLoseWork, "discard uncommitted changes")
		}
	case "clean":
		if hasShortFlag(texts, "f") || hasLongOption(texts, "force") {
			return add(RuleBashLoseWork, "delete untracked files")
		}
	case "checkout", "restore":
		if !slices.Contains(texts, "--staged") && slices.Contains(texts, ".") {
			return add(RuleBashLoseWork, "discard working tree changes")
		}
	case "stash":
		if len(texts) > 0 && (texts[0] == "drop" || texts[0] == "clear") {
			return add(RuleBashLoseWork, "drop stashed changes")
		}
	case "branch":
		if slices.Contains(texts, "-D") {
			return add(RuleBashLoseWork, "force delete branch")
		}
	}
	return nil
}

func curlUploads(words []string) bool {
	for i, w := range words[1:] {
		flag, value, attached := splitFlag(w, "XdFT")
		switch {
		case curlDataFlags[flag] || flag == "-T" || flag == "--upload-file":
			return true
		case strings.HasPrefix(w, "--data") || strings.HasPrefix(w, "--form"):
			return true
		case flag == "-X" || flag == "--request":
			if !attached && i+2 < len(words) {
				value = words[i+2]
			}
			if method := strings.ToUpper(value); method != "" && method != "GET" && method != "HEAD" && method != "OPTIONS" {
				return true
			}
		}
	}
	return false
}

// runsAICE reports an invocation of the AICE binary, directly or through
// `go run` of its main package.
func runsAICE(words []string) bool {
	if filepath.Base(words[0]) == "aice" {
		return true
	}
	return filepath.Base(words[0]) == "go" && len(words) > 2 && words[1] == "run" &&
		slices.ContainsFunc(words[2:], func(w string) bool {
			return strings.TrimSuffix(filepath.ToSlash(w), "/") == "./cmd/aice" || strings.HasSuffix(w, "/cmd/aice")
		})
}

// aiceReadOnly lists AICE invocations that cannot change configuration,
// permissions, credentials or start another agent.
func aiceReadOnly(words []string) bool {
	args := words[1:]
	if filepath.Base(words[0]) == "go" {
		args = words[3:]
	}
	if slices.ContainsFunc(args, func(w string) bool { return w == "--help" || w == "-h" }) {
		return true
	}
	switch strings.Join(args, " ") {
	case "--version", "-v", "help", "mcp status", "session tree":
		return true
	}
	return len(args) == 3 && args[0] == "mcp" && args[1] == "status"
}

// isRemoteSpec reports rsync/scp style host:path or rsync:// operands.
func isRemoteSpec(w string) bool {
	if strings.HasPrefix(w, "rsync://") {
		return true
	}
	if strings.HasPrefix(w, "-") || strings.HasPrefix(w, "/") || strings.HasPrefix(w, ".") || windowsPathLike(w) {
		return false
	}
	host, _, ok := strings.Cut(w, ":")
	return ok && host != "" && !strings.Contains(host, "/")
}

var ghMutatingVerbs = map[string]bool{
	"create": true, "edit": true, "merge": true, "close": true, "reopen": true, "comment": true, "delete": true,
	"review": true, "upload": true, "run": true, "rerun": true, "cancel": true, "fork": true, "ready": true,
	"lock": true, "unlock": true, "transfer": true, "archive": true, "rename": true, "set": true, "enable": true, "disable": true,
}

func ghMutates(words []string) bool {
	if len(words) < 2 {
		return false
	}
	if words[1] == "api" {
		for i, w := range words[2:] {
			flag, value, attached := splitFlag(w, "XfF")
			switch flag {
			case "-f", "-F", "--field", "--raw-field", "--input":
				return true
			case "-X", "--method":
				if !attached && i+3 < len(words) {
					value = words[i+3]
				}
				if method := strings.ToUpper(value); method != "" && method != "GET" {
					return true
				}
			}
		}
		return false
	}
	return len(words) > 2 && ghMutatingVerbs[words[2]]
}
