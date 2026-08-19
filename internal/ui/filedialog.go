package ui

// FileFilter names one group of kinds a picker offers, as the shell wants it:
// a line in the combo box and the patterns behind it. Extensions carry their
// dot (".png") — each picker spells them the way its own API asks.
type FileFilter struct {
	Label      string
	Extensions []string
}
