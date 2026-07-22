package scenarios

import "math/bits"

// assignmentBits stores one truth value per complete slate assignment. Slate
// searches can contain tens of thousands of assignments, so a packed set is
// materially smaller than []bool and makes coverage comparison/counting cheap.
type assignmentBits struct {
	words []uint64
	size  int
}

func newAssignmentBits(size int) assignmentBits {
	return assignmentBits{words: make([]uint64, (size+63)/64), size: size}
}

func (b assignmentBits) set(index int) {
	b.words[index/64] |= uint64(1) << uint(index%64)
}

func (b assignmentBits) has(index int) bool {
	return b.words[index/64]&(uint64(1)<<uint(index%64)) != 0
}

func (b assignmentBits) count() int {
	total := 0
	for _, word := range b.words {
		total += bits.OnesCount64(word)
	}
	return total
}

func (b assignmentBits) equal(other assignmentBits) bool {
	if b.size != other.size || len(b.words) != len(other.words) {
		return false
	}
	for i := range b.words {
		if b.words[i] != other.words[i] {
			return false
		}
	}
	return true
}

func (b assignmentBits) each(f func(int)) {
	for wordIndex, word := range b.words {
		for word != 0 {
			bit := bits.TrailingZeros64(word)
			index := wordIndex*64 + bit
			if index < b.size {
				f(index)
			}
			word &= word - 1
		}
	}
}
