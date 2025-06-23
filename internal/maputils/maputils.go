package maputils

import (
	"maps"
	"slices"
)

func SortedKeys[T any](stringMap map[string]T) []string {
	return slices.Sorted(maps.Keys(stringMap))
}
