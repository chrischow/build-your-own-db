package free

// Convert a pointer to a monotically increasing index
func seq2idx(seq uint64) int {
	return int(seq % LNODE_CAP)
}
