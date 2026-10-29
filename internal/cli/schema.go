package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/output"
)

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema [command]",
		Short: "Print the JSON command and error contract",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			doc := buildSchema(cmd.Root())
			if len(args) == 1 {
				found := findCommand(doc.Commands, args[0])
				if found == nil {
					return clierror.Usage("unknown command \""+args[0]+"\"", "run gplay schema")
				}
				one := doc
				one.Commands = []commandSchema{*found}
				return output.JSON(cmd.OutOrStdout(), one)
			}
			return output.JSON(cmd.OutOrStdout(), doc)
		},
	}
}

type schemaDocument struct {
	Tool      string          `json:"tool"`
	Module    string          `json:"module"`
	Version   string          `json:"version"`
	Commands  []commandSchema `json:"commands"`
	ExitCodes []exitSchema    `json:"exit_codes"`
	Error     errorSchema     `json:"error"`
}

type commandSchema struct {
	Name     string          `json:"name"`
	Use      string          `json:"use"`
	Short    string          `json:"short"`
	Success  string          `json:"success,omitempty"`
	Flags    []flagSchema    `json:"flags,omitempty"`
	Children []commandSchema `json:"children,omitempty"`
}

type flagSchema struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Usage   string `json:"usage"`
	Default string `json:"default,omitempty"`
}

type exitSchema struct {
	Code      string `json:"code"`
	ExitCode  int    `json:"exit_code"`
	Retryable bool   `json:"retryable"`
	Meaning   string `json:"meaning"`
}

type errorSchema struct {
	Required []string `json:"required"`
	Optional []string `json:"optional"`
	Note     string   `json:"note"`
}

func buildSchema(root *cobra.Command) schemaDocument {
	src := clierror.Codes()
	exits := make([]exitSchema, len(src))
	for i, c := range src {
		exits[i] = exitSchema{Code: c.Code, ExitCode: c.ExitCode, Retryable: c.Retryable, Meaning: c.Meaning}
	}
	return schemaDocument{
		Tool:      toolID,
		Module:    modulePath,
		Version:   buildVersion,
		Commands:  commandTree(root),
		ExitCodes: exits,
		Error: errorSchema{
			Required: []string{"code", "exit_code", "message", "retryable"},
			Optional: []string{"hint", "http_status", "google_status", "google_reason", "edit_id", "request_id"},
			Note:     "One JSON object, last line of stderr, on every non-zero exit. Optional fields are omitted, never null. stdout is empty.",
		},
	}
}

func commandTree(cmd *cobra.Command) []commandSchema {
	var out []commandSchema
	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		out = append(out, describe(c))
	}
	return out
}

func describe(cmd *cobra.Command) commandSchema {
	s := commandSchema{
		Name:    cmd.Name(),
		Use:     cmd.Use,
		Short:   cmd.Short,
		Success: cmd.Annotations["success"],
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		s.Flags = append(s.Flags, flagSchema{
			Name:    f.Name,
			Type:    f.Value.Type(),
			Usage:   f.Usage,
			Default: f.DefValue,
		})
	})
	s.Children = commandTree(cmd)
	return s
}

func findCommand(cmds []commandSchema, name string) *commandSchema {
	for i := range cmds {
		if cmds[i].Name == name {
			return &cmds[i]
		}
		if found := findCommand(cmds[i].Children, name); found != nil {
			return found
		}
	}
	return nil
}
