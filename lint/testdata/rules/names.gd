class_name my_class
extends Node

signal BadSignal
enum bad_enum { lower_element, GOOD_ELEMENT }
const badConstant = 1
const bad_load = preload("res://a.gd")
var BadVariable = 1
var BAD_loaded = preload("res://b.gd")

class bad_sub:
	var x = 1


func BadFunction(BadArgument):
	var BadLocal = 1
	var bad_preload = preload("res://c.gd")
	for BadLoop in range(3):
		print(BadLoop)
	print(BadArgument, BadLocal, bad_preload)
