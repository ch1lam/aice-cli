package tui

import (
	"strings"
	"unicode"
)

// fuzzyMatch ranks ordered subsequences and returns rune offsets for the same
// alignment. Consecutive letters and word starts beat scattered matches. This
// small local scorer does not implement Nucleo's query language or MRU ranking.
func fuzzyMatch(value, query string) (int, []int) {
	text := []rune(value)
	pattern := []rune(strings.ToLower(query))
	if len(pattern) == 0 {
		return 0, nil
	}
	if len(pattern) > len(text) {
		return -1, nil
	}
	const missing = -1 << 30
	previous := make([]int, len(text))
	parents := make([][]int, len(pattern))
	for i, target := range pattern {
		current := make([]int, len(text))
		parents[i] = make([]int, len(text))
		best, bestIndex := missing, -1
		for j, character := range text {
			current[j], parents[i][j] = missing, -1
			if j > 0 && previous[j-1]+j-1 > best {
				best, bestIndex = previous[j-1]+j-1, j-1
			}
			if unicode.ToLower(character) != target {
				continue
			}
			bonus := 10
			if j == 0 || !unicode.IsLetter(text[j-1]) && !unicode.IsDigit(text[j-1]) ||
				unicode.IsLower(text[j-1]) && unicode.IsUpper(character) {
				bonus += 12
			}
			if i == 0 {
				current[j] = bonus - j
				continue
			}
			if bestIndex >= 0 && previous[bestIndex] != missing {
				current[j], parents[i][j] = best-j+1+bonus, bestIndex
			}
			if j > 0 && previous[j-1] != missing && previous[j-1]+bonus+20 > current[j] {
				current[j], parents[i][j] = previous[j-1]+bonus+20, j-1
			}
		}
		previous = current
	}
	best, end := missing, -1
	for j, score := range previous {
		if score > best {
			best, end = score, j
		}
	}
	if end < 0 {
		return -1, nil
	}
	indices := make([]int, len(pattern))
	for i := len(pattern) - 1; i >= 0; i-- {
		indices[i] = end
		end = parents[i][end]
	}
	if strings.EqualFold(value, query) {
		best += 1000
	}
	return max(best, 0), indices
}
