extends Node

# https://docs.godotengine.org/en/stable/classes/class_resourceloader.html#class-resourceloader-method-load-threaded-request
const SCENE_PATH := "res://features/combat/presentation/widgets/health_bar/variants/large/health_bar_with_a_very_long_name.tscn"


func irreducible_line():
	var kind := ENTITY_TYPE_WORLD_PROP_DESTRUCTIBLE_CRATE_WITH_A_VERY_SPECIFIC_GENERATED_SUFFIX_THAT_NOBODY_CHOSE_BY_HAND
	print(kind, SCENE_PATH)
