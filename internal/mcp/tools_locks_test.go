package mcp

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
)

// Two concurrent creates of the same note must yield exactly one success:
// the exists-probe and the write have to sit under the per-path lock, as
// every other create path already does (BUG-038).
func TestCreatePaths_HoldPathLock(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(onePxPNG)
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(s *Server, dir string)
		act   func(s *Server, i int) bool
	}{
		{"promote_agent", func(s *Server, dir string) {}, func(s *Server, i int) bool {
			res, _ := s.handlePromoteAgent(ctx, call(map[string]any{
				"project": "p", "slug": "dup", "content": "---\nname: dup\n---\nbody " + string(rune('a'+i)) + "\n",
			}))
			return !res.IsError
		}},
		{"media_note", func(s *Server, dir string) {
			s.vault.SetMediaNotes(true)
			writeVaultFile(t, dir, "p/attachments/x.png", string(png))
		}, func(s *Server, i int) bool {
			res, _ := s.handleCreateMediaNote(ctx, call(map[string]any{
				"project": "p", "title": "same", "attachment": "p/attachments/x.png", "caption": "c",
			}))
			return !res.IsError
		}},
		{"table_note", func(s *Server, dir string) {
			s.vault.SetTableNotes(true)
			writeVaultFile(t, dir, "p/attachments/t.csv", "a,b\n1,2\n")
		}, func(s *Server, i int) bool {
			res, _ := s.handleCreateTableNote(ctx, call(map[string]any{
				"project": "p", "title": "same", "attachment": "p/attachments/t.csv", "caption": "c",
			}))
			return !res.IsError
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for round := 0; round < 20; round++ {
				s, _, dir := newTestServer(t)
				tc.setup(s, dir)
				var wg sync.WaitGroup
				var mu sync.Mutex
				ok := 0
				for i := 0; i < 4; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						if tc.act(s, i) {
							mu.Lock()
							ok++
							mu.Unlock()
						}
					}(i)
				}
				wg.Wait()
				if ok != 1 {
					t.Fatalf("round %d: %d concurrent creates succeeded, want exactly 1", round, ok)
				}
			}
		})
	}
}
