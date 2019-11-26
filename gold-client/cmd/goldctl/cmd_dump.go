package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.skia.org/infra/gold-client/go/goldclient"
)

// dumpEnv provides the environment for the dump command.
type dumpEnv struct {
	flagDumpHashes   bool
	flagDumpBaseline bool

	flagWorkDir string
}

// getDumpCmd returns the definition of the dump command.
func getDumpCmd() *cobra.Command {
	env := &dumpEnv{}
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Output information about the tests/images",
		Long: `
Output information such as the baselines and known hashes
that have been downloaded from the server.

Only has output after goldctl imgtest init or goldctl imgtest add
has been run.
`,
		Run: env.runDumpCmd,
	}

	cmd.Flags().BoolVar(&env.flagDumpHashes, "hashes", false, "Dump the (potentially long) list of hashes that have been seen before.")
	cmd.Flags().BoolVar(&env.flagDumpBaseline, "baseline", true, "Dump the baseline.")

	// add the workdir flag and make it required
	cmd.Flags().StringVar(&env.flagWorkDir, fstrWorkDir, "", "Work directory for intermediate results")
	Must(cmd.MarkFlagRequired(fstrWorkDir))

	return cmd
}

// runDumpCmd executes the dump logic - it loads the previous setup
// from disk and dumps out the information.
func (d *dumpEnv) runDumpCmd(cmd *cobra.Command, args []string) {
	auth, err := goldclient.LoadAuthOpt(d.flagWorkDir)
	ifErrLogExit(cmd, err)

	if auth == nil {
		logErrf(cmd, "Auth is empty - did you call goldctl auth first?")
		exitProcess(cmd, 1)
	}

	// the user is presumed to have called init first, so we can just load it
	goldClient, err := goldclient.LoadCloudClient(auth, d.flagWorkDir)
	ifErrLogExit(cmd, err)

	if d.flagDumpBaseline {
		b, err := goldClient.DumpBaseline()
		ifErrLogExit(cmd, err)
		fmt.Printf("Baseline: \n%s\n", b)
	}

	if d.flagDumpHashes {
		h, err := goldClient.DumpKnownHashes()
		ifErrLogExit(cmd, err)
		fmt.Printf("Known Hashes: \n%s\n", h)
	}
}
