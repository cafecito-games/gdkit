extends Node

const LIMIT := 10

@export var speed = 1.0

var items: Array = []
var lookup: Dictionary[String, int] = {}
var pending := []
var cache := {}


func tally() -> int:
	var total = 0
	var seen := {}
	for value in items:
		total += value
	return total + LIMIT + int(speed) + lookup.size() + pending.size() + cache.size() + seen.size()
