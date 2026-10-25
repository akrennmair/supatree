# The sidebar: trees in every state, their pull requests, and the dashboard.

Output demo/out/tour.gif
Output demo/out/tour.mp4

Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 15
Set Width 1600
Set Height 900
Set Padding 12
Set TypingSpeed 60ms
Set WaitTimeout 90s

Hide
Type `eval "$(demo/setup.sh env)" && clear && supatree start demo` Enter
Wait+Screen /lisbon is fresh/
Show
Sleep 3s

# Into the sidebar, and a refresh that fetches every tree's PRs.
Ctrl+p
Sleep 300ms
Type "h"
Sleep 300ms
Escape
Sleep 500ms
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
