extends Node


func many_returns(value):
	if value == 1:
		return 1
	if value == 2:
		return 2
	if value == 3:
		return 3
	if value == 4:
		return 4
	if value == 5:
		return 5
	if value == 6:
		return 6
	return 7


func many_arguments(a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11):
	print(a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11)
