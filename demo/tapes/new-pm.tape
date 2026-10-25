# Add a second PM for a narrower job: docs. It finds the gap and starts a
# tree to fill it.

Output demo/out/new-pm.mp4
Source demo/tapes/lib/settings.tape
Source demo/tapes/lib/start.tape
Sleep 2s

Source demo/tapes/lib/sidebar.tape
Type "p"
Sleep 800ms
Type "docs"
Sleep 500ms
Enter
Wait+Screen /I'm the docs PM/
Sleep 1s

Type "What needs docs work right now?"
Sleep 600ms
Enter
Wait+Screen /shall I start a tree/
Sleep 2s
Type "Yes, go ahead."
Sleep 600ms
Enter
Wait+Screen /report when its PR is up/
Sleep 2s
Screenshot demo/out/new-pm-1-started.png
Sleep 4s
