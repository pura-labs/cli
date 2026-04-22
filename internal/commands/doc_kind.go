package commands

// kindSubstrateLabel joins a kind + substrate pair into a short human
// label used in CLI summaries. When kind and substrate diverge (e.g.
// kind=sheet, substrate=csv) both are shown; when they're the same or
// one is empty, the non-empty value stands alone.
func kindSubstrateLabel(kind, substrate string) string {
	switch {
	case kind != "" && substrate != "" && substrate != kind:
		return kind + "/" + substrate
	case kind != "":
		return kind
	case substrate != "":
		return substrate
	default:
		return "-"
	}
}
