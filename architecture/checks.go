package architecture

import (
	"fmt"
	"sort"
	"strings"
)

func (a *Analyzer) checkDependencies(files map[string]*parsedFile, edges []Edge, diagnostics *[]Diagnostic) {
	for _, edge := range edges {
		from := files[edge.From]
		if from == nil || from.file.Classification.Layer == "" {
			continue
		}
		to, ok := a.Config.classify(edge.To)
		if !ok {
			continue
		}
		if a.Config.dependencyAllowed(from.file, edge.To, to) {
			continue
		}
		*diagnostics = append(*diagnostics, Diagnostic{
			Rule: "dependency.direction", Severity: SeverityError, Symbol: edge.Symbol, Target: edge.To,
			Message: fmt.Sprintf("%s/%s may not depend on %s/%s (%s)",
				from.file.Classification.Feature, from.file.Classification.Layer, to.Feature, to.Layer, edge.To),
			Location: edge.Location,
		})
	}
}

func (a *Analyzer) checkCycles(edges []Edge, diagnostics *[]Diagnostic) {
	graph := make(map[string][]string)
	locations := make(map[string]Location)
	for _, edge := range edges {
		if !strings.HasSuffix(edge.To, ".gd") {
			continue
		}
		graph[edge.From] = appendUnique(graph[edge.From], edge.To)
		key := edge.From + "\x00" + edge.To
		if _, exists := locations[key]; !exists {
			locations[key] = edge.Location
		}
		if _, exists := graph[edge.To]; !exists {
			graph[edge.To] = nil
		}
	}
	for node := range graph {
		sort.Strings(graph[node])
	}

	index := 0
	indices := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var components [][]string
	var strongConnect func(string)
	strongConnect = func(node string) {
		indices[node] = index
		lowlink[node] = index
		index++
		stack = append(stack, node)
		onStack[node] = true
		for _, target := range graph[node] {
			if _, visited := indices[target]; !visited {
				strongConnect(target)
				lowlink[node] = min(lowlink[node], lowlink[target])
			} else if onStack[target] {
				lowlink[node] = min(lowlink[node], indices[target])
			}
		}
		if lowlink[node] != indices[node] {
			return
		}
		var component []string
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == node {
				break
			}
		}
		if len(component) > 1 || len(component) == 1 && sliceContains(graph[component[0]], component[0]) {
			components = append(components, component)
		}
	}

	var nodes []string
	for node := range graph {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if _, visited := indices[node]; !visited {
			strongConnect(node)
		}
	}
	for _, component := range components {
		sort.Strings(component)
		cycle := cyclePath(component, graph)
		from, to := cycle[0], cycle[1]
		*diagnostics = append(*diagnostics, Diagnostic{
			Rule: "dependency.cycle", Severity: SeverityError, Target: to,
			Message:  "dependency cycle: " + strings.Join(cycle, " -> "),
			Location: locations[from+"\x00"+to],
		})
	}
}

func cyclePath(component []string, graph map[string][]string) []string {
	inComponent := make(map[string]bool, len(component))
	for _, node := range component {
		inComponent[node] = true
	}
	start := component[0]
	var path []string
	visiting := make(map[string]bool)
	var find func(string) bool
	find = func(node string) bool {
		path = append(path, node)
		visiting[node] = true
		for _, target := range graph[node] {
			if !inComponent[target] {
				continue
			}
			if target == start {
				path = append(path, start)
				return true
			}
			if !visiting[target] && find(target) {
				return true
			}
		}
		visiting[node] = false
		path = path[:len(path)-1]
		return false
	}
	if find(start) {
		return path
	}
	return append(component, component[0])
}

func appendUnique(values []string, value string) []string {
	if sliceContains(values, value) {
		return values
	}
	return append(values, value)
}

func sliceContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
