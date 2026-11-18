class_name FixtureHttpClient
extends Node


static func begin(parent: Node) -> HTTPRequest:
	var request := HTTPRequest.new()
	parent.add_child(request)
	return request
