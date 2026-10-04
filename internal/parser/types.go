package parser

type WikiLinkRef struct {
	Target string // raw target, e.g. "Some Note" or "folder/note"
	Alias  string // may be empty
	// Field is the frontmatter key the link was written in (IMP-127
	// iteration 2), "" for a link in the body.
	Field string
}
