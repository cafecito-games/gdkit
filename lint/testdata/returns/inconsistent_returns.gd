extends Node


# Returns a value on two paths and falls off its end on a third.
func clamped(value):
	if value < 0:
		return 0
	if value > 1:
		return 1


# An endless loop cannot fall through, so this function is complete.
func poll(ready):
	while true:
		if ready:
			return 1
