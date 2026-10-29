package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/TuanFaied/rtogtor/engine/protocol"
)

func newServeCmd(_ *options) *cobra.Command {
	var stdio bool
	cmd := &cobra.Command{
		Use:   "serve --stdio",
		Short: "Serve the SDK protocol over stdin/stdout (used by the SDKs)",
		Long: `Serve the engine ↔ SDK protocol (specs/protocol.md): JSON Lines requests on
stdin, responses and events on stdout, diagnostics on stderr. The session ends
on a shutdown request or when stdin closes.

Credentials are never part of protocol messages; connections name environment
variables of this process.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdio {
				return fmt.Errorf("--stdio is required (it is the only transport in protocol 1.0)")
			}
			s := protocol.NewServer(cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err := s.Serve(cmd.Context(), os.Stdin); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "rtogtor serve: %v\n", err)
				return &exitError{code: ExitFailure}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", false, "use stdin/stdout")
	return cmd
}
