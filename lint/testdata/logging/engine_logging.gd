class_name EngineLoggingFixture
extends Node

const Logger = preload("res://logging/logger.gd")

var _logger := Logger.new(_write)


func _ready() -> void:
	push_warning("configuration is stale")
	push_error("configuration is missing")
	print("ready")
	prints("ready", name)
	printt("ready", name)
	printraw("ready")
	printerr("ready")
	print_rich("[b]ready[/b]")
	print_debug("ready")
	print_stack()
	OS.alert("ready")


func _notify(message: String) -> void:
	# A call through an object reaches the project's logger, not the engine, so
	# none of these are reported.
	_logger.error(message)
	_logger.push_error(message)
	_logger.print(message)
	self.print(message)
	# gdkit:ignore = no-engine-logging
	push_error(message)


func _write(level: String, message: String) -> void:
	_logger.emit(level, message)
