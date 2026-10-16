package supacmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/supatree/pkg/supatree"
)

var (
	rmForce bool
	rmPush  bool
	rmYes   bool
)

var rmCmd = &cobra.Command{
	Use:   "rm [name]",
	Short: "Remove a supatree (all member worktrees + branches)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := resolveTreeName(args)
		if err != nil {
			return err
		}
		inst, err := supatree.Get(stCfg, name)
		if err != nil {
			// Already gone, perhaps half-way: what it left in zellij and the
			// process table can still be cleaned up.
			if w := cleanupTreeTabs(name, nil); len(w) == 0 {
				fmt.Fprintf(os.Stderr, "(%s is not a supatree; closed any tabs and agents it left behind)\n", name)
			}
			return err
		}
		if !rmYes {
			fmt.Printf("Remove supatree %q and its %d member worktree(s) at %s? [y/N] ", name, len(inst.Members), inst.Root)
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if strings.ToLower(strings.TrimSpace(line)) != "y" {
				fmt.Println("aborted")
				return nil
			}
		}
		res, err := supatree.Remove(stCfg, name, supatree.RemoveOptions{Force: rmForce, Push: rmPush})
		if err != nil {
			return err
		}
		for _, w := range res.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		fmt.Printf("removed supatree %q\n", name)
		// Last: if this runs in a shell inside one of the tree's tabs, closing
		// it ends this process.
		cleanupTreeTabs(name, res)
		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVar(&rmForce, "force", false, "remove even with uncommitted stack changes")
	rmCmd.Flags().BoolVar(&rmPush, "push", false, "push the st/<name> branch before deleting it locally")
	rmCmd.Flags().BoolVarP(&rmYes, "yes", "y", false, "skip confirmation")
}

// cleanupTreeTabs closes a removed tree's tabs in every supatree session —
// stopping its agents — and deletes their layouts, printing and returning
// what went wrong.
func cleanupTreeTabs(tree string, res *supatree.RemoveResult) []string {
	warnings := supatree.CloseTree(supatreeWorkspace(), tree, res)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	return warnings
}
