package dbschema

import (
	"errors"
	"sort"
	"strings"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
)

// maxDatabases caps the database notes read for one project: a project
// keeps a handful, and every write to a row reads them.
const maxDatabases = 50

// ForProject returns the parsed database notes of a project, sorted by path.
// A database note whose schema does not parse is returned in bad with the
// reason, so lint can report it instead of skipping its rows silently.
func ForProject(idx *index.Index, v *vault.Vault, project string) (schemas []*Schema, bad map[string]error, err error) {
	hits, _, err := idx.Query(index.QueryOptions{
		Projects: []string{project},
		Where:    []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{"database"}}},
		Sort:     "path",
		Limit:    maxDatabases,
	})
	if err != nil {
		return nil, nil, err
	}
	for _, h := range hits {
		note, err := v.Load(h.Path)
		if err != nil {
			continue // stale index entry: the next scan drops it
		}
		s, err := Parse(h.Path, parser.FrontmatterRawForPath(h.Path, note.Content))
		switch {
		case errors.Is(err, ErrNotDatabase):
			continue
		case err != nil:
			if bad == nil {
				bad = map[string]error{}
			}
			bad[h.Path] = err
		default:
			schemas = append(schemas, s)
		}
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].Path < schemas[j].Path })
	return schemas, bad, nil
}

// Covering returns the schema of the database whose rows include rel, or
// nil when rel is not a row of any database of its project.
func Covering(idx *index.Index, v *vault.Vault, rel string) (*Schema, error) {
	project, _, ok := strings.Cut(rel, "/")
	if !ok {
		return nil, nil
	}
	schemas, _, err := ForProject(idx, v, project)
	if err != nil {
		return nil, err
	}
	for _, s := range schemas {
		if s.Covers(rel) {
			return s, nil
		}
	}
	return nil, nil
}
