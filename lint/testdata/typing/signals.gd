extends Node

signal damaged(amount)
signal healed(amount: int, source: Node)
signal scattered(hits: Array)


func _on_hit() -> void:
	damaged.emit(1)
	healed.emit(1, self)
	scattered.emit([1])
