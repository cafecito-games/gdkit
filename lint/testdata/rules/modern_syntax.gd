extends Node

signal changed(value: int)

enum Mode { IDLE, RUNNING }

const LOOKUP: Dictionary[String, int] = {"one": 1, "two": 2}

static var instances: Array[Node] = []

@export_group("Tuning")
@export var speed: float = 1.0

var names: Array[String] = []
var scores: Dictionary[String, int] = {}
var match: int = 0
var health: int = 10:
	get:
		# Reads go through the accessor.
		return health
	set(value):
		# Negative health is clamped.
		if value < 0:
			health = 0
		else:
			health = value
var armor: int:
	get:
		if armor > 0:
			return armor
		else:
			return 0
	set(value):
		armor = value
var _cache: Dictionary = {}:
	set(BadValue):
		_cache = BadValue


func named_lambda(items: Array) -> Array:
	var double := func double_value(item): return item * 2
	return items.map(double)


func lambda_only(ignored, factor):
	var scale := func(item): return item * factor  # one-line lambda
	return scale


func lambda_names():
	var BadLambda = func(BadParameter): return BadParameter
	var quiet = func(unused_in_lambda): return 1
	return [BadLambda, quiet]


func lambda_branches(limit):
	var clamp_value = func(item):
		if item > limit:
			return limit
		else:
			return item
	var same = func(item): return item == item
	var report = func():
		print(limit)
		pass
	return [clamp_value, same, report]


func describe(value, fallback):
	match value:
		var bound when bound is int:
			return bound
		var BadBind:
			print(BadBind)
		1, 2, 3:
			return "small"
		[var first, ..]:
			return first
		{"key": var inner}:
			return inner
		_:
			pass
	return fallback


func guarded(value, limit):
	match value:
		var bound when bound > limit:
			bound
		_ when limit == limit:
			pass


func lua_style(width, height):
	var size = {width = width, height = 2}
	{depth = 3}
	return size


func variadic(first, ...arguments: Array):
	return first


func variadic_used(...arguments: Array):
	return arguments.size()


func keyword_members(target):
	target.match = 1
	target.signal
	return target.class.is_empty()


func typed_locals():
	var counts: Dictionary[String, int] = {}
	var BadTyped: Array[int] = []
	for index: int in BadTyped:
		counts[str(index)] = index
	return counts
