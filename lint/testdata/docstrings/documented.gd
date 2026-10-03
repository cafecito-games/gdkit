class_name DocumentedThing
extends Node
## A thing that documents every public member.

## Emitted once the thing has happened.
signal thing_happened

## The kinds of thing there are.
enum Kind { A, B }

## How many things fit.
const MAX_THINGS = 3

## How many things there are.
var count := 0
var _hidden := 0

## Does the thing.
##
## The second paragraph of a documentation block is still the same block.
func do_thing() -> void:
	pass

func _private_thing() -> void:
	pass

## Builds a thing.
# gdkit:ignore = max-returns
static func make() -> DocumentedThing:
	return null

## A nested thing.
class Inner:
	## The value of the nested thing.
	var value := 0

	## Runs the nested thing.
	func run() -> void:
		pass
