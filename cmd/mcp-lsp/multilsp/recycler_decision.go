package multilsp

func (d recyclerMemoryDecision) exceeded() bool {
	return d.processExceeded || d.cohort.EvictSelf
}

func (d recyclerMemoryDecision) reasonAndLimit() (string, uint64) {
	if d.cohort.EvictSelf {
		return "cohort_rss_limit", d.cohort.HardLimit
	}
	return "process_tree_rss_limit", d.processLimit
}
