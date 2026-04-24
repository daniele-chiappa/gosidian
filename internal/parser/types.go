package parser

type WikiLinkRef struct {
	Target string // raw target, e.g. "Some Note" or "folder/note"
	Alias  string // may be empty
}
