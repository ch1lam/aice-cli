package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/classify"
)

// newAutoModeCommand prints the built-in classifier template that the
// auto_mode settings extend.
func newAutoModeCommand() *cobra.Command {
	root := &cobra.Command{Use: "auto-mode", Short: "Inspect the auto mode classifier", Args: cobra.NoArgs}
	root.AddCommand(&cobra.Command{
		Use:   "defaults",
		Short: "Print the built-in block rules and allow exceptions that auto_mode extends",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			out := command.OutOrStdout()
			fmt.Fprintf(out, "Prompt version: %s\n\nBlock rules:\n", classify.PromptVersion)
			for _, rule := range classify.DefaultBlockRules {
				fmt.Fprintf(out, "- %s\n", rule)
			}
			fmt.Fprintln(out, "\nAllow exceptions:")
			for _, exception := range classify.DefaultAllowExceptions {
				fmt.Fprintf(out, "- %s\n", exception)
			}
			fmt.Fprintln(out, "\nThe trusted environment is the workspace, its git branch and remotes.")
			fmt.Fprintln(out, "auto_mode.environment, auto_mode.block and auto_mode.allow in settings append to these lists.")
			return nil
		},
	})
	return root
}
