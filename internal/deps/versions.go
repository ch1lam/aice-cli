package deps

// Dependency pins live in one file so bumping a helper is a single reviewable
// change. To bump one: update the version/tag below, the asset names, and the
// pinned SHA-256 (recompute from the upstream published checksum).
const (
	// ripgrepVersion is the pinned ripgrep release (tag name is the bare
	// version, no "v" prefix).
	ripgrepVersion = "15.2.0"

	// gitForWindowsTag and portableGitAsset pin the Git for Windows Portable
	// edition, which ships bash.exe and git.exe without an installer.
	gitForWindowsTag = "v2.55.0.windows.3"
	portableGitAsset = "PortableGit-2.55.0.3-64-bit.7z.exe"

	// portableGitSHA256 is the published SHA-256 of portableGitAsset.
	portableGitSHA256 = "ab00566336b5472120f9a52d34f2e79c5406535792acb0548001ffd0bd090e5d"
)

// ripgrepAsset maps goos/goarch to the ripgrep release asset that contains the
// rg binary for that platform.
var ripgrepAsset = map[string]string{
	"darwin/amd64":  "ripgrep-15.2.0-x86_64-apple-darwin.tar.gz",
	"darwin/arm64":  "ripgrep-15.2.0-aarch64-apple-darwin.tar.gz",
	"linux/amd64":   "ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz",
	"linux/arm64":   "ripgrep-15.2.0-aarch64-unknown-linux-musl.tar.gz",
	"windows/amd64": "ripgrep-15.2.0-x86_64-pc-windows-msvc.zip",
	"windows/arm64": "ripgrep-15.2.0-aarch64-pc-windows-msvc.zip",
}

// ripgrepSHA256 pins the published SHA-256 of each ripgrepAsset so a tampered
// or truncated download is rejected before it is executed.
var ripgrepSHA256 = map[string]string{
	"darwin/amd64":  "af7825fcc69a2afc7a7aea55fc9af90e26421d8f20fe59df32e233c0b8a231c1",
	"darwin/arm64":  "3750b2e93f37e0c692657da574d7019a101c0084da05a790c83fd335bad973e4",
	"linux/amd64":   "33e15bcf1624b25cdd2a55813a47a2f95dbe126268203e76aa6a585d1e7b149c",
	"linux/arm64":   "800b1e7206afe799dfb5a6901f23147cfaabe0e52210538100f61e86e1740915",
	"windows/amd64": "71b2fef860abe467217a538ff31de02f5258807c0129f771846f87bd029aafc5",
	"windows/arm64": "e4abca10c3a64ebea742667dd7009449d49403db5460dd6873e389fa2945360f",
}

func ripgrepDownloadURL(base, asset string) string {
	return base + "/BurntSushi/ripgrep/releases/download/" +
		ripgrepVersion + "/" + asset
}

func portableGitDownloadURL(base string) string {
	return base + "/git-for-windows/git/releases/download/" +
		gitForWindowsTag + "/" + portableGitAsset
}
