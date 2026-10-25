# Start a tree from the sidebar for a change across api and web. Its agent
# commits in both, opens the PRs in dependency order, and the sidebar follows
# them through CI to approval.

Output demo/out/new-tree.mp4
Source demo/tapes/lib/settings.tape
Source demo/tapes/lib/start.tape
Sleep 2s

Source demo/tapes/lib/sidebar.tape
Type "n"
Sleep 800ms
Type "bulk-refunds"
Sleep 500ms
Enter
# The new tree is selected once it exists; enter opens its agent.
Wait+Screen /created bulk-refunds/
Sleep 1s
Enter
Wait+Screen /What are we building/
Sleep 1s

Type "Bulk refunds: an api endpoint that refunds a list of paid orders, and a button in web that calls it."
Sleep 600ms
Enter
Wait+Screen /cross-linked/
Screenshot demo/out/new-tree-1-prs.png
Sleep 3s

# The sidebar follows the new PRs: CI passes, then approval.
Source demo/tapes/lib/sidebar.tape
Type "r"
Sleep 20s
Type "r"
Sleep 4s
Screenshot demo/out/new-tree-2-passing.png
Sleep 16s
Type "r"
Sleep 5s
Screenshot demo/out/new-tree-3-approved.png
