# Ask the PM to review two pull requests that belong together. It opens a
# review tree, a reviewer agent reads the change and posts both reviews, and
# the PM reports back.

Output demo/out/pm-review.mp4

Source demo/tapes/lib/settings.tape

Source demo/tapes/lib/start.tape
Sleep 2s

Type "Can you review acme/api#17 and acme/web#42? Jonas and Mira want them in today."
Sleep 600ms
Enter
Wait+Screen /reviewer is working/
Screenshot demo/out/pm-review-1-asked.png
Sleep 3s

# Over to the reviewer's tab, which the watcher opened in the background.
Ctrl+t
Sleep 300ms
Type "l"
Sleep 300ms
Escape
Wait+Screen /Both reviews are posted/
Screenshot demo/out/pm-review-2-reviewed.png
Sleep 3s

# Back to the PM, which has picked up the reviewer's report.
Ctrl+t
Sleep 300ms
Type "h"
Sleep 300ms
Escape
Wait+Screen /The review is in/
Sleep 4s
Screenshot demo/out/pm-review-3-reported.png
Sleep 1s
