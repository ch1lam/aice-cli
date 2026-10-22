package guard

import "strings"

// structuralMatcher returns a description when a parsed command's word list
// is dangerous. Matching parsed words avoids false positives like `echo rm -rf`.
type structuralMatcher func(words []string) string

func rmMatcher(words []string) string {
	if len(words) == 0 || words[0] != "rm" {
		return ""
	}
	hasRecursive := hasShortFlag(words, "r") || hasShortFlag(words, "R") || hasLongOption(words, "recursive") || hasLongOption(words, "dir")
	hasForce := hasShortFlag(words, "f") || hasLongOption(words, "force")
	if hasRecursive && hasForce {
		return "recursive force delete"
	}
	return ""
}

func shredMatcher(words []string) string {
	if len(words) > 0 && words[0] == "shred" {
		return "secure file overwrite"
	}
	return ""
}
func sudoMatcher(words []string) string {
	if len(words) > 0 && words[0] == "sudo" {
		return "superuser command"
	}
	return ""
}
func doasMatcher(words []string) string {
	if len(words) > 0 && words[0] == "doas" {
		return "privileged command execution"
	}
	return ""
}
func pkexecMatcher(words []string) string {
	if len(words) > 0 && words[0] == "pkexec" {
		return "privileged command execution"
	}
	return ""
}
func ddMatcher(words []string) string {
	if len(words) == 0 || words[0] != "dd" {
		return ""
	}
	if hasArg(words, "of=") {
		return "disk write operation"
	}
	return ""
}
func mkfsMatcher(words []string) string {
	if len(words) == 0 {
		return ""
	}
	cmd := words[0]
	if cmd == "mkfs" || strings.HasPrefix(cmd, "mkfs.") {
		return "filesystem format"
	}
	return ""
}
func wipefsMatcher(words []string) string {
	if len(words) > 0 && words[0] == "wipefs" {
		return "filesystem signature wipe"
	}
	return ""
}
func blkdiscardMatcher(words []string) string {
	if len(words) > 0 && words[0] == "blkdiscard" {
		return "block device discard"
	}
	return ""
}
func fdiskMatcher(words []string) string {
	if len(words) == 0 {
		return ""
	}
	cmd := words[0]
	if cmd == "fdisk" || cmd == "sfdisk" || cmd == "cfdisk" {
		return "disk partitioning"
	}
	return ""
}
func partedMatcher(words []string) string {
	if len(words) == 0 {
		return ""
	}
	cmd := words[0]
	if cmd == "parted" || cmd == "sgdisk" {
		return "disk partitioning"
	}
	return ""
}
func chmodMatcher(words []string) string {
	if len(words) == 0 || words[0] != "chmod" {
		return ""
	}
	hasRecursive := hasShortFlag(words, "R") || hasLongOption(words, "recursive")
	hasWorldWritable := false
	for _, w := range words {
		if w == "777" || w == "0777" || w == "a+rwx" || w == "ugo+rwx" || w == "7777" || w == "1777" {
			hasWorldWritable = true
			break
		}
	}
	if hasRecursive && hasWorldWritable {
		return "insecure recursive permissions"
	}
	return ""
}
func chownMatcher(words []string) string {
	if len(words) == 0 || words[0] != "chown" {
		return ""
	}
	if hasShortFlag(words, "R") || hasLongOption(words, "recursive") {
		return "recursive ownership change"
	}
	return ""
}
func containerMatcher(words []string) string {
	if len(words) < 2 {
		return ""
	}
	cmd := words[0]
	if cmd != "docker" && cmd != "podman" {
		return ""
	}
	sub := words[1]
	if sub != "run" && sub != "create" {
		return ""
	}
	for _, w := range words {
		if w == "--privileged" || strings.HasPrefix(w, "--privileged=") {
			return "container with privileged mode"
		}
	}
	return ""
}

var builtinStructuralMatchers = []structuralMatcher{
	rmMatcher, shredMatcher, sudoMatcher, doasMatcher, pkexecMatcher,
	ddMatcher, mkfsMatcher, wipefsMatcher, blkdiscardMatcher,
	fdiskMatcher, partedMatcher, chmodMatcher, chownMatcher, containerMatcher,
}

// structuralDangerousMatch checks each parsed command's word list against
// builtin structural matchers. Returns description and matched pattern when hit.
func structuralDangerousMatch(command string) (string, string) {
	calls := parseCallWords(command)
	for _, words := range calls {
		for _, m := range builtinStructuralMatchers {
			if desc := m(words); desc != "" {
				// Use the raw command prefix as pattern for reason formatting
				pat := strings.Join(words, " ")
				if len(pat) > 60 {
					pat = pat[:60] + "..."
				}
				return desc, pat
			}
		}
	}
	return "", ""
}
