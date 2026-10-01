extends Node


func else_return(value):
	if value:
		return 1
	else:
		return 2


func elif_return(value):
	if value == 1:
		return 1
	elif value == 2:
		return 2
	return 3
