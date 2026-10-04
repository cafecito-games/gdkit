class_name FixtureLogger

signal logged(level: String, message: String)

var _sink: Callable


func _init(sink: Callable) -> void:
	_sink = sink


func warn(message: String) -> void:
	emit("WARN", message)


func error(message: String) -> void:
	emit("ERROR", message)


func emit(level: String, message: String) -> void:
	_sink.call(level, message)
	logged.emit(level, message)
