extends Node


## Never write use_threads = true here; see FixtureHttpClient.
class Thing:
	var use_threads := false


func _ready() -> void:
	var thing := Thing.new()
	thing.use_threads = true
	var allowed := FixtureHttpClient.begin(self)
	if allowed.use_threads:
		return
	var forbidden := HTTPRequest.new()
	forbidden.use_threads = true
	# gdkit:ignore = restricted-api
	var exempted := HTTPRequest.new()
	remove_child(exempted)
