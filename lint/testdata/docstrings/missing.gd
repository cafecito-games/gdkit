class_name UndocumentedThing
extends Node

signal thing_happened

enum Kind { A, B }

const MAX_THINGS = 3

var count := 0
var _hidden := 0

func do_thing() -> void:
	pass

func _private_thing() -> void:
	pass

static func make() -> UndocumentedThing:
	return null

class Inner:
	var value := 0

	func run() -> void:
		pass
