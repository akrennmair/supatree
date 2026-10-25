# The authors merged what was reviewed. Close the review tree, and ask what
# the week amounted to.

Output demo/out/close-review.mp4
Source demo/tapes/lib/settings.tape
Source demo/tapes/lib/start.tape
Sleep 2s

Type "Yes, close it."
Sleep 600ms
Enter
Wait+Screen /Anything else\?/
Screenshot demo/out/close-review-1-closed.png
Sleep 2s

Type "What did we finish this week?"
Sleep 600ms
Enter
Wait+Screen /only needs merging/
Sleep 5s
Screenshot demo/out/close-review-2-history.png
Sleep 1s
