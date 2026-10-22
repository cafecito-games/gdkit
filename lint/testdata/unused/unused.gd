extends Node


const _DEAD = 1


const _USED = 2


func _dead():
	pass


func _helper():
	return _USED


func _ready():
	_helper()
