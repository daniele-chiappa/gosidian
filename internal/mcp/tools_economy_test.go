package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const economyHot = `---
title: Hot
tags: [type:index]
---

# Hot

## Current focus

lots of prose here

## Active plans

more prose
`

type economyBootstrapView struct {
	DirectivesBlock   string `json:"directives_block"`
	DirectivesVersion int    `json:"directives_version"`
	HotMD             struct {
		Present     bool   `json:"present"`
		Content     string `json:"content"`
		ETag        string `json:"etag"`
		Unchanged   bool   `json:"unchanged"`
		Frontmatter string `json:"frontmatter"`
		Headings    []struct {
			Text string `json:"text"`
		} `json:"headings"`
	} `json:"hot_md"`
	Readme struct {
		Content   string `json:"content"`
		Unchanged bool   `json:"unchanged"`
	} `json:"readme"`
}

func bootstrapView(t *testing.T, s *Server, args map[string]any) economyBootstrapView {
	t.Helper()
	res, _ := s.handleBootstrap(context.Background(), call(args))
	var v economyBootstrapView
	if err := json.Unmarshal([]byte(resultText(t, res)), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMCP_BootstrapKnownVersionAndEtags(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/hot.md", "content": economyHot}))
	resultText(t, res)
	res, _ = s.handleCreate(ctx, call(map[string]any{"path": "p/README.md", "content": "# Readme\nbody"}))
	resultText(t, res)

	full := bootstrapView(t, s, map[string]any{"project": "p"})
	if full.DirectivesBlock == "" || full.HotMD.Content == "" || full.HotMD.ETag == "" {
		t.Fatalf("full bootstrap missing block/content/etag: %+v", full)
	}

	// Matching version + etag: directives omitted, hot body omitted, readme
	// (etag not supplied) still full.
	slim := bootstrapView(t, s, map[string]any{
		"project":                  "p",
		"known_directives_version": full.DirectivesVersion,
		"known_etags":              map[string]any{"p/hot.md": full.HotMD.ETag},
	})
	if slim.DirectivesBlock != "" {
		t.Fatal("directives_block must be omitted when known version matches")
	}
	if slim.DirectivesVersion != full.DirectivesVersion {
		t.Fatal("directives_version must always be present")
	}
	if !slim.HotMD.Unchanged || slim.HotMD.Content != "" || slim.HotMD.ETag != full.HotMD.ETag {
		t.Fatalf("hot_md with matching etag = %+v, want unchanged+empty content", slim.HotMD)
	}
	if slim.Readme.Content == "" || slim.Readme.Unchanged {
		t.Fatalf("readme without known etag must stay full: %+v", slim.Readme)
	}

	// Stale version/etag: everything comes back full.
	stale := bootstrapView(t, s, map[string]any{
		"project":                  "p",
		"known_directives_version": full.DirectivesVersion - 1,
		"known_etags":              map[string]any{"p/hot.md": "stale-etag"},
	})
	if stale.DirectivesBlock == "" || stale.HotMD.Content == "" || stale.HotMD.Unchanged {
		t.Fatalf("stale known_* must return full payload: %+v", stale.HotMD)
	}
}

func TestMCP_BootstrapLiteMode(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/hot.md", "content": economyHot}))
	resultText(t, res)
	res, _ = s.handleCreate(ctx, call(map[string]any{"path": "p/README.md", "content": "# Readme\nbody"}))
	resultText(t, res)

	lite := bootstrapView(t, s, map[string]any{"project": "p", "mode": "lite"})
	if lite.HotMD.Content != "" {
		t.Fatal("lite mode must omit the hot.md body")
	}
	if len(lite.HotMD.Headings) == 0 || !strings.Contains(lite.HotMD.Frontmatter, "title: Hot") {
		t.Fatalf("lite mode must carry outline+frontmatter: %+v", lite.HotMD)
	}
	if lite.Readme.Content == "" {
		t.Fatal("lite mode only strips hot.md, not README")
	}

	res, _ = s.handleBootstrap(ctx, call(map[string]any{"project": "p", "mode": "bogus"}))
	expectError(t, res)
}

func TestMCP_BatchGetModesAndTruncation(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/big.md", "content": economyHot}))
	resultText(t, res)

	var out struct {
		Results []batchGetEntry `json:"results"`
	}

	res, _ = s.handleBatchGet(ctx, call(map[string]any{"paths": []any{"p/big.md"}, "mode": "outline"}))
	decodeResult(t, res, &out)
	if e := out.Results[0]; e.Content != "" || len(e.Headings) == 0 || e.ETag == "" {
		t.Fatalf("outline entry = %+v", e)
	}

	res, _ = s.handleBatchGet(ctx, call(map[string]any{"paths": []any{"p/big.md"}, "mode": "frontmatter"}))
	decodeResult(t, res, &out)
	if e := out.Results[0]; e.Content != "" || !strings.Contains(e.Frontmatter, "title: Hot") {
		t.Fatalf("frontmatter entry = %+v", e)
	}

	res, _ = s.handleBatchGet(ctx, call(map[string]any{"paths": []any{"p/big.md"}, "max_bytes_per_note": 10}))
	decodeResult(t, res, &out)
	if e := out.Results[0]; !e.Truncated || len(e.Content) != 10 {
		t.Fatalf("truncated entry = %+v", e)
	}

	res, _ = s.handleBatchGet(ctx, call(map[string]any{"paths": []any{"p/big.md"}, "mode": "bogus"}))
	expectError(t, res)
}
