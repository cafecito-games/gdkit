extends Node

const A = preload("res://a.gd")
const B = preload("res://a.gd")


func unneeded(value):
	print(value)
	pass


func unused(first, second):
	print(first)


func expression():
	var x = 1
	x == 2
	x + 1


func itself(value):
	if value == value:
		print("same")
