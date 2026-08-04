package cli

// Version is stamped at build time with
//
//	-ldflags "-X github.com/ch1lam/aice-cli/internal/cli.Version=<version>"
//
// and reported by the root command's --version flag. Dev builds keep "dev".
var Version = "dev"
