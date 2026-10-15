package match

import "slices"

// radixMin is the size below which slices.Sort beats the radix sort's
// fixed per-pass cost.
const radixMin = 512

// sortVotes sorts a in ascending order using buf as scratch (len(buf) >=
// len(a)) and returns the sorted slice, which is either a or buf.
//
// LSD radix sort, one byte per pass, skipping any byte that is the same in
// every key. Votes are track<<32 | (offset + 2^31): track ids and offsets
// are small, so only ~3-4 of the 8 bytes vary, and the sort costs that
// many linear passes instead of pdqsort's ~log2(n) comparisons per element.
// pprof showed pdqsort at 54% of shard CPU at saturation (D22).
func sortVotes(a, buf []uint64) []uint64 {
	if len(a) < radixMin {
		slices.Sort(a)
		return a
	}
	var all0, all1 uint64 = 0, ^uint64(0) // OR and AND of every key
	for _, v := range a {
		all0 |= v
		all1 &= v
	}
	varying := all0 ^ all1 // bits that are not constant across keys
	src, dst := a, buf[:len(a)]
	var count [256]int
	for shift := uint(0); shift < 64; shift += 8 {
		if (varying>>shift)&0xFF == 0 {
			continue // this byte is identical in every key: already "sorted"
		}
		clear(count[:])
		for _, v := range src {
			count[byte(v>>shift)]++
		}
		sum := 0
		for i, c := range count {
			count[i] = sum
			sum += c
		}
		for _, v := range src {
			d := byte(v >> shift)
			dst[count[d]] = v
			count[d]++
		}
		src, dst = dst, src
	}
	return src
}
