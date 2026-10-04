package localpath

// Place is one starting point on the picker's first screen: a drive, or a
// folder most people mean. That screen is the reason a folder on D: can be
// reached at all — the picker used to open in the home folder, and walking
// up from there stopped at the top of C:.
type Place struct {
	// Name is what to show first: "Desktop", or a drive's letter, "D:".
	Name string
	// Label is the volume's own name — "Data" — shown next to the letter, so
	// a drive is recognised by what it holds rather than by which letter it
	// happened to get. Empty for a folder.
	Label string
	Path  string
	// Detail is "610 GB free of 1 TB", where that can be measured.
	Detail string
}
