# The sidebar: trees in every state, their pull requests, and the dashboard.

Output demo/out/tour.gif
Output demo/out/tour.mp4

Source demo/tapes/lib/settings.tape

Source demo/tapes/lib/start.tape
Sleep 3s

# Into the sidebar, and a refresh that fetches every tree's PRs.
Source demo/tapes/lib/sidebar.tape
Type "r"
Wait+Screen /#16/
Screenshot demo/out/tour-1-sidebar.png
Sleep 2s
Type "jjjj"
Sleep 2s

# The dashboard.
Type "D"
Wait+Screen /order-export/
Sleep 4s
Screenshot demo/out/tour-2-dash.png
