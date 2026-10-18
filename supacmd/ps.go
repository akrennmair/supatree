package supacmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/panamafrancis/supatree/pkg/supatree"
)

var (
	psOrphans   bool
	killOrphans bool
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running agent processes, and which have lost their tab",
	Long: "ps lists every agent and PM process supatree launched and that is still\n" +
		"running, from the records `agent-exec` keeps while each runs. An orphan is one\n" +
		"whose tab is gone — left by a crash, a racing `supatree pm`, or a removed tree —\n" +
		"and still holding its memory; `supatree kill --orphans` stops them.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		procs := supatree.ListProcs()
		tabs := supatree.SessionTabs(procs)
		if psOrphans {
			procs = supatree.Orphans(procs, tabs)
			if len(procs) == 0 {
				fmt.Println("no orphaned agent processes")
				return nil
			}
		}
		fmt.Print(supatree.FormatProcs(procs, tabs, time.Now()))
		return nil
	},
}

var killCmd = &cobra.Command{
	Use:   "kill [<pid> | <tree>[:<agent>]]",
	Short: "Stop agent processes: one, a tree's, or every orphan",
	Long: "kill stops agent processes supatree recorded — never an arbitrary pid. It\n" +
		"hangs up on each, as closing its tab would, and kills it if it has not exited\n" +
		"within a few seconds. `--orphans` stops every process whose tab is gone.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t := supatree.StopTarget{Orphans: killOrphans}
		if len(args) == 1 {
			if pid, err := strconv.Atoi(args[0]); err == nil {
				t.PID = pid
			} else {
				t.Tree, t.Agent, _ = strings.Cut(args[0], ":")
			}
		}
		stopped, err := supatree.StopAgents(t)
		for _, r := range stopped {
			fmt.Printf("stopped %s (pid %d)\n", r.Who(), r.PID)
		}
		if len(stopped) == 0 && err == nil {
			return errors.New("no recorded agent process matched — `supatree ps` lists them")
		}
		return err
	},
}

func init() {
	psCmd.Flags().BoolVar(&psOrphans, "orphans", false, "only processes whose tab is gone")
	killCmd.Flags().BoolVar(&killOrphans, "orphans", false, "stop every process whose tab is gone")
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(killCmd)
}
