extends Node

var handler = func(event): return event


func _ready():
	var speed := compute(1, 2.0)
	print(speed)


func compute(first, second: float) -> float:
	return first + second


func typed(value: int) -> int:
	var double := func(amount): return amount * 2
	return double.call(value)
