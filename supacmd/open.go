package supacmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/supatree/pkg/supatree"
	"github.com/panamafrancis/workbench/pkg/zellij"
)

var (
	openAgent      string
	openModel      string
	openRepo       string
	openSession    string
	openBackground bool
)

var openCmd = &cobra.Command{
	Use:   "open [name]",
	Short: "Open (or resume) an agent in a supatree's Zellij tab",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := resolveTreeName(args)
		if err != nil {
			return err
		}
		inst, err := supatree.Get(stCfg, name)
		if err != nil {
			return err
		}
		if openRepo != "" {
			return openMemberAgent(inst)
		}
		return openRootAgent(inst)
	},
}

// openRootAgent opens or resumes a named agent running at the supatree root.
func openRootAgent(inst *supatree.Instance) error {
	ensureZellij()
	// A background open (the watcher acting on the PM's start_agent) leaves
	// the focus where it was: OpenRootAgent puts it back (Wake).
	opts := supatree.OpenOptions{Model: openModel, Wake: openBackground}
	if openBackground {
		// The watcher reads this line to tell the PM what the launch came to,
		// and stops waiting once it has it: printed before the focus watch, it
		// does not hold up the launch queue.
		opts.Opened = func(outcome supatree.LaunchOutcome, _ error) {
			fmt.Println(supatree.OpenOutcomePrefix + string(outcome))
		}
	}
	_, err := supatree.OpenRootAgent(inst, stCfg, supatreeWorkspace(), stCfg.ResolveSidebarWidth(), openAgent, opts, os.Stderr)
	return err
}

// openMemberAgent opens an agent scoped to a single member repo.
func openMemberAgent(inst *supatree.Instance) error {
	ensureZellij()
	_, err := supatree.OpenMemberAgent(inst, stCfg, supatreeWorkspace(), stCfg.ResolveSidebarWidth(), openRepo, openModel)
	return err
}

// ensureZellij makes sure we can open a tab: it honors --session and otherwise
// requires being inside a Zellij session, exiting with guidance when not.
func ensureZellij() {
	if openSession != "" {
		_ = os.Setenv("ZELLIJ_SESSION_NAME", openSession)
		return
	}
	if !zellij.IsInZellij() {
		fmt.Fprintln(os.Stderr, "supatree: not inside a Zellij session.")
		fmt.Fprintln(os.Stderr, "Start one with: supatree start")
		os.Exit(1)
	}
}

func init() {
	openCmd.Flags().StringVar(&openAgent, "agent", "main", "agent name (each is an independently-resumable session at the root)")
	openCmd.Flags().StringVar(&openModel, "model", "", "model override (default: supatree's model)")
	openCmd.Flags().StringVar(&openRepo, "repo", "", "open an agent scoped to a single member repo instead of the root")
	openCmd.Flags().StringVar(&openSession, "session", "", "target a specific Zellij session")
	openCmd.Flags().BoolVar(&openBackground, "background", false, "return focus to the previously focused tab after opening")
}
