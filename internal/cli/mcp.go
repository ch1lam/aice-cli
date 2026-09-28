package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

type MCPRequest struct {
	Workspace            string
	ProjectTrustOverride *bool
	Operation            interaction.MCPRequest
	Auth                 *interaction.AuthInteraction
	NoBrowser            bool
}

type MCPManager interface {
	ManageMCP(context.Context, MCPRequest) (interaction.MCPResult, error)
}

func newMCPCommand(manager MCPManager) *cobra.Command {
	var workspace string
	var trustProject, distrustProject bool
	root := &cobra.Command{Use: "mcp", Short: "Manage MCP services and connection approval", Args: func(c *cobra.Command, args []string) error { return newUsageError(cobra.NoArgs(c, args)) }}
	root.PersistentFlags().StringVar(&workspace, "workspace", ".", "workspace whose trusted project configuration to inspect")
	root.PersistentFlags().BoolVar(&trustProject, "trust-project", false, "load this project's configuration for this command; does not approve connections")
	root.PersistentFlags().BoolVar(&distrustProject, "no-trust-project", false, "ignore this project's configuration")
	for _, action := range []string{"status", "add", "replace", "enable", "disable", "remove", "approve", "deny", "forget", "credential", "login", "logout", "connect", "reconnect", "permissions", "permission"} {
		var fingerprint string
		var clearSecret bool
		var noBrowser bool
		var permission interaction.MCPPermission
		var permissionScope string
		use := action + " KEY"
		if action == "status" {
			use = "status [KEY]"
		}
		if action == "add" {
			use = "add ID"
		}
		if action == "credential" {
			use = "credential KEY SLOT"
		}
		descriptions := map[string]string{
			"status":      "Show configured connection details and fingerprints without connecting",
			"add":         "Read one server definition as JSON from stdin; save without connecting",
			"replace":     "Replace a user server from JSON on stdin; save without connecting",
			"enable":      "Enable a user server without granting connection or tool permission",
			"disable":     "Disable a user server",
			"remove":      "Remove a user server definition",
			"approve":     "Approve the reviewed connection fingerprint; does not grant tools",
			"deny":        "Deny the reviewed connection fingerprint, including under --yolo",
			"forget":      "Remove a saved connection decision",
			"credential":  "Read a service-scoped credential from stdin; does not connect",
			"login":       "Authorize this MCP service in a browser; new login requires new connection approval",
			"logout":      "Remove this service's OAuth login and connection approval",
			"connect":     "Test initialization and supported catalog discovery using prior connection approval",
			"reconnect":   "Establish and test a fresh owned connection using prior approval",
			"permissions": "Discover operation names and schema fingerprints for explicit user rules; never executes tools",
			"permission":  "Save or remove one identity-bound user permission; does not connect",
		}
		command := &cobra.Command{Use: use, Short: descriptions[action], Args: func(c *cobra.Command, args []string) error {
			if strings.TrimSpace(workspace) == "" || trustProject && distrustProject {
				return newUsageError(fmt.Errorf("workspace is required and project trust flags are mutually exclusive"))
			}
			validator := cobra.ExactArgs(1)
			if action == "status" {
				validator = cobra.MaximumNArgs(1)
			}
			if action == "credential" {
				validator = cobra.ExactArgs(2)
			}
			return newUsageError(validator(c, args))
		}, RunE: func(c *cobra.Command, args []string) error {
			request := interaction.MCPRequest{Action: action, Fingerprint: fingerprint}
			if action == "permission" {
				request.Permission, request.PermissionScope = &permission, permissionScope
			}
			if len(args) > 0 {
				request.Key = args[0]
			}
			if action == "add" || action == "replace" {
				data, err := readMCPInput(c.InOrStdin(), 1<<20)
				if err != nil {
					return err
				}
				request.Definition = data
			}
			if action == "credential" {
				request.Slot = args[1]
				if !clearSecret {
					data, err := readMCPInput(c.InOrStdin(), 8192)
					if err != nil {
						return err
					}
					request.Secret = strings.TrimRight(string(data), "\r\n")
					if request.Secret == "" {
						return newUsageError(fmt.Errorf("credential must not be empty; use --clear to remove it"))
					}
				}
			}
			var auth *interaction.AuthInteraction
			if action == "login" {
				auth = &interaction.AuthInteraction{Notify: func(ctx context.Context, prompt interaction.AuthPrompt) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					_, err := fmt.Fprintf(c.ErrOrStderr(), "%s\n%s\n%s\n", prompt.Title, prompt.URL, prompt.Instructions)
					return err
				}}
			}
			result, err := manager.ManageMCP(c.Context(), MCPRequest{Workspace: workspace, ProjectTrustOverride: projectTrustOverride(trustProject, distrustProject), Operation: request, Auth: auth, NoBrowser: noBrowser})
			if err == nil || result.Committed || len(result.Services) > 0 {
				encoder := json.NewEncoder(c.OutOrStdout())
				encoder.SetIndent("", "  ")
				if writeErr := encoder.Encode(result); writeErr != nil {
					return fmt.Errorf("write MCP result: %w", writeErr)
				}
			}
			return err
		}}
		if action == "approve" || action == "deny" || action == "forget" || action == "credential" || action == "login" || action == "logout" || action == "permission" {
			command.Flags().StringVar(&fingerprint, "fingerprint", "", "exact fingerprint from mcp status KEY")
			_ = command.MarkFlagRequired("fingerprint")
		}
		if action == "permission" {
			command.Flags().StringVar(&permission.Tool, "tool", "", "exact remote tool name (resources/read for resource access)")
			command.Flags().StringVar(&permission.Operation, "operation", "", "empty for tools; resources/read for service-wide resource reading")
			command.Flags().StringVar(&permission.SchemaFingerprint, "schema-fingerprint", "", "exact schema fingerprint from mcp permissions KEY")
			command.Flags().StringVar(&permission.Decision, "decision", "", "allow, deny, or ask (remove the saved rule)")
			command.Flags().StringVar(&permissionScope, "scope", "", "exact permission_scope from mcp permissions or status")
			for _, flag := range []string{"tool", "schema-fingerprint", "decision", "scope"} {
				_ = command.MarkFlagRequired(flag)
			}
		}
		if action == "login" {
			command.Flags().BoolVar(&noBrowser, "no-browser", false, "display the authorization URL without opening a browser")
		}
		if action == "credential" {
			command.Flags().BoolVar(&clearSecret, "clear", false, "remove this stored credential slot without reading stdin")
		}
		root.AddCommand(command)
	}
	return root
}

func readMCPInput(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read MCP input failed")
	}
	if int64(len(data)) > limit {
		return nil, newUsageError(fmt.Errorf("MCP input exceeds its byte limit"))
	}
	return data, nil
}
