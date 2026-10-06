extends Node


func sign_of(value):
	if value < 0:
		return -1
	return 1


func describe(value):
	match value:
		0:
			return "zero"
		_:
			return "other"


func log_value(value):
	print(value)
