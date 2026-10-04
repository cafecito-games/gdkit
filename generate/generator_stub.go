package generate

// Temporary until the registry lands in Task 7.
func isGeneratorName(name string) bool {
	return name == "to_string" || name == "equals"
}

func GeneratorNames() []string { return []string{"equals", "to_string"} }
