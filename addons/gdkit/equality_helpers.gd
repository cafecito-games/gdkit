## Structural equality for the `deep_equals` methods that `gdkit gen` writes.
##
## Generated code compares every field through [method deep_equals] rather than
## with `==`, because `==` compares an Object by reference and so reports two
## equal but distinct value objects as different.
class_name GDKitEquality
extends RefCounted


## Returns true when [param p_lhs] and [param p_rhs] hold equal values, looking
## inside Arrays and Dictionaries and deferring to an Object's own
## `deep_equals` or `equals` when it declares one.
static func deep_equals(p_lhs: Variant, p_rhs: Variant) -> bool:
	if p_lhs == p_rhs:
		return true
	if p_lhs is Array and p_rhs is Array:
		if p_lhs.size() != p_rhs.size():
			return false
		for index in p_lhs.size():
			if not deep_equals(p_lhs[index], p_rhs[index]):
				return false
		return true
	if p_lhs is Dictionary and p_rhs is Dictionary:
		if p_lhs.size() != p_rhs.size():
			return false
		for key in p_lhs:
			if not p_rhs.has(key):
				return false
			if not deep_equals(p_lhs[key], p_rhs[key]):
				return false
		return true
	if p_lhs == null or p_rhs == null:
		return false
	if p_lhs is Object and p_lhs.has_method("deep_equals"):
		return p_lhs.deep_equals(p_rhs)
	if p_lhs is Object and p_lhs.has_method("equals"):
		return p_lhs.equals(p_rhs)
	return false
